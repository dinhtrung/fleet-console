package act

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/dinhtrung/fleet-console/internal/decide"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// TickInput is one project's decide plan plus the snapshot it came from. Apply
// false makes the tick a pure no-op; the caller keeps the dry-run output.
type TickInput struct {
	Store        *store.Store
	Project      store.ProjectRow
	Forge        forge.Forge
	Rig          Rig
	Git          Git
	Gate         Gate
	Apply        bool
	Items        []forge.Item
	CRs          []forge.ChangeRequest
	Rows         []rig.QueueRow
	Plan         decide.Plan
	WorktreeRoot string
	// RelayMode and RelaySeat are the project's verdict_relay resolution
	// (verdict-relay, design D8): the caller resolves them from
	// config.RelayFor. Empty is relay off, today's behaviour: operator
	// questions stay on human@kernel and a relay intent is skipped with the
	// named reason.
	RelayMode string
	RelaySeat string
	Now       func() time.Time
	// LifecycleFailed names, per rig, the launch failure this tick hit: every
	// dispatch intent on that rig is skipped with the same named reason rather
	// than writing a worktree and a row no seat will ever claim (design D5).
	LifecycleFailed map[string]string
	// OnDecision receives every decision this tick raised, so the caller can
	// put it on somebody's phone. It is called after the row exists. It cannot
	// fail the tick: a dead transport is not a dead fleet (design.md §5), so a
	// nil hook and a broken one are the same to the caller — nothing is sent,
	// and the tick carries on.
	OnDecision func(DecisionRaised)
}

// DecisionRaised is one decision a tick created, described the way the phone
// message needs it and the way the tick already knows it.
type DecisionRaised struct {
	QItemID string // the queue row holding the question
	Item    int    // the issue it is about
	Text    string // the question, as the plan worded it
}

// TickReport is what one applied tick did, for the log and the board.
type TickReport struct {
	Applied    bool
	Blocked    bool
	Reason     string
	Dispatches []DispatchResult
	Verdicts   []CapturedVerdict
	Merges     []*MergeResult
	Decisions  []string
	// BlockedMerges lists the merge refusals this tick judged to be operator
	// obligations — a stall the next tick cannot fix (#252). The fact is
	// reported on every tick it holds; the rate limit per (item, reason, head
	// sha) belongs to the notifier, which pages from it.
	BlockedMerges []BlockedMerge
	// Epics lists the epics this tick closed by rollup or by the sweep, once each.
	Epics []EpicRollup
	// SweepReads is the number of forge sub-issue reads the sweep spent this tick:
	// one per open epic, which is the bound the design asks to keep visible. It is
	// separate from the forge's own budget so a reader can tell the sweep's share.
	SweepReads int
	Skipped    []string
}

var tickIssueNumber = regexp.MustCompile(`#(\d+)\b`)

// RunTick executes one project's plan. The gate is checked first: a blocked
// gate performs no write and returns the one named reason. Without Apply the
// tick is a no-op. With Apply it captures verdicts, merges on the four facts,
// then dispatches the plan; every actual write goes through Actions, which
// records one audit event per write.
func RunTick(ctx context.Context, in TickInput) (TickReport, error) {
	report := TickReport{Applied: in.Apply}
	if err := in.Gate.Check(); err != nil {
		report.Blocked = true
		report.Reason = refusalReason(err)
		return report, nil
	}
	if !in.Apply {
		return report, nil
	}
	if in.Store == nil || in.Forge == nil || in.Rig == nil || in.Git == nil {
		return report, fmt.Errorf("act tick: store, forge, rig and git are required")
	}
	actions := &Actions{
		Store: in.Store, Forge: in.Forge, Rig: in.Rig, Git: in.Git, Now: in.Now,
		ProjectID: in.Project.ID, ProjectKey: in.Project.Key, WorktreeRoot: in.WorktreeRoot,
	}

	// Verdict capture and the merge watchdog run before new dispatches: a merged
	// task frees its seat, and the same tick must not re-dispatch it.
	dispatches, err := in.Store.DispatchesForProject(ctx, in.Project.ID)
	if err != nil {
		return report, err
	}
	// The watchdog walks the open change requests, not the live dispatches. A builder
	// ends its row when it delivers, and that supersedes the dispatch; keying the
	// watchdog on live dispatches left accepted pull requests waiting on a row that no
	// longer existed, so nothing merged for hours while every check was green.
	latest := make(map[int]store.DispatchRow, len(dispatches))
	for _, dispatch := range dispatches {
		if dispatch.ItemNumber == 0 {
			continue
		}
		if current, ok := latest[dispatch.ItemNumber]; !ok || dispatch.ID > current.ID {
			latest[dispatch.ItemNumber] = dispatch
		}
	}
	rolled := make(map[int]bool)
	for _, request := range in.CRs {
		if request.Merged {
			continue
		}
		switch strings.ToLower(request.State) {
		case "closed", "merged":
			continue
		}
		item := forge.ParseCloses(request.Body)
		if item == 0 {
			item = request.ClosesItem
		}
		if item == 0 {
			continue
		}
		// Capture first, decide second (design D3): the verdict is a fact about the
		// change request, and it must be recorded whether or not this tick merges.
		// A dispatch id of 0 is adoption's case — the item has no row, and the
		// verdict is keyed by item so a later merge can find it.
		dispatch, hasDispatch := latest[item]
		var dispatchID int64
		if hasDispatch {
			dispatchID = dispatch.ID
		}
		captured, err := actions.CaptureFromForge(ctx, dispatchID, item, request.Number, request.HeadSHA)
		if err != nil {
			// An ACCEPT that names another sha ends the capture (#289 T2): the
			// skip line below already names the note and both shas through the
			// error's own text, and the audit surface gets one event per note
			// so a tick repeating every few minutes cannot flood it. The write
			// is best-effort: a ledger failure keeps the skip line and moves
			// on — a missed audit row never becomes a crashed tick or a merge,
			// and the next tick retries the write.
			var accepted *UnattributedAcceptError
			var rejected *UnattributedRejectError
			// The parse failure keeps today's behaviour: it is a malformed note
			// a reviewer typo causes, not a fact about this head, so it is
			// neither recorded nor deduped (design D1).
			alreadyNoted := false
			if errors.As(err, &accepted) {
				alreadyNoted = noteUnattributedVerdict(ctx, actions, item, keyFor(accepted.NoteID),
					fmt.Sprintf("%s sha=%s; head=%s", keyFor(accepted.NoteID), accepted.Written, accepted.Head))
			} else if errors.As(err, &rejected) {
				alreadyNoted = noteUnattributedVerdict(ctx, actions, item, keyFor(rejected.NoteID),
					fmt.Sprintf("%s sha=%s; head=%s", keyFor(rejected.NoteID), rejected.Written, rejected.Head))
			}
			// The line is silence only on a later tick over the same note —
			// already on the ledger (design D2); the audit row is the durable
			// signal. The tick that wrote the row prints it, and a ledger
			// failure keeps today's line: the flow never changes for a missed
			// audit row.
			if !alreadyNoted {
				report.Skipped = append(report.Skipped, fmt.Sprintf("#%d verdict: %v", item, err))
			}
			continue
		}
		if captured != nil {
			report.Verdicts = append(report.Verdicts, *captured)
		}
		var merged *MergeResult
		switch {
		case hasDispatch && request.Branch != "" && request.Branch != dispatch.Branch:
			// Two branches close one item: the dispatch is one and this change
			// request is the other. Which work survives is a human decision, so
			// refuse and name both, exactly as the adoption arm does (design D4).
			report.Skipped = append(report.Skipped, fmt.Sprintf(
				"#%d merge waiting: refusing merge: live dispatch on %s and change request #%d is on %s",
				item, dispatch.Branch, request.Number, request.Branch))
			continue
		case hasDispatch:
			merged, err = actions.Merge(ctx, dispatch.ID, item)
		default:
			// No row: adopt the change request when it is the one the app would
			// have produced (design D1). The guards are in MergeAdopted, so a
			// refusal here names the missing fact rather than the missing row.
			merged, err = actions.MergeAdopted(ctx, in.Project.ID, item, request)
		}
		if err != nil {
			// A forge write refused for permission already raised the operator
			// question (#260): the journal line names the permission instead of
			// an anonymous error, and the refusal is not counted as a retry —
			// the dispatch stays live, and the next tick retries by itself once
			// the token is fixed. The #252 blocked-merge page would double-page
			// the same refusal, so it is not consulted here.
			var refused *PermissionRefused
			if errors.As(err, &refused) {
				notePermission(in, &report, refused)
				continue
			}
			// An accepted change request that cannot merge never recovers on its
			// own: the head must be rebased or the item deferred, so the skip is
			// the repair's trigger, not its resting state (#251). The raise is
			// once per (item, head); the skip line still carries the reason.
			var notMergeable *NotMergeableError
			if errors.As(err, &notMergeable) {
				if repair, repairErr := actions.RaiseMergeRepair(ctx, in.Project.Key, notMergeable, in.WorktreeRoot); repairErr != nil {
					report.Skipped = append(report.Skipped, fmt.Sprintf("#%d merge repair: %v", item, repairErr))
				} else if !repair.NoOp {
					report.Decisions = append(report.Decisions, repair.QItemID)
					if in.OnDecision != nil {
						in.OnDecision(DecisionRaised{QItemID: repair.QItemID, Item: item, Text: fmt.Sprintf(
							"accepted change request #%d cannot merge: rework or defer #%d", notMergeable.Number, item)})
					}
				}
			}
			// A refusal the next tick cannot fix is an operator obligation, not
			// idleness: report it as a fact for the notifier to page (#252).
			// The skip line stays the journal's own record of the same refusal.
			if blocked, ok := blockedMerge(item, request, err); ok {
				report.BlockedMerges = append(report.BlockedMerges, blocked)
			}
			report.Skipped = append(report.Skipped, fmt.Sprintf("#%d merge waiting: %v", item, err))
			continue
		}
		report.Merges = append(report.Merges, merged)
		// A post-merge write (the item close) can also be refused for
		// permission: the merge landed, the result rides the refusal, and the
		// same note path tells the phone (#260).
		notePermission(in, &report, merged.Permission)
		// The rollup runs after the merge and against the same snapshot the plan
		// used: no extra forge read. `rolled` is shared across the loop so two
		// merged siblings produce one close.
		actions.rollupAfterMerge(ctx, in, &report, rolled, item, merged)
		// The merge landed; a gap means part of its audit trail did not. It is
		// reported on the same channel as a skip so the tick log names it, but it
		// is not a skip: `merged` above is already recorded.
		for _, why := range merged.AuditGaps {
			report.Skipped = append(report.Skipped, fmt.Sprintf("#%d merged, audit gap: %s", item, why))
		}
	}
	// The epic sweep runs after the merge loop and on every applied tick, merge or
	// not: an epic whose last child closed by hand, as a duplicate, or in an
	// earlier tick whose close never landed has no merge to hang its rollup on.
	// It shares `rolled` with the merge loop, so an epic the rollup just closed is
	// not closed twice.
	actions.sweepEpics(ctx, in, &report, rolled)

	// The plan is built before this tick runs, so its "no verdict" judgment is a
	// prediction about a state the tick itself changes: the merge loop above
	// captures verdicts from the same notes the plan read. Paging the owner for a
	// decision they answered before the row reached them is the failure this guard
	// exists for (#142).
	capturedItems := make(map[int]bool, len(report.Verdicts))
	for _, captured := range report.Verdicts {
		capturedItems[captured.Item] = true
	}

	// The decision's audience routes the raise (#265): an operator question —
	// a missing verdict, which only a human can answer — goes to human@kernel,
	// and the reviewer path keeps its re-review row. OnDecision fires for both,
	// so the alert path is unchanged.
	for _, intent := range in.Plan.Intents {
		if intent.Kind != decide.IntentRaiseDecision {
			continue
		}
		if capturedItems[intent.Item] {
			report.Skipped = append(report.Skipped, fmt.Sprintf(
				"#%d decision skipped: this tick captured the verdict the plan called missing", intent.Item))
			continue
		}
		var result *RaiseResult
		var err error
		if intent.Audience == decide.AudienceOperator {
			// Operator mode is a destination switch, not a second intent
			// (verdict-relay, design D4): the configured seat takes the
			// questions when the project's relay mode is operator, and
			// human@kernel keeps them otherwise.
			result, err = actions.RaiseOperatorQuestion(ctx, in.Project, intent.Item, intent.Text,
				intent.Event.Kind, intent.HeadSHA, in.WorktreeRoot, operatorDestination(in.RelayMode, in.RelaySeat))
		} else {
			result, err = actions.RaiseDecision(ctx, in.Project, intent.Item, intent.Text,
				intent.Event.Kind, intent.ChangeRequest, intent.HeadSHA, in.WorktreeRoot)
		}
		if err != nil {
			report.Skipped = append(report.Skipped, fmt.Sprintf("#%d decision: %v", intent.Item, err))
			continue
		}
		if !result.NoOp {
			report.Decisions = append(report.Decisions, result.QItemID)
			// The row exists before the phone is told about it, so a message
			// never points at something the store cannot show.
			if in.OnDecision != nil {
				in.OnDecision(DecisionRaised{QItemID: result.QItemID, Item: intent.Item, Text: intent.Text})
			}
		}
	}

	// The relay (verdict-relay, design D2/D5): the plan named a finished
	// review without a verdict. This has its own case — never a decision row —
	// so the two can never share a destination by accident: the relay's
	// destination is the configured seat in both modes, while an operator
	// question's is operatorDestination's. The raise reads the review's notes,
	// writes the brief and creates one row; it writes nothing on the forge and
	// merges nothing — the note it produces is judged by the merge gate's own
	// four facts on a later tick.
	for _, intent := range in.Plan.Intents {
		if intent.Kind != decide.IntentRelayVerdict {
			continue
		}
		if capturedItems[intent.Item] {
			report.Skipped = append(report.Skipped, fmt.Sprintf(
				"#%d relay skipped: this tick captured the verdict the plan called missing", intent.Item))
			continue
		}
		if strings.TrimSpace(in.RelaySeat) == "" {
			report.Skipped = append(report.Skipped, fmt.Sprintf(
				"#%d relay skipped: the project has no verdict_relay.seat", intent.Item))
			continue
		}
		result, err := actions.RaiseVerdictRelay(ctx, in.Project, intent.Item, intent.ChangeRequest,
			intent.Text, intent.Event.Kind, intent.HeadSHA, in.RelaySeat, in.WorktreeRoot)
		if err != nil {
			report.Skipped = append(report.Skipped, fmt.Sprintf("#%d relay: %v", intent.Item, err))
			continue
		}
		if !result.NoOp {
			report.Decisions = append(report.Decisions, result.QItemID)
			if in.OnDecision != nil {
				in.OnDecision(DecisionRaised{QItemID: result.QItemID, Item: intent.Item, Text: intent.Text})
			}
		}
	}

	// A live dispatch that produced no change request is a stall, not progress
	// (#261): decide named it, and this raises the operator's question — once
	// per (item, attempt) on the ledger, the same raise family #260 uses. The
	// seat is not killed and nothing is re-dispatched: two builders on one
	// branch close one item from two heads, and the merge gate refuses both.
	// The facts come from the dispatch rows this tick already read, so a
	// dispatch that ended between the plan and here raises nothing, and a
	// change request that appeared raises nothing — that is the merge gate's
	// case, and the plan's judgment is stale.
	for _, intent := range in.Plan.Intents {
		if intent.Kind != decide.IntentSilentDispatch {
			continue
		}
		item := itemFor(in.Items, intent.Item)
		if item == nil || !strings.EqualFold(item.State, forge.StateOpen) {
			report.Skipped = append(report.Skipped,
				fmt.Sprintf("#%d silent dispatch skipped: the item is not open", intent.Item))
			continue
		}
		if changeRequestFor(in.CRs, intent.Item) != nil {
			report.Skipped = append(report.Skipped,
				fmt.Sprintf("#%d silent dispatch skipped: the item now has a change request", intent.Item))
			continue
		}
		dispatch, live := latest[intent.Item]
		if !live || dispatch.EndedAt != nil {
			report.Skipped = append(report.Skipped,
				fmt.Sprintf("#%d silent dispatch skipped: the dispatch has already ended", intent.Item))
			continue
		}
		age := dispatchAge(dispatch, in.Now)
		if age <= 0 {
			report.Skipped = append(report.Skipped,
				fmt.Sprintf("#%d silent dispatch skipped: the dispatch's age cannot be read", intent.Item))
			continue
		}
		raised, err := actions.RaiseSilentDispatch(ctx, SilentDispatchInput{
			Item: intent.Item, Title: item.Title, Seat: dispatch.Seat, Branch: dispatch.Branch, Worktree: dispatch.Worktree,
			Attempt: dispatch.Attempt, Age: age, Repo: in.Project.ForgeProject, ProjectKey: in.Project.Key,
		})
		if err != nil {
			report.Skipped = append(report.Skipped, fmt.Sprintf("#%d silent dispatch: %v", intent.Item, err))
			continue
		}
		if raised.NoOp {
			continue
		}
		report.Skipped = append(report.Skipped, raised.Journal)
		report.Decisions = append(report.Decisions, raised.QItemID)
		// The row exists before the phone is told about it, like every raise.
		if in.OnDecision != nil {
			in.OnDecision(DecisionRaised{QItemID: raised.QItemID, Item: intent.Item, Text: raised.Question})
		}
	}

	// A rig whose launch failed this tick has nothing to claim its work: name the
	// failure once, before the dispatch loop, whatever the plan carried. The skip
	// is the one line the operator sees, and the issue is retried next tick.
	for _, rigName := range projectRigs(in.Project) {
		if failure, ok := in.LifecycleFailed[rigName]; ok {
			report.Skipped = append(report.Skipped, fmt.Sprintf("rig %s up: %s", rigName, failure))
		}
	}
	for _, intent := range in.Plan.Intents {
		if intent.Kind != decide.IntentDispatch {
			continue
		}
		item := itemFor(in.Items, intent.Item)
		if item == nil {
			report.Skipped = append(report.Skipped, fmt.Sprintf("#%d is not in the mirror", intent.Item))
			continue
		}
		if rigName := rigFromSeat(intent.Seat); rigName != "" {
			if _, ok := in.LifecycleFailed[rigName]; ok {
				continue
			}
		}
		if liveRowNames(in.Rows, intent.Item) {
			report.Skipped = append(report.Skipped, fmt.Sprintf("#%d has a live row", intent.Item))
			continue
		}
		// A change request on this item is already work in flight. decide's
		// branch_open_cr only sees a CR whose branch name encodes the number, so a
		// CR on a differently named branch reaches here and must not be dispatched
		// twice; it also counts in Result.Ready, which can start the rig.
		if changeRequestFor(in.CRs, intent.Item) != nil {
			report.Skipped = append(report.Skipped, fmt.Sprintf("#%d already has a change request", intent.Item))
			continue
		}
		result, err := actions.Dispatch(ctx, DispatchInput{
			ProjectID: in.Project.ID, ProjectKey: in.Project.Key, Item: *item,
			Seat: intent.Seat, WorktreeRoot: in.WorktreeRoot,
		})
		if err != nil {
			report.Skipped = append(report.Skipped, fmt.Sprintf("#%d dispatch: %v", intent.Item, err))
			continue
		}
		if result.NoOp {
			report.Skipped = append(report.Skipped, fmt.Sprintf("#%d already dispatched %s", intent.Item, result.QItemID))
			continue
		}
		report.Dispatches = append(report.Dispatches, *result)
	}
	return report, nil
}

// keyFor is a note's dedupe key: `note=<id>;` — the trailing `;` is the
// terminator, so note 12 can never match note 123.
func keyFor(noteID string) string {
	return fmt.Sprintf("note=%s;", noteID)
}

// noteUnattributedVerdict writes the once-per-note audit row for an
// unattributable verdict (epic #309's fixed vocabulary: kind
// verdict.unattributed, subject #<item>, detail "note=<id>; sha=<written>;
// head=<head>"). One helper, two callers — the ACCEPT and the REJECT arm — so
// a reader of the audit surface never learns a second vocabulary for one fact.
// It answers whether the note was already on the ledger before this tick —
// the one case the line goes quiet on: the tick that writes the row prints
// it, and a later tick over the same note is silence. A ledger failure writes
// nothing and answers false, so the line keeps printing — the best-effort
// rule: a missed audit row never becomes a crashed tick or a merge.
func noteUnattributedVerdict(ctx context.Context, actions *Actions, item int, key, detail string) bool {
	subject := fmt.Sprintf("#%d", item)
	noted, err := actions.Store.HasEvent(ctx, "verdict.unattributed", subject, key)
	if err != nil || noted {
		return noted
	}
	_, _ = actions.event(ctx, "act", "verdict.unattributed", subject, detail)
	return false
}

func refusalReason(err error) string {
	var refusal *Refusal
	if errors.As(err, &refusal) {
		return refusal.Reason
	}
	return err.Error()
}

func itemFor(items []forge.Item, number int) *forge.Item {
	for index := range items {
		if items[index].Number == number {
			return &items[index]
		}
	}
	return nil
}

// projectRigs is every rig a project is bound to, deduplicated in order.
func projectRigs(project store.ProjectRow) []string {
	rigs := project.Rigs
	if len(rigs) == 0 && project.Rig != "" {
		rigs = []string{project.Rig}
	}
	seen := make(map[string]bool, len(rigs))
	out := make([]string, 0, len(rigs))
	for _, name := range rigs {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func changeRequestFor(requests []forge.ChangeRequest, item int) *forge.ChangeRequest {
	for index := range requests {
		// A closed or merged change request can never merge again. It must not block
		// a re-dispatch: a rejected attempt leaves its PR closed, and the item is
		// free work for the next builder.
		if requests[index].Merged || strings.EqualFold(requests[index].State, "closed") {
			continue
		}
		if requests[index].ClosesItem == item || forge.ParseCloses(requests[index].Body) == item {
			return &requests[index]
		}
	}
	return nil
}

func liveRowNames(rows []rig.QueueRow, item int) bool {
	want := fmt.Sprintf("%d", item)
	for _, row := range rows {
		state := strings.ToLower(row.PickupState)
		if state == "" {
			state = strings.ToLower(row.State)
		}
		switch state {
		case "terminal", "closed", "done", "cancelled", "canceled", "denied", "failed",
			"handed-off", "handed_off", "handedoff":
			continue
		}
		head := row.Body
		if len(head) > 400 {
			head = head[:400]
		}
		head += " " + row.Summary
		for _, match := range tickIssueNumber.FindAllStringSubmatch(head, -1) {
			if match[1] == want {
				return true
			}
		}
	}
	return false
}
