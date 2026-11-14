// Package decide holds the dispatcher's rules as pure functions over a snapshot.
//
// The rules are a port of ~/bin/fleet-autodispatch.py, which is the proven contract:
// same order, same one-way blocking, same idempotency, same cap. Nothing here reads a
// forge, the rig or git; the mirror builds a Snapshot and this package answers what the
// tick would do, so the same snapshot always produces the same intents.
package decide

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Forge selects the one-directional blocking rule. The decide loop never mixes them.
type Forge string

const (
	ForgeGitHub Forge = "github"
	ForgeGitLab Forge = "gitlab"
)

// ItemKind is the mirror's normalized work-item type.
type ItemKind string

const (
	KindEpic ItemKind = "epic"
	KindTask ItemKind = "task"
)

// State is the forge's item state.
const (
	StateOpen   = "open"
	StateClosed = "closed"
)

// Project is one enabled project's tick scope.
type Project struct {
	Key            string   `json:"key"`
	Forge          Forge    `json:"forge"`
	Enabled        bool     `json:"enabled"`
	OnHold         bool     `json:"onhold"`
	ConcurrencyCap int      `json:"concurrency_cap"` // 0 = no cap beyond free builders
	Rigs           []RigRef `json:"rigs,omitempty"`
	// RelayMode is the project's verdict_relay mode ("scribe" or "operator"),
	// resolved by the caller from the config. Empty means the project never
	// opted in: no relay intent is planned. Decide keeps a string, not a config
	// type, so this package stays free of config imports; the act side resolves
	// the destination seat from the config it already holds.
	RelayMode string `json:"relay_mode,omitempty"`
	// Reviewer is the reviewer member prefix the project configured (#384),
	// resolved by the caller from the config and defaulted to DefaultReviewer.
	// The destination act writes and the recognition this package reads both
	// come from ReviewerSeat/IsReviewerSeat, so they cannot drift.
	Reviewer string `json:"reviewer,omitempty"`
	// EpicParallel restores today's parallel siblings (#443) when true. The field
	// is the opt-OUT so its zero value keeps the rule on: a caller that forgets
	// to thread the config serializes epics rather than silently disabling the
	// rule.
	EpicParallel bool `json:"epic_parallel"`
}

// DefaultReviewer is the reviewer member prefix a project uses when its config
// names none (#384) — the name every project had before the field existed.
const DefaultReviewer = "dev-review"

// ReviewerSeat is the session a project's review rows are addressed to: the
// configured member prefix with the rig appended, or the prefix alone when the
// rig is unknown.
func ReviewerSeat(prefix, rig string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = DefaultReviewer
	}
	if rig == "" {
		return prefix
	}
	return prefix + "@" + rig
}

// IsReviewerSeat reports whether a queue row's destination belongs to the
// project's reviewer seat. One rule, so the destination act writes and the
// recognition decide reads can never disagree.
func IsReviewerSeat(prefix, destination string) bool {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = DefaultReviewer
	}
	destination = strings.ToLower(strings.TrimSpace(destination))
	return destination == prefix || strings.HasPrefix(destination, prefix+"@")
}

// RigRef is one declared rig and its place in the fill order. The config type carries the
// same three fields; decide keeps its own copy so this package stays free of config imports
// and the snapshot stays the only input.
type RigRef struct {
	Name  string `json:"name"`
	Tier  int    `json:"tier"`
	Spill string `json:"spill"`
}

// The spill triggers. A later tier is reached only when its trigger allows it.
const (
	SpillNever         = "never"
	SpillOnFull        = "on-full"
	SpillOnUnavailable = "on-unavailable"
)

// Link is a forge link. GitHub dependencies use State; GitLab related links are
// symmetric and carry the other item's state.
type Link struct {
	Number int    `json:"number"`
	State  string `json:"state"`
}

// Item is the normalized mirror row the decide loop reads.
type Item struct {
	Number    int      `json:"number"`
	Kind      ItemKind `json:"kind"`
	State     string   `json:"state"`
	Title     string   `json:"title"`
	Labels    []string `json:"labels"`
	Parent    *int     `json:"parent,omitempty"`
	BlockedBy []Link   `json:"blocked_by,omitempty"` // GitHub native, directional
	Related   []Link   `json:"related,omitempty"`    // GitLab relates_to, symmetric
	URL       string   `json:"url,omitempty"`
}

func (i Item) open() bool { return strings.EqualFold(i.State, StateOpen) }

func (i Item) onHold() bool {
	for _, label := range i.Labels {
		if label == "onhold" {
			return true
		}
	}
	return false
}

// Row is a rig queue row. The rig spells the transport state `terminal` and the row
// state `canceled`; both vocabularies are kept here because the snapshot is a mirror.
type Row struct {
	QItemID     string    `json:"qitem_id"`
	State       string    `json:"state"`
	Pickup      string    `json:"pickup"`
	Destination string    `json:"destination"`
	Body        string    `json:"body"`
	Summary     string    `json:"summary"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
	LastNudgeAt time.Time `json:"last_nudge_at,omitempty"`
}

// Branch is one origin branch the mirror saw. A branch named feature/<n>-* belongs to
// item n; an open or merged change request on it is the second half of idempotency.
type Branch struct {
	Name        string `json:"name"`
	Item        int    `json:"item"`
	HasOpenCR   bool   `json:"has_open_cr"`
	HasMergedCR bool   `json:"has_merged_cr"`
}

// Snapshot is everything a tick decides from. ForgeOK is false when the forge read
// failed: a failed read is `stale`, never "no tasks".
type Snapshot struct {
	Project  Project  `json:"project"`
	Items    []Item   `json:"items"`
	Rows     []Row    `json:"rows"`
	Branches []Branch `json:"branches"`
	Builders []string `json:"builders"`
	// WaitingOnHuman names the seats the rig reports as waiting on a human
	// (lifecycle attention_required, or a node with a pending input), folded in
	// by the caller from the same read Builders comes from (#426, design D3).
	// It carries the population Builders does — this project's builder seats —
	// minus the running requirement: a waiting seat is not a running seat, so it
	// is never in Builders, and without this list the plan cannot tell a busy
	// fleet from one blocked on a person. A non-builder seat waiting on a human
	// takes no dispatch capacity and does not belong here; the waiting panel
	// lists those separately. Empty also means the read did not carry the fact,
	// so a detail built here never claims a count nobody read.
	WaitingOnHuman []string `json:"waiting_on_human,omitempty"`
	Dispatched     []int    `json:"dispatched,omitempty"` // item numbers an earlier tick dispatched
	ForgeOK        bool     `json:"forge_ok"`
	ForgeError     string   `json:"forge_error,omitempty"`

	// RigUnread names the project's rigs whose seats this tick could not read
	// (#438). Their seats are simply absent from Builders, so the capacity the
	// plan sees is the readable rigs' — the right number to dispatch on — and a
	// refusal that would otherwise read as "the fleet is full" says which rig
	// went blind instead.
	RigUnread []string `json:"rig_unread,omitempty"`
	// RigUnknown is true when the project's capacity could not be read at all:
	// every rig it declares failed to answer. The plan then decides nothing,
	// because an unread fleet is not an empty one — treating it as empty would
	// report "no free builder" for a project that may well have builders.
	RigUnknown bool `json:"rig_unknown,omitempty"`
	// RigError is that failure, so the stale tick names what went wrong.
	RigError string `json:"rig_error,omitempty"`

	Dispatches []DispatchState `json:"dispatches,omitempty"`
	CRs        []CRState       `json:"change_requests,omitempty"`
	Verdicts   []VerdictState  `json:"verdicts,omitempty"`

	// MalformedVerdicts carries, per item, the newest verdict.malformed event
	// the ledger holds (epic #345, design D7): a note whose first line parsed
	// as a verdict but named no sha. The plan reads nothing else about the
	// fact — the fold is the same shape as Dispatched and RelayedHeads.
	MalformedVerdicts map[int]MalformedVerdict `json:"malformed_verdicts,omitempty"`

	// RelayedHeads carries the (item, head) pairs the event ledger already
	// holds a verdict.relayed for. The plan is pure and cannot read the ledger
	// itself, so the caller folds this fact in — the same way Dispatched folds
	// an earlier tick's dispatches. An empty slice means nothing was relayed;
	// the entries are lowercased shas.
	RelayedHeads []RelayedHead `json:"relayed_heads,omitempty"`
}

// RelayedHead is one (item, head) the ledger already relayed.
type RelayedHead struct {
	Item int    `json:"item"`
	Head string `json:"head"`
}

// Reason is a skip reason. Every candidate carries one; a candidate with no reason is a
// failure the caller reports.
type Reason string

const (
	ReasonEpic              Reason = "epic"
	ReasonOnHold            Reason = "onhold"
	ReasonParentOnHold      Reason = "parent_onhold"
	ReasonParentNotOpenEpic Reason = "parent_not_open_epic"
	ReasonAlreadyDispatched Reason = "already_dispatched"
	ReasonLiveRow           Reason = "live_row"
	ReasonBranchOpenCR      Reason = "branch_open_cr"
	ReasonBranchMergedCR    Reason = "branch_merged_cr"
	ReasonBlocked           Reason = "blocked"
	ReasonNoFreeBuilder     Reason = "no_free_builder"
	// ReasonEpicBusy: another child of the same epic holds it — a live dispatch
	// or an open unmerged change request (#443).
	ReasonEpicBusy Reason = "epic_busy"
)

// Status is the tick's outcome.
type Status string

const (
	StatusPlanned Status = "planned"
	StatusNothing Status = "nothing"
	StatusStale   Status = "stale"
)

// Skip records why an item was not dispatched.
type Skip struct {
	Number int    `json:"number"`
	Reason Reason `json:"reason"`
	Detail string `json:"detail,omitempty"`
}

// Dispatch is one ready item and the free seat that would take it.
type Dispatch struct {
	Number int    `json:"number"`
	Seat   string `json:"seat"`
}

// Result is the whole plan: deterministic for a snapshot.
type Result struct {
	Status Status `json:"status"`
	// Free is the number of seats that could take work. Ready is how many items
	// the rules found dispatchable, counted before the concurrency limit: a rig
	// with Ready > 0 and Free == 0 has work and no seat to take it (design D1).
	Free       int        `json:"free"`
	Ready      int        `json:"ready"`
	Dispatches []Dispatch `json:"dispatches"`
	Skips      []Skip     `json:"skips"`
	Error      string     `json:"error,omitempty"`
}

var terminalRowStates = map[string]struct{}{
	"terminal": {}, "closed": {}, "done": {}, "cancelled": {}, "canceled": {},
	"denied": {}, "failed": {}, "handed-off": {}, "handed_off": {}, "handedoff": {},
}

var activeRowStates = map[string]struct{}{
	"pending": {}, "working": {}, "claimed": {}, "in-progress": {},
	"unclaimed": {}, "delivered": {}, "delivered-ack-pending": {}, "ack-pending": {},
}

var issueNumber = regexp.MustCompile(`#(\d+)\b`)

// Decide applies the dispatcher's rules to one project snapshot.
func Decide(s Snapshot) Result {
	if !s.ForgeOK {
		return Result{Status: StatusStale, Error: s.ForgeError}
	}
	if !s.Project.Enabled || s.Project.OnHold {
		// A disabled or held project is not a tick source; it produces no intents.
		return Result{Status: StatusNothing}
	}
	if s.RigUnknown {
		// Unknown capacity is stale, not empty (#438). The one line the caller
		// reports names the rig read, so the operator learns the project was not
		// planned rather than that it had nowhere to dispatch.
		detail := s.RigError
		if detail == "" {
			detail = "rig read failed"
		}
		return Result{Status: StatusStale, Error: detail}
	}

	items := append([]Item(nil), s.Items...)
	sort.Slice(items, func(a, b int) bool { return items[a].Number < items[b].Number })

	byNumber := make(map[int]Item, len(items))
	epics := make(map[int]bool)
	for _, item := range items {
		byNumber[item.Number] = item
		if item.Kind == KindEpic && item.open() {
			epics[item.Number] = true
		}
	}

	dispatched := make(map[int]bool, len(s.Dispatched))
	for _, number := range s.Dispatched {
		dispatched[number] = true
	}

	busy := busyBuilders(s.Rows)
	free := freeBuilders(s.Builders, busy)
	// The tier order decides which of those free seats this project may use. fillSeats returns the
	// admitted seats and the reason a later tier was refused, which the skip detail quotes.
	free, tierRefusal := fillSeats(s.Project.Rigs, free, s.Builders, s.Project.ConcurrencyCap)
	limit := len(free)
	if s.Project.ConcurrencyCap > 0 && s.Project.ConcurrencyCap < limit {
		limit = s.Project.ConcurrencyCap
	}

	liveRows := inflightNumbers(s.Rows)
	branches := branchIndex(s.Branches)

	// The epic rule (#443, design D1): a child holds its epic while it has a
	// live dispatch row or an open unmerged change request, because a builder's
	// dispatch ends when the pull request opens and the next sibling starting off
	// main is the conflict this prevents. A sibling on hold holds nothing.
	holders := map[int]epicHolder{}
	claimed := map[int]int{}
	if !s.Project.EpicParallel {
		for _, item := range items {
			if !item.open() || item.Kind == KindEpic || item.onHold() || item.Parent == nil {
				continue
			}
			if _, openEpic := epics[*item.Parent]; !openEpic {
				continue
			}
			_, inFlight := liveRows[item.Number]
			inReview := false
			if branch, ok := branches[item.Number]; ok {
				inReview = branch.HasOpenCR && !branch.HasMergedCR
			}
			if !inFlight && !inReview {
				continue
			}
			if existing, held := holders[*item.Parent]; !held || (existing.review && inFlight) {
				holders[*item.Parent] = epicHolder{number: item.Number, review: !inFlight}
			}
		}
	}

	var result Result
	result.Free = len(free)
	var ready []Item
	markReady := func(item Item) {
		ready = append(ready, item)
		if item.Parent != nil {
			claimed[*item.Parent] = item.Number
		}
	}
	// epicGate is the one-child rule (#443) at the last moment before a candidate
	// becomes ready, so the item's own facts — a blocking relation, a live row, an
	// open branch — keep their existing skip and the epic wait is the reason only
	// when nothing more specific applies.
	epicGate := func(item Item) (Skip, bool) {
		if s.Project.EpicParallel || item.Parent == nil {
			return Skip{}, false
		}
		if holder, held := holders[*item.Parent]; held && holder.number != item.Number {
			return epicBusySkip(item.Number, holder), true
		}
		if chosen, taken := claimed[*item.Parent]; taken && chosen != item.Number {
			return epicBusySkip(item.Number, epicHolder{number: chosen}), true
		}
		return Skip{}, false
	}

	for _, item := range items {
		if !item.open() {
			continue
		}
		if item.Kind == KindEpic {
			result.Skips = append(result.Skips, Skip{Number: item.Number, Reason: ReasonEpic})
			continue
		}
		if item.onHold() {
			result.Skips = append(result.Skips, Skip{Number: item.Number, Reason: ReasonOnHold})
			continue
		}
		if item.Parent != nil {
			if parent, ok := byNumber[*item.Parent]; ok && parent.onHold() {
				result.Skips = append(result.Skips, Skip{
					Number: item.Number, Reason: ReasonParentOnHold,
					Detail: fmt.Sprintf("parent #%d carries onhold", *item.Parent),
				})
				continue
			}
			if !epics[*item.Parent] {
				result.Skips = append(result.Skips, Skip{
					Number: item.Number, Reason: ReasonParentNotOpenEpic,
					Detail: fmt.Sprintf("parent #%d is not an open epic", *item.Parent),
				})
				continue
			}
		}
		if dispatched[item.Number] {
			result.Skips = append(result.Skips, Skip{Number: item.Number, Reason: ReasonAlreadyDispatched})
			continue
		}
		if qid, ok := liveRows[item.Number]; ok {
			result.Skips = append(result.Skips, Skip{
				Number: item.Number, Reason: ReasonLiveRow, Detail: qid,
			})
			continue
		}
		if branch, ok := branches[item.Number]; ok {
			if branch.HasMergedCR {
				result.Skips = append(result.Skips, Skip{
					Number: item.Number, Reason: ReasonBranchMergedCR, Detail: branch.Name,
				})
				continue
			}
			if branch.HasOpenCR {
				result.Skips = append(result.Skips, Skip{
					Number: item.Number, Reason: ReasonBranchOpenCR, Detail: branch.Name,
				})
				continue
			}
			// A branch with no change request is unfinished work: adopt it.
			if skip, busy := epicGate(item); busy {
				result.Skips = append(result.Skips, skip)
				continue
			}
			markReady(item)
			continue
		}
		if blockers := openBlockers(s.Project.Forge, item); len(blockers) > 0 {
			result.Skips = append(result.Skips, Skip{
				Number: item.Number, Reason: ReasonBlocked, Detail: "#" + joinNumbers(blockers),
			})
			continue
		}
		if skip, busy := epicGate(item); busy {
			result.Skips = append(result.Skips, skip)
			continue
		}
		markReady(item)
	}

	result.Ready = len(ready)
	if len(ready) == 0 {
		result.Status = StatusNothing
		return result
	}

	// One refusal detail for the whole plan: every ready item over the limit is
	// refused for the same reason, and the count must not depend on which item
	// happened to ask (#426). exhausted gates the human count: while a free seat
	// remains, the refusal is the cap's doing, not the fleet's.
	refusalDetail := noFreeBuilderDetail(tierRefusal, s.Project.Rigs, s.WaitingOnHuman, s.RigUnread, len(free) == 0)

	for index, item := range ready {
		if index >= limit {
			result.Skips = append(result.Skips, Skip{
				Number: item.Number, Reason: ReasonNoFreeBuilder, Detail: refusalDetail,
			})
			continue
		}
		result.Dispatches = append(result.Dispatches, Dispatch{Number: item.Number, Seat: free[index]})
	}
	if len(result.Dispatches) > 0 {
		result.Status = StatusPlanned
	} else {
		result.Status = StatusNothing
	}
	return result
}

// openBlockers applies the forge's one-way rule. GitHub dependencies are already
// directional; GitLab relates_to is symmetric, so only a smaller open number blocks.
func openBlockers(forge Forge, item Item) []int {
	var out []int
	switch forge {
	case ForgeGitLab:
		for _, link := range item.Related {
			if link.Number < item.Number && strings.EqualFold(link.State, StateOpen) {
				out = append(out, link.Number)
			}
		}
	default:
		for _, link := range item.BlockedBy {
			if strings.EqualFold(link.State, StateOpen) {
				out = append(out, link.Number)
			}
		}
	}
	sort.Ints(out)
	return out
}

func busyBuilders(rows []Row) map[string]bool {
	busy := make(map[string]bool)
	for _, row := range rows {
		state := strings.ToLower(row.State)
		pickup := strings.ToLower(row.Pickup)
		if _, terminal := terminalRowStates[state]; terminal {
			continue
		}
		_, stateActive := activeRowStates[state]
		_, pickupActive := activeRowStates[pickup]
		if !stateActive && !pickupActive {
			continue
		}
		if row.Destination != "" {
			busy[row.Destination] = true
		}
	}
	return busy
}

func freeBuilders(builders []string, busy map[string]bool) []string {
	free := make([]string, 0, len(builders))
	seen := make(map[string]bool)
	for _, seat := range builders {
		if seat == "" || seen[seat] || busy[seat] {
			continue
		}
		seen[seat] = true
		free = append(free, seat)
	}
	return free
}

// rigOf reads the rig a seat belongs to from the `@<rig>` suffix of its session name. A seat with
// no suffix belongs to no declared rig and is ignored by the fill order.
func rigOf(session string) string {
	at := strings.LastIndex(session, "@")
	if at < 0 || at == len(session)-1 {
		return ""
	}
	return session[at+1:]
}

// seatsByRig groups free seats by their rig. Order within a rig follows the builders list, so the
// same snapshot always fills the same seat.
func seatsByRig(free []string) map[string][]string {
	out := make(map[string][]string, len(free))
	for _, seat := range free {
		rigName := rigOf(seat)
		if rigName == "" {
			continue
		}
		out[rigName] = append(out[rigName], seat)
	}
	return out
}

// rigHasRunningSeat reports whether any builder seat runs on the named rig. An empty rig has no
// running seat, which is the condition `on-unavailable` tests.
func rigHasRunningSeat(builders []string, rigName string) bool {
	for _, seat := range builders {
		if rigOf(seat) == rigName {
			return true
		}
	}
	return false
}

// refusalKind separates the two ways a walk can end with no seat: a later tier's spill trigger said
// no, or every tier that was reached had no free seat. They read the same to the operator — nothing
// can be dispatched — but only the second is the all-seats-taken statement the capacity line
// qualifies with a human count (#426).
type refusalKind string

const (
	// refusalSpill: a spill trigger refused the walk. The trigger is the fact.
	refusalSpill refusalKind = "spill"
	// refusalTaken: no trigger refused, a reached tier simply had no free seat — the same statement
	// as `every admitted seat is taken`, one tier down.
	refusalTaken refusalKind = "taken"
)

// tierRefusal is why a later tier was not reached. The skip detail quotes it as
// `tier N (spill=<trigger>: <fact>)`, so the three parts are kept apart until the string is built.
type tierRefusal struct {
	Kind  refusalKind
	Tier  int
	Name  string
	Spill string
	Fact  string
}

// noFreeBuilderDetail is the detail every no_free_builder skip in one plan carries. Both forms that
// say the seats are taken — the bare line and a reached tier with no free seat — gain the human count
// when a seat waits on one and no free seat remained, because "busy fleet" and "fleet blocked on a
// person" are different problems with different repairs (#426, design D3). A spill trigger refusal
// names its own reason and is left alone: two facts on one line read as one. exhausted is whether the
// plan had no free seat at all; a cap-bound refusal is the cap's doing, so a count there would answer
// a question nobody asked.
func noFreeBuilderDetail(refusal tierRefusal, rigs []RigRef, waiting, unread []string, exhausted bool) string {
	count := 0
	if exhausted {
		count = waitingOnHuman(rigs, waiting)
	}
	var detail string
	if rendered := refusal.String(); rendered != "" {
		if refusal.Kind == refusalTaken {
			detail = withHumanCount(rendered, count)
		} else {
			detail = rendered
		}
	} else {
		detail = withHumanCount("every admitted seat is taken", count)
	}
	// A rig that went unread explains a zero capacity better than either form
	// above: the seats are not busy, they are unseen (#438). Same gate as the
	// human count — while a free seat remains the skip is the cap's doing, and a
	// blind rig did not cause it.
	if exhausted {
		detail = withUnreadRigs(detail, unread)
	}
	return detail
}

// withUnreadRigs names the rigs whose read failed, and leaves the line alone when
// every rig answered: no detail ever grows an empty `unread`.
func withUnreadRigs(detail string, unread []string) string {
	named := make([]string, 0, len(unread))
	for _, rigName := range unread {
		if rigName != "" {
			named = append(named, rigName)
		}
	}
	switch len(named) {
	case 0:
		return detail
	case 1:
		return fmt.Sprintf("%s; rig %s unread", detail, named[0])
	default:
		return fmt.Sprintf("%s; rigs %s unread", detail, strings.Join(named, ", "))
	}
}

// withHumanCount appends the count to a detail line, and leaves the line byte for byte alone when
// nobody is waiting: no detail ever grows a `(0 waiting on a human)`.
func withHumanCount(detail string, count int) string {
	if count <= 0 {
		return detail
	}
	return fmt.Sprintf("%s (%d waiting on a human)", detail, count)
}

// waitingOnHuman counts the admitted seats the rig reports as waiting on a human. A seat on a rig
// the project does not declare is ignored, exactly as fillSeats ignores it; a flat list (no tier
// declaration) admits every seat, so every waiting seat counts there.
func waitingOnHuman(rigs []RigRef, waiting []string) int {
	if len(rigs) == 0 {
		count := 0
		for _, seat := range waiting {
			if seat != "" {
				count++
			}
		}
		return count
	}
	declared := make(map[string]bool, len(rigs))
	for _, ref := range rigs {
		declared[ref.Name] = true
	}
	count := 0
	for rigName, seats := range seatsByRig(waiting) {
		if declared[rigName] {
			count += len(seats)
		}
	}
	return count
}

// String renders the spec's refusal form, `tier N (spill=<trigger>: <fact>)`, or "" when no tier
// refused the item. The rig's name goes in the fact so the operator knows which rig came up empty
// without the form growing a second parenthetical.
func (r tierRefusal) String() string {
	if r.Fact == "" {
		return ""
	}
	return fmt.Sprintf("tier %d (spill=%s: %s on %s)", r.Tier, r.Spill, r.Fact, r.Name)
}

// fillSeats walks the declared rigs in order and returns the seats this project may use, with the
// refusal when a tier stopped the walk. A seat on a rig the project does not declare is ignored.
//
// The trigger on a later tier decides whether reaching it is allowed:
//
//   - never: unused while any earlier tier exists
//   - on-full: allowed when no free seat remains in the earlier tiers, the concurrency cap included
//   - on-unavailable: allowed only when an earlier tier's rig has no running seat
//
// The first argument is the whole free list so the caller's primitives stay unchanged; the second is
// every running builder, which is what tells an empty rig from a busy one.
func fillSeats(rigs []RigRef, free, builders []string, cap int) ([]string, tierRefusal) {
	if len(rigs) == 0 {
		// No tier declaration: the flat list behaves exactly as before.
		return free, tierRefusal{}
	}
	byRig := seatsByRig(free)
	declared := make(map[string]bool, len(rigs))
	chosen := make([]string, 0, len(free))
	// admitted records that an earlier tier was reached. A later tier is considered only then, so a
	// `never` tier cannot be entered past a tier that itself refused.
	admitted := false
	var refusal, empty tierRefusal
	for index, ref := range rigs {
		if declared[ref.Name] {
			continue
		}
		declared[ref.Name] = true
		first := index == 0
		if !first && !admitted {
			break
		}
		if !first {
			if fact := spillFact(rigs[:index], free, builders, cap, ref.Spill); fact != "" {
				refusal = tierRefusal{Kind: refusalSpill, Tier: ref.Tier, Name: ref.Name, Spill: ref.Spill, Fact: fact}
				break
			}
		}
		seats := byRig[ref.Name]
		if len(seats) > 0 {
			chosen = append(chosen, seats...)
			admitted = true
			continue
		}
		// A tier with no free seat: the walk may still reach a later one. The fact is remembered so a
		// skip with no seat anywhere names the first tier that came up empty, but it is not a refusal
		// while a later tier still supplies a seat.
		if empty.Fact == "" {
			empty = tierRefusal{
				Kind: refusalTaken,
				Tier: ref.Tier, Name: ref.Name, Spill: ref.Spill,
				Fact: "no free seat on this rig",
			}
		}
		admitted = true
	}
	// A refusal only matters when no seat was admitted at all. A walk that spilled successfully is
	// not a refusal, even though an earlier tier came up empty on the way.
	if len(chosen) > 0 {
		return chosen, tierRefusal{}
	}
	if refusal.Fact != "" {
		return chosen, refusal
	}
	return chosen, empty
}

// spillFact answers whether a later tier may be reached, naming the earlier tier that refuses it. An
// empty answer means the spill is allowed.
func spillFact(earlier []RigRef, free, builders []string, cap int, spill string) string {
	switch spill {
	case SpillNever:
		return "an earlier tier exists"
	case SpillOnUnavailable:
		for _, ref := range earlier {
			if !rigHasRunningSeat(builders, ref.Name) {
				return ""
			}
		}
		return "every earlier tier has a running seat"
	case SpillOnFull, "":
		byRig := seatsByRig(free)
		remaining := 0
		for _, ref := range earlier {
			remaining += len(byRig[ref.Name])
		}
		if cap > 0 && cap < remaining {
			remaining = cap
		}
		if remaining > 0 {
			return fmt.Sprintf("%d free seat(s) remain in an earlier tier", remaining)
		}
		return ""
	default:
		return fmt.Sprintf("unknown trigger %q", spill)
	}
}

// inflightNumbers maps a task number to the live row whose body names it. The
// dispatcher's own state only knows the rows it created; a row made by hand still
// blocks a second dispatch.
func inflightNumbers(rows []Row) map[int]string {
	live := make(map[int]string)
	for _, row := range rows {
		state := strings.ToLower(row.Pickup)
		if state == "" {
			state = strings.ToLower(row.State)
		}
		if _, terminal := terminalRowStates[state]; terminal {
			continue
		}
		head := row.Body
		if len(head) > 400 {
			head = head[:400]
		}
		head += " " + row.Summary
		for _, match := range issueNumber.FindAllStringSubmatch(head, -1) {
			number, err := strconv.Atoi(match[1])
			if err == nil && number > 0 {
				live[number] = row.QItemID
			}
		}
	}
	return live
}

func branchIndex(branches []Branch) map[int]Branch {
	index := make(map[int]Branch)
	for _, branch := range branches {
		number := branch.Item
		if number == 0 {
			number = BranchItem(branch.Name)
		}
		if number == 0 {
			continue
		}
		current, exists := index[number]
		if !exists {
			index[number] = branch
			continue
		}
		current.HasOpenCR = current.HasOpenCR || branch.HasOpenCR
		current.HasMergedCR = current.HasMergedCR || branch.HasMergedCR
		if current.Name == "" {
			current.Name = branch.Name
		}
		index[number] = current
	}
	return index
}

var branchPrefix = regexp.MustCompile(`^feature/(\d+)-`)

// BranchItem extracts the item number from a change request's branch name
// (`feature/138-...` → 138), or 0 when the branch carries none. Shared with the
// no-item diagnostic in act (#314): a branch number is a mention, never an
// association.
func BranchItem(name string) int {
	match := branchPrefix.FindStringSubmatch(name)
	if match == nil {
		return 0
	}
	number, err := strconv.Atoi(match[1])
	if err != nil {
		return 0
	}
	return number
}

// ItemToken extracts the first `#<n>` mention anywhere in text, or 0 when the
// text names no item. Shared with the no-item diagnostic in act (#314): the
// first mention is the item the change request probably meant — diagnostic
// only, never an association.
func ItemToken(text string) int {
	match := issueNumber.FindStringSubmatch(text)
	if match == nil {
		return 0
	}
	number, err := strconv.Atoi(match[1])
	if err != nil {
		return 0
	}
	return number
}

func joinNumbers(numbers []int) string {
	parts := make([]string, len(numbers))
	for index, number := range numbers {
		parts[index] = fmt.Sprintf("%d", number)
	}
	return strings.Join(parts, ", #")
}

// epicHolder is the child that currently holds an epic (#443).
type epicHolder struct {
	number int
	review bool // true when the hold is an open change request, not a dispatch
}

// epicBusySkip names the holder: "in flight" for a dispatch, "in review" for an
// open change request (design D4).
func epicBusySkip(item int, holder epicHolder) Skip {
	detail := fmt.Sprintf("#%d in flight", holder.number)
	if holder.review {
		detail = fmt.Sprintf("#%d in review", holder.number)
	}
	return Skip{Number: item, Reason: ReasonEpicBusy, Detail: detail}
}
