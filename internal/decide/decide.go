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
	Key            string `json:"key"`
	Forge          Forge  `json:"forge"`
	Enabled        bool   `json:"enabled"`
	OnHold         bool   `json:"onhold"`
	ConcurrencyCap int    `json:"concurrency_cap"` // 0 = no cap beyond free builders
}

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
	QItemID     string `json:"qitem_id"`
	State       string `json:"state"`
	Pickup      string `json:"pickup"`
	Destination string `json:"destination"`
	Body        string `json:"body"`
	Summary     string `json:"summary"`
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
	Project    Project  `json:"project"`
	Items      []Item   `json:"items"`
	Rows       []Row    `json:"rows"`
	Branches   []Branch `json:"branches"`
	Builders   []string `json:"builders"`
	Dispatched []int    `json:"dispatched,omitempty"` // item numbers an earlier tick dispatched
	ForgeOK    bool     `json:"forge_ok"`
	ForgeError string   `json:"forge_error,omitempty"`
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
	Status     Status     `json:"status"`
	Free       int        `json:"free"`
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
	limit := len(free)
	if s.Project.ConcurrencyCap > 0 && s.Project.ConcurrencyCap < limit {
		limit = s.Project.ConcurrencyCap
	}

	liveRows := inflightNumbers(s.Rows)
	branches := branchIndex(s.Branches)

	var result Result
	result.Free = len(free)
	var ready []Item

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
			ready = append(ready, item)
			continue
		}
		if blockers := openBlockers(s.Project.Forge, item); len(blockers) > 0 {
			result.Skips = append(result.Skips, Skip{
				Number: item.Number, Reason: ReasonBlocked, Detail: "#" + joinNumbers(blockers),
			})
			continue
		}
		ready = append(ready, item)
	}

	if len(ready) == 0 {
		result.Status = StatusNothing
		return result
	}

	for index, item := range ready {
		if index >= limit {
			result.Skips = append(result.Skips, Skip{Number: item.Number, Reason: ReasonNoFreeBuilder})
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
			number = branchItem(branch.Name)
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

func branchItem(name string) int {
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

func joinNumbers(numbers []int) string {
	parts := make([]string, len(numbers))
	for index, number := range numbers {
		parts[index] = fmt.Sprintf("%d", number)
	}
	return strings.Join(parts, ", #")
}
