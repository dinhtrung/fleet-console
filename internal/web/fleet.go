package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/mirror"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
	"github.com/gofiber/fiber/v3"
)

// Fleet is the read model behind the product screens.
type Fleet struct {
	Store *store.Store
	Rig   *rig.Adapter
	Hub   *Hub
	Now   func() time.Time
	// Tick is the mirror's poll cadence. The project header judges how old a
	// mirror read is against it: quiet past two ticks, stalled past five (#394).
	// Zero means the config default.
	Tick time.Duration
	// MaxWait is the longest the mirror's poll may legitimately sleep between
	// reads — the cadence's own cap (rig_lifecycle.idle_poll_max when the block
	// is written, tick otherwise). A quiet box writes a stamp once per window,
	// so the window, not the tick, is the age a healthy header must call fresh
	// (#408, the same derivation web.Readiness.staleThreshold uses).
	MaxWait time.Duration
	Compat  func(ctx context.Context) rig.Report
	// ForgeFor resolves a project's forge for the one write path the board
	// owns: a human approve/reject posts a note on the change request. Every
	// other write stays in the act loop.
	ForgeFor func(ctx context.Context, project store.ProjectRow) (forge.Forge, error)
	// Waker is the mirror loop's wake channel. A board verdict changes what the
	// next tick would read, so it asks for that tick now instead of waiting out
	// the quiet backoff. Nil means no loop runs in this process.
	Waker *mirror.Waker
}

type boardItem struct {
	Number int      `json:"number"`
	Title  string   `json:"title"`
	State  string   `json:"state"`
	Seat   string   `json:"seat,omitempty"`
	Age    string   `json:"age"`
	Reason string   `json:"reason"`
	URL    string   `json:"url,omitempty"`
	Labels []string `json:"labels"`
	// VerdictSHA and HeadSHA are the two commits a stale verdict names
	// (console-ui: "A stale verdict shows both shas"). They ride the row so the
	// board draws them where the state is, not only in the drawer's detail.
	VerdictSHA string `json:"verdict_sha,omitempty"`
	HeadSHA    string `json:"head_sha,omitempty"`
	// PlanReason is the last plan's recorded reason for leaving this item
	// undispatched, with its detail (#428's plan_reason; #429's why-column).
	// Empty means the last plan had nothing to say about this item, which is a
	// different fact from "no plan has run" — boardProject.Planned tells them
	// apart.
	PlanReason string `json:"plan_reason,omitempty"`
	// PlanAge is how old that record is, "" without a record.
	PlanAge string `json:"plan_age,omitempty"`
}

type boardCounts struct {
	Building int `json:"building"`
	Review   int `json:"review"`
	Waiting  int `json:"waiting"`
	Parked   int `json:"parked"`
	Blocked  int `json:"blocked"`
	Stale    int `json:"stale"`
}

// projectPaused treats a disabled project as paused: removing its config block
// disables it, which must read as paused on the board, not active.
func projectPaused(p store.ProjectRow) bool { return p.OnHold || !p.Enabled }

type boardProject struct {
	Key    string      `json:"key"`
	Name   string      `json:"name"`
	Paused bool        `json:"paused"`
	Status string      `json:"status"`           // ok | compat-blocked
	Reason string      `json:"reason,omitempty"` // the named compat refusal
	Counts boardCounts `json:"counts"`
	Items  []boardItem `json:"items"`
	// Questions are the operator questions this project's ledger still has
	// unanswered (#377). A waiting question is why a project can look idle and
	// still be stuck, so it is part of the project's own row.
	Questions []boardQuestion `json:"questions"`
	// ForgeRead and RigRead say how old each mirror read is (#394): the
	// difference between a stalled mirror and a forge with nothing to say.
	// Both are built server-side so the thresholds live in one place.
	ForgeRead mirrorRead `json:"forge_read"`
	RigRead   mirrorRead `json:"rig_read"`
	// Planned is true once any tick has run for this project. It is what makes
	// an empty plan_reason table readable: before the first tick nothing is
	// known, after one an empty table means the last plan skipped nothing
	// (#429). Without it the two cases would render as the same blank cell.
	Planned bool `json:"planned"`
}

// boardQuestion is one unanswered operator question, read from the ledger and
// joined with the queue row where the snapshot had it (#377).
type boardQuestion struct {
	Item          int    `json:"item"`
	ChangeRequest int    `json:"change_request"`
	NoteID        string `json:"note_id"`
	Head          string `json:"head"`
	Destination   string `json:"destination"`
	Waiting       string `json:"waiting"`
	QItemID       string `json:"qitem,omitempty"`
}

func (f *Fleet) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now().UTC()
}

func age(since string) string {
	if since == "" {
		return "—"
	}
	t, err := time.Parse(time.RFC3339Nano, since)
	if err != nil {
		if t, err = time.Parse(time.RFC3339, since); err != nil {
			return "—"
		}
	}
	return ageBetween(time.Now(), t)
}

func splitLabels(labels string) []string {
	fields := strings.FieldsFunc(labels, func(r rune) bool { return r == ',' || r == ' ' })
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

func hasLabel(labels []string, want string) bool {
	for _, l := range labels {
		if l == want {
			return true
		}
	}
	return false
}

func labelValue(labels []string, prefix string) string {
	for _, l := range labels {
		if strings.HasPrefix(l, prefix) {
			return strings.TrimPrefix(l, prefix)
		}
	}
	return ""
}

func parseInts(raw string) []int {
	var out []int
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

// forgeHeads maps an item to its open change request's head sha from the forge
// snapshot. It is how the board knows a verdict sha is stale.
func (f *Fleet) forgeHeads(ctx context.Context, projectID int64) (map[int]string, error) {
	raw, _, err := f.Store.GetSnapshot(ctx, projectID, "forge")
	if err != nil || len(raw) == 0 {
		return map[int]string{}, err
	}
	var snapshot struct {
		CRs []struct {
			ClosesItem int    `json:"closes_item"`
			HeadSHA    string `json:"head_sha"`
		} `json:"change_requests"`
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return map[int]string{}, nil
	}
	heads := make(map[int]string, len(snapshot.CRs))
	for _, cr := range snapshot.CRs {
		if cr.ClosesItem > 0 && cr.HeadSHA != "" {
			heads[cr.ClosesItem] = cr.HeadSHA
		}
	}
	return heads, nil
}

// forgeChange returns the newest open change request that closes this item, read
// from the mirror's forge snapshot. The item drawer used to take the pull request
// from a pr: label, and nothing writes that label on an item the act loop did not
// dispatch - so a hand-built or adopted pull request left the drawer saying "no
// change request" while the pull request was open, with no way to judge it.
func (f *Fleet) forgeChange(ctx context.Context, projectID int64, number int) *changeRequest {
	raw, _, err := f.Store.GetSnapshot(ctx, projectID, "forge")
	if err != nil || len(raw) == 0 {
		return nil
	}
	var snapshot struct {
		CRs []struct {
			Number     int    `json:"number"`
			ClosesItem int    `json:"closes_item"`
			Merged     bool   `json:"merged"`
			State      string `json:"state"`
			Checks     []struct {
				Name       string `json:"name"`
				Conclusion string `json:"conclusion"`
			} `json:"checks"`
		} `json:"change_requests"`
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return nil
	}
	var best *changeRequest
	for _, cr := range snapshot.CRs {
		// The mirror stores every change request the forge read returned, closed
		// ones included. Skip a closed request as well as a merged one: a closed
		// pull request is not something the operator can judge, and the verdict
		// POST refuses it with 409. openChangeRequest (verdict.go) is the twin
		// predicate and already skips StateClosed; the drawer must agree with it,
		// or the drawer offers a button the POST rejects.
		if cr.Merged || cr.State == string(forge.StateClosed) || cr.ClosesItem != number {
			continue
		}
		if best != nil && cr.Number <= best.Number {
			continue
		}
		judged := changeRequest{Number: cr.Number}
		for _, check := range cr.Checks {
			judged.Checks = append(judged.Checks, checkOut{Name: check.Name, Conclusion: check.Conclusion})
		}
		best = &judged
	}
	return best
}

// Board assembles projects, counts and one reason per task from stored facts.
func (f *Fleet) Board(c fiber.Ctx) error {
	ctx := c.Context()
	// One instant for the whole response: every age on the board is measured from it, so a
	// project's read age can never look newer than the board's own sync time (#394).
	now := f.now()
	compat := rig.Report{OK: true, Status: rig.StatusOK}
	if f.Compat != nil {
		compat = f.Compat(ctx)
	}
	compatBlocked := compat.Blocked()
	projects, err := f.Store.Projects(ctx)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	items, err := f.Store.Items(ctx)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	byProject := map[int64][]store.ItemRow{}
	for _, item := range items {
		byProject[item.ProjectID] = append(byProject[item.ProjectID], item)
	}
	openNumbers := map[int64]map[int]string{}
	for _, p := range projects {
		openNumbers[p.ID] = map[int]string{}
		for _, item := range byProject[p.ID] {
			openNumbers[p.ID][item.Number] = item.State
		}
	}

	// The mirror's stamps decide the board's sync time and stale flag. A failed
	// forge read keeps the previous items and stamp; it never reads as an empty board.
	statuses := make(map[int64]store.MirrorStatus, len(projects))
	synced := time.Time{}
	stale := false
	forbidden := false
	for _, p := range projects {
		status, err := f.Store.MirrorStatus(ctx, p.ID)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		statuses[p.ID] = status
		if status.LastForgeAt != nil && status.LastForgeAt.After(synced) {
			synced = *status.LastForgeAt
		}
		stale = stale || status.ForgeStale
		forbidden = forbidden || status.ForgeForbidden
	}

	result := make([]boardProject, 0, len(projects))
	for _, p := range projects {
		// The last plan's reasons (#428), one read per project: a recorded reason
		// is the item's why-column when it exists (#429). A read failure is not a
		// reason to fail the board — the state-derived reason still renders.
		planReasons := map[int]store.PlanReason{}
		if reasons, err := f.Store.PlanReasons(ctx, p.ID); err == nil {
			for _, reason := range reasons {
				planReasons[reason.ItemNumber] = reason
			}
		}
		// One query per project, not per item: the newest attempt per item and the
		// newest verdict per item are folded here.
		allDispatches, err := f.Store.DispatchesForProject(ctx, p.ID)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		dispatches := make(map[int]store.DispatchRow, len(allDispatches))
		live := 0
		for _, dispatch := range allDispatches {
			// A reconciled attempt is over; the item reads as queued until a
			// later tick dispatches a new one.
			if dispatch.EndedAt != nil &&
				(dispatch.EndReason == "superseded" || dispatch.EndReason == "abandoned") {
				continue
			}
			if _, exists := dispatches[dispatch.ItemNumber]; exists {
				continue
			}
			dispatches[dispatch.ItemNumber] = dispatch
			if dispatch.EndedAt == nil {
				live++
			}
		}
		projectVerdicts, err := f.Store.VerdictsForProject(ctx, p.ID)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		verdictByItem := make(map[int]store.VerdictForItem, len(projectVerdicts))
		for _, verdict := range projectVerdicts {
			if _, exists := verdictByItem[verdict.Item]; !exists {
				verdictByItem[verdict.Item] = verdict
			}
		}
		heads, err := f.forgeHeads(ctx, p.ID)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		projectStatus := statuses[p.ID]
		bp := boardProject{
			Key: p.Key, Name: p.Name, Paused: projectPaused(p), Status: rig.StatusOK,
			Items: []boardItem{}, Questions: []boardQuestion{},
		}
		// How old each mirror read is belongs on the project's own header (#394).
		// The stamps are the only evidence a stalled mirror leaves behind.
		bp.ForgeRead = mirrorReadAt(now, projectStatus.LastForgeAt, f.Tick, f.MaxWait, "forge")
		bp.RigRead = mirrorReadAt(now, projectStatus.LastRigAt, f.Tick, f.MaxWait, "rig")
		// A tick that wrote its status block is the evidence a plan has run
		// (#429), and a recorded reason is proof on its own — the write happens
		// inside a plan. Either one makes an empty reason table mean "nothing
		// was skipped" rather than "nothing has been planned yet".
		bp.Planned = projectStatus.LastTickAt != nil || len(planReasons) > 0
		// A question waiting an hour must not look like a project with nothing
		// to do (#377): the ledger's unanswered verdict questions ride the same
		// read the board already makes, and a row the queue says is finished
		// has already dropped out.
		questions, err := f.Store.UnansweredQuestions(ctx, p.ID, p.Key)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		for _, question := range questions {
			bp.Questions = append(bp.Questions, boardQuestion{
				Item: question.Item, ChangeRequest: question.ChangeRequest, NoteID: question.NoteID,
				Head: question.Head, Destination: question.Destination, Waiting: age(question.At),
				QItemID: question.QItemID,
			})
		}
		switch {
		case compatBlocked:
			bp.Status = "compat-blocked"
			bp.Reason = compat.Error
		case projectStatus.ForgeForbidden:
			// The token lacks a scope or the branch is protected; retrying cannot
			// succeed. The reason names the operation and the forge's message.
			bp.Status = "forbidden"
			bp.Reason = projectStatus.ForgeError
		}
		for _, item := range byProject[p.ID] {
			if item.State != "open" {
				continue
			}
			labels := splitLabels(item.Labels)
			row := boardItem{
				Number: item.Number, Title: item.Title, State: "queued", Reason: "queued",
				Age: age(item.UpdatedAt), URL: item.URL, Labels: labels,
			}
			d := dispatches[item.Number]
			switch {
			case projectPaused(p):
				row.State, row.Reason = "paused", "project paused"
				if !p.Enabled {
					row.Reason = "project removed from config"
				}
			case item.ParentNumber != nil && openNumbers[p.ID][*item.ParentNumber] != "open":
				row.State, row.Reason = "blocked", fmt.Sprintf("parent #%d is not open", *item.ParentNumber)
			}
			if row.State == "queued" {
				for _, blocked := range parseInts(item.BlockedByJSON) {
					if openNumbers[p.ID][blocked] == "open" {
						row.State, row.Reason = "waiting", fmt.Sprintf("waits on #%d", blocked)
						break
					}
				}
			}
			if row.State == "queued" && d.ItemNumber != 0 {
				row.Seat = d.Seat
				switch {
				case heads[d.ItemNumber] != "" || hasLabel(labels, "cr:open"):
					// An open change request is the review phase even while the
					// dispatch row is still open; a stale verdict is visible here.
					verdict, ok := verdictByItem[d.ItemNumber]
					head := heads[d.ItemNumber]
					switch {
					case !ok:
						row.State, row.Reason = "parked", "parked: change request open, no verdict"
					case head != "" && !strings.EqualFold(verdict.SHA, head):
						// D2: the verdict names a sha that is no longer the head.
						// A stale row is not parked: someone is needed — the
						// reviewer must look at the new head — and the spec forbids
						// the parked draw. The row carries both shas so the board
						// draws them beside the state.
						row.State = "stale"
						row.Reason = staleVerdictReason(verdict.Decision, verdict.SHA, head)
						row.VerdictSHA, row.HeadSHA = verdict.SHA, head
					default:
						row.State, row.Reason = "review", "verdict "+verdict.Decision+", waits for merge"
					}
				case d.EndedAt == nil:
					row.State, row.Reason = "building", ""
				case d.EndReason == "merged":
					row.State, row.Reason = "review", "merged"
				case d.EndReason != "":
					row.State, row.Reason = "waiting", "dispatch "+d.EndReason
				}
				row.Age = age(d.CreatedAt)
			}
			if row.State == "queued" && live >= p.ConcurrencyCap && p.ConcurrencyCap > 0 {
				row.State, row.Reason = "waiting", "no free builder"
			}
			switch {
			case compatBlocked:
				// The act loop is off; every project says why instead of looking
				// like an empty or idle queue.
				row.State, row.Reason = "blocked", "compat-blocked"
			case projectStatus.ForgeForbidden:
				row.State, row.Reason = "blocked", projectStatus.ForgeError
			}
			switch row.State {
			case "building":
				bp.Counts.Building++
			case "review":
				bp.Counts.Review++
			case "parked":
				bp.Counts.Parked++
			case "stale":
				// A stale verdict is a review waiting for the reviewer, not a
				// row nobody needs: it gets its own count so the header cannot
				// report it as parked (console-ui).
				bp.Counts.Stale++
			case "waiting":
				bp.Counts.Waiting++
			case "blocked", "paused":
				bp.Counts.Blocked++
			}
			if reason, ok := planReasons[row.Number]; ok {
				row.PlanReason = reason.Reason
				if reason.Detail != "" {
					row.PlanReason += " — " + reason.Detail
				}
				if !reason.TickAt.IsZero() {
					row.PlanAge = ageBetween(now, reason.TickAt)
				}
			}
			bp.Items = append(bp.Items, row)
		}
		sort.Slice(bp.Items, func(i, j int) bool { return bp.Items[i].Number < bp.Items[j].Number })
		result = append(result, bp)
	}
	syncedAt := now.UTC().Format(time.RFC3339)
	if !synced.IsZero() {
		syncedAt = synced.UTC().Format(time.RFC3339)
	}
	return c.JSON(fiber.Map{
		"projects": result, "synced_at": syncedAt, "stale": stale,
		"compat_blocked": compatBlocked, "compat_reason": compat.Error,
		"forge_forbidden": forbidden,
	})
}

// staleVerdictReason is the one text a stale verdict reads as (console-ui:
// "A stale verdict shows both shas"). The board row and the drawer both write
// it, so the two surfaces cannot name the commits differently.
func staleVerdictReason(decision, sha, head string) string {
	return fmt.Sprintf("verdict stale: %s %s is on %s, head is %s",
		strings.ToUpper(decision), sha, sha, head)
}

type checkOut struct {
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
}

type itemDetail struct {
	Project      boardProject       `json:"project"`
	Item         boardItem          `json:"item"`
	Dispatch     *store.DispatchRow `json:"dispatch,omitempty"`
	Brief        string             `json:"brief,omitempty"`
	Change       *changeRequest     `json:"change_request,omitempty"`
	Verdicts     []store.VerdictRow `json:"verdicts"`
	VerdictStale bool               `json:"verdict_stale,omitempty"`
	VerdictSHA   string             `json:"verdict_sha,omitempty"`
	HeadSHA      string             `json:"head_sha,omitempty"`
	Events       []store.EventRow   `json:"events"`
}

type changeRequest struct {
	Number int        `json:"number"`
	Checks []checkOut `json:"checks"`
}

// Item returns the task drawer's facts: brief, branch, worktree, CR, checks,
// verdict and events.
func (f *Fleet) Item(c fiber.Ctx) error {
	number, err := strconv.Atoi(c.Params("number"))
	if err != nil || number <= 0 {
		return fiber.NewError(fiber.StatusBadRequest, "item number is required")
	}
	ctx := c.Context()
	projects, err := f.Store.Projects(ctx)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	projectKey := c.Query("project")
	for _, p := range projects {
		if projectKey != "" && p.Key != projectKey {
			continue
		}
		item, err := f.Store.Item(ctx, p.ID, number)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		if item == nil {
			continue
		}
		labels := splitLabels(item.Labels)
		detail := itemDetail{
			Project: boardProject{Key: p.Key, Name: p.Name, Paused: projectPaused(p), Status: rig.StatusOK},
			Item: boardItem{
				Number: item.Number, Title: item.Title, State: item.State,
				Age: age(item.UpdatedAt), URL: item.URL, Labels: labels,
			},
		}
		// The drawer's project header carries the same two read ages as the board,
		// so the item surface cannot disagree with the project's own row (#394).
		status, err := f.Store.MirrorStatus(ctx, p.ID)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		now := f.now()
		detail.Project.ForgeRead = mirrorReadAt(now, status.LastForgeAt, f.Tick, f.MaxWait, "forge")
		detail.Project.RigRead = mirrorReadAt(now, status.LastRigAt, f.Tick, f.MaxWait, "rig")
		dispatches, err := f.Store.Dispatches(ctx, p.ID, number)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		// The snapshot is the authority: every mirror tick rewrites it, while the
		// pr: label is a write the act loop only makes after it dispatched the item
		// itself - so an adopted pull request has no label and used to read as "no
		// change request" with nothing to judge.
		if judged := f.forgeChange(ctx, p.ID, number); judged != nil {
			detail.Change = judged
		} else if pr := labelValue(labels, "pr:"); pr != "" {
			if n, err := strconv.Atoi(pr); err == nil {
				cr := changeRequest{Number: n}
				for _, l := range labels {
					if strings.HasPrefix(l, "check:") {
						parts := strings.SplitN(strings.TrimPrefix(l, "check:"), "=", 2)
						if len(parts) == 2 {
							cr.Checks = append(cr.Checks, checkOut{Name: parts[0], Conclusion: parts[1]})
						}
					}
				}
				detail.Change = &cr
			}
		}
		if len(dispatches) > 0 {
			d := dispatches[0]
			detail.Dispatch = &d
			detail.Item.Seat = d.Seat
			if d.BriefPath != "" {
				if data, err := readCapped(d.BriefPath, 8192); err == nil {
					detail.Brief = data
				}
			}
			verdicts, err := f.Store.Verdicts(ctx, d.ID)
			if err != nil {
				return fiber.NewError(fiber.StatusInternalServerError, err.Error())
			}
			detail.Verdicts = verdicts
			if len(verdicts) > 0 {
				heads, err := f.forgeHeads(ctx, p.ID)
				if err != nil {
					return fiber.NewError(fiber.StatusInternalServerError, err.Error())
				}
				if head := heads[number]; head != "" && !strings.EqualFold(verdicts[0].SHA, head) {
					detail.VerdictStale = true
					detail.VerdictSHA = verdicts[0].SHA
					detail.HeadSHA = head
					// The drawer's row carries the same state and shas the
					// board's row does: the spec names both surfaces.
					detail.Item.State = "stale"
					detail.Item.Reason = staleVerdictReason(verdicts[0].Decision, verdicts[0].SHA, head)
					detail.Item.VerdictSHA, detail.Item.HeadSHA = verdicts[0].SHA, head
				}
			}
		}
		events, err := f.Store.EventsForItem(ctx, number, 20)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		detail.Events = events
		return c.JSON(detail)
	}
	return fiber.NewError(fiber.StatusNotFound, "item not found")
}

func readCapped(path string, max int) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(data) > max {
		data = append(data[:max], []byte("\n… truncated")...)
	}
	return string(data), nil
}

type decisionOut struct {
	ID          string              `json:"id"`
	Summary     string              `json:"summary"`
	Body        string              `json:"body"`
	Destination string              `json:"destination"`
	State       string              `json:"state"`
	CreatedAt   string              `json:"created_at"`
	DeadlineAt  string              `json:"deadline_at,omitempty"`
	Tier        string              `json:"tier,omitempty"`
	EvidenceRef string              `json:"evidence_ref,omitempty"`
	Questions   []rig.HumanQuestion `json:"questions,omitempty"`
}

// Decisions reads obligations waiting on a person from the rig, oldest first.
func (f *Fleet) Decisions(c fiber.Ctx) error {
	if f.Rig == nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "rig adapter is not configured")
	}
	rows, err := f.Rig.QueueList(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, err.Error())
	}
	out := make([]decisionOut, 0)
	for _, row := range rows {
		// A cheap pre-filter only: `rig queue list` may omit blockedOn, so a
		// blocked row's fate is decided by QueueShow below. The authoritative
		// check is rig.WaitsOnHuman on the resolved item — the same rule the
		// quiet predicate asks, so served and counted are one answer (#143).
		if row.State != "pending" && row.State != "blocked" {
			continue
		}
		if row.State == "pending" && !rig.HumanDecision(row.Destination) {
			continue
		}
		item, err := f.Rig.QueueShow(c.Context(), row.ID)
		if err != nil {
			return fiber.NewError(fiber.StatusBadGateway, err.Error())
		}
		if !rig.WaitsOnHuman(item.State, item.Destination, item.BlockedOn) {
			continue
		}
		d := decisionOut{
			ID: item.ID, Summary: item.Summary, Body: item.Body, Destination: item.Destination,
			State: item.State, CreatedAt: item.CreatedAt, DeadlineAt: item.DeadlineAt,
			Tier: item.Tier, EvidenceRef: item.EvidenceRef, Questions: item.HumanQuestions,
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return c.JSON(fiber.Map{"decisions": out})
}

// AnswerDecision records the answer with the rig and one audit event.
//
// Two row shapes carry a decision, and the verb each one accepts differs
// (#253): `queue resolve` requires a leg-1 parked row (state blocked), while a
// pending row addressed to a human — the shape act's raiser creates — accepts
// only an update that closes it. The pending fallback closes the row the way
// the operator's own workaround did, so the board and the queue stay in step
// either way.
func (f *Fleet) AnswerDecision(c fiber.Ctx) error {
	if f.Rig == nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "rig adapter is not configured")
	}
	id := c.Params("id")
	var body struct {
		Decision string `json:"decision"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "body must be JSON")
	}
	decision := strings.TrimSpace(body.Decision)
	if id == "" || decision == "" {
		return fiber.NewError(fiber.StatusBadRequest, "id and decision are required")
	}
	row, err := f.Rig.QueueShow(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, err.Error())
	}
	var item *rig.QueueItem
	switch state := strings.ToLower(row.State); {
	case state == "blocked":
		item, err = f.Rig.QueueResolve(c.Context(), id, decision)
	case state == "pending" && rig.HumanDecision(row.Destination):
		// A pending human row cannot resolve: the verb demands a parked row.
		// Close it as answered, carrying the decision in the note.
		item, err = f.Rig.QueueUpdate(c.Context(), id, rig.UpdateRequest{
			State: "done", ClosureReason: "answered",
			Note: "decision: " + decision + " (answered from the board)",
		})
	default:
		return fiber.NewError(fiber.StatusConflict,
			fmt.Sprintf("row %s is %s and addressed to %s; the board can answer a blocked row or a pending row addressed to a human", id, row.State, row.Destination))
	}
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, err.Error())
	}
	eventID, err := f.Store.AppendEvent(c.Context(), store.Event{
		Actor: "board", Kind: "decision.answer", Subject: id, Detail: decision,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	f.Hub.Publish("decision.answer", fiber.Map{"id": id, "state": item.State, "event_id": eventID})
	return c.JSON(fiber.Map{"id": id, "state": item.State, "event_id": eventID})
}

type seatOut struct {
	Session      string `json:"session"`
	Rig          string `json:"rig"`
	LogicalID    string `json:"logical_id"`
	State        string `json:"state"`
	Runtime      string `json:"runtime"`
	Model        string `json:"model"`
	Permission   string `json:"permission"`
	LastActivity string `json:"last_activity"`
}

// Seats reads the live topology.
func (f *Fleet) Seats(c fiber.Ctx) error {
	if f.Rig == nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "rig adapter is not configured")
	}
	seats, err := f.Rig.Seats(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, err.Error())
	}
	out := make([]seatOut, 0, len(seats))
	for _, seat := range seats {
		out = append(out, seatOut{
			Session: seat.Session, Rig: seat.Rig, LogicalID: seat.LogicalID, State: seat.Lifecycle,
			Runtime: seat.Runtime, Model: seat.Model, Permission: seat.Permission,
			LastActivity: seat.LastActivity,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Session < out[j].Session })
	projects, err := f.Store.Projects(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	synced := time.Time{}
	stale := false
	for _, project := range projects {
		status, err := f.Store.MirrorStatus(c.Context(), project.ID)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		if status.LastRigAt != nil && status.LastRigAt.After(synced) {
			synced = *status.LastRigAt
		}
		stale = stale || status.RigStale
	}
	syncedAt := f.now().UTC().Format(time.RFC3339)
	if !synced.IsZero() {
		syncedAt = synced.UTC().Format(time.RFC3339)
	}
	return c.JSON(fiber.Map{"seats": out, "synced_at": syncedAt, "stale": stale})
}

// SeatTail reads a read-only pane tail.
func (f *Fleet) SeatTail(c fiber.Ctx) error {
	if f.Rig == nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "rig adapter is not configured")
	}
	session, err := url.PathUnescape(c.Params("session"))
	if err != nil {
		session = c.Params("session")
	}
	lines := 20
	if raw := c.Query("lines"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 200 {
			lines = n
		}
	}
	tail, err := f.Rig.Capture(c.Context(), session, lines)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, err.Error())
	}
	return c.JSON(fiber.Map{"session": session, "lines": lines, "tail": tail})
}

// allowedEventFilters is the closed set of /api/events query parameters. A name
// outside it is refused, never ignored: a silently dropped filter answers
// "nothing matched" for a question the operator did ask.
var allowedEventFilters = map[string]bool{
	"actor": true, "kind": true, "subject": true,
	"since": true, "until": true, "limit": true, "before": true,
}

// Events returns one page of the audit log, newest first.
func (f *Fleet) Events(c fiber.Ctx) error {
	// Every parameter is validated before the store is touched, so a refused
	// request costs no query.
	for name := range c.Queries() {
		if !allowedEventFilters[name] {
			return fiber.NewError(fiber.StatusBadRequest, "unsupported filter: "+name)
		}
	}

	filter := store.EventFilter{
		Actor:         c.Query("actor"),
		Kind:          c.Query("kind"),
		SubjectPrefix: c.Query("subject"),
		Limit:         50,
	}
	// limit keeps its existing clamp: an unusable value falls back to the default
	// rather than refusing the read the operator is already looking at.
	if raw := c.Query("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 500 {
			filter.Limit = n
		}
	}
	if raw := c.Query("before"); raw != "" {
		before, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || before <= 0 {
			return fiber.NewError(fiber.StatusBadRequest, "unsupported filter: before")
		}
		filter.Before = before
	}
	for _, name := range []string{"since", "until"} {
		raw := c.Query(name)
		if raw == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "unsupported filter: "+name)
		}
		// Normalized to UTC RFC3339Nano: the store compares the column with
		// julianday, and one spelling of the bound keeps that comparison exact.
		if name == "since" {
			filter.Since = at.UTC().Format(time.RFC3339Nano)
		} else {
			filter.Until = at.UTC().Format(time.RFC3339Nano)
		}
	}

	page, err := f.Store.Events(c.Context(), filter)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	return c.JSON(page)
}

// Event returns one audit row by id, with its exit code and stdout hash.
func (f *Fleet) Event(c fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || id <= 0 {
		return fiber.NewError(fiber.StatusNotFound, "event not found")
	}
	event, err := f.Store.EventByID(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	if event == nil {
		return fiber.NewError(fiber.StatusNotFound, "event not found")
	}
	return c.JSON(event)
}
