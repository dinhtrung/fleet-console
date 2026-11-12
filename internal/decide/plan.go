package decide

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// MarkerLabel is the in-flight marker the app owns on both forges.
const MarkerLabel = "status::ongoing"

const (
	// StalledAfter is how long an ended dispatch with no open change request waits
	// before the next attempt replaces it.
	StalledAfter = 30 * time.Minute
	// SilentDispatchAfter is how long a dispatch that is still live and has
	// produced no change request waits before the operator is asked (#261).
	// The two thresholds measure different clocks: StalledAfter runs on an
	// attempt that already ended, this one on a dispatch whose seat is still
	// holding it and has produced nothing — the reset rule cannot see that
	// case, and the merge watchdog walks open change requests, so without
	// this constant the dispatch is invisible forever.
	SilentDispatchAfter = 30 * time.Minute
	// RenudgeAfter is how long an unclaimed row waits before a nudge.
	RenudgeAfter = 15 * time.Minute
	// RenudgeCooldown is the shortest gap between two nudges of one row.
	RenudgeCooldown = 30 * time.Minute
	// MaxAttempts is the point at which a stalled item stops being retried.
	MaxAttempts = 3
)

// DispatchState is one owned dispatch the snapshot carries.
type DispatchState struct {
	Item      int       `json:"item"`
	Seat      string    `json:"seat,omitempty"`
	Branch    string    `json:"branch,omitempty"`
	QItemID   string    `json:"qitem_id,omitempty"`
	Attempt   int       `json:"attempt"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
	EndReason string    `json:"end_reason,omitempty"`
}

// CRState is one normalized change request in the snapshot.
type CRState struct {
	Number   int    `json:"number"`
	Item     int    `json:"item"`
	State    string `json:"state"`
	Merged   bool   `json:"merged"`
	HeadSHA  string `json:"head_sha,omitempty"`
	DocsOnly bool   `json:"docs_only,omitempty"`
}

// VerdictState is one reviewer verdict in the snapshot.
type VerdictState struct {
	Item     int    `json:"item"`
	SHA      string `json:"sha"`
	Decision string `json:"decision"`
}

// IntentKind is one action the act loop would take.
type IntentKind string

const (
	IntentDispatch      IntentKind = "dispatch"
	IntentNudge         IntentKind = "nudge"
	IntentClearMarker   IntentKind = "clear_marker"
	IntentCloseEpic     IntentKind = "close_epic"
	IntentRaiseDecision IntentKind = "raise_decision"
	// IntentSilentDispatch asks the operator about a live dispatch that
	// produced no change request (#261). It is a question, not a reset: the
	// seat is not killed and nothing is re-dispatched.
	IntentSilentDispatch IntentKind = "silent_dispatch"
)

// Event is the audit row an intent writes.
type Event struct {
	Actor   string `json:"actor"`
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
	Detail  string `json:"detail"`
}

// Audience is who a decision is for. The zero value is the reviewer seat:
// every decision before the audience split went to the reviewer, so callers
// that never set the field keep today's behaviour (#265).
type Audience int

const (
	AudienceReviewer Audience = iota
	AudienceOperator
)

func (a Audience) String() string {
	if a == AudienceOperator {
		return "operator"
	}
	return "reviewer"
}

// Intent is one planned action. The decide loop never performs it.
type Intent struct {
	Kind    IntentKind `json:"kind"`
	Project string     `json:"project"`
	Item    int        `json:"item,omitempty"`
	Seat    string     `json:"seat,omitempty"`
	QItemID string     `json:"qitem_id,omitempty"`
	Text    string     `json:"text,omitempty"`
	// Audience routes the decision: the reviewer seat, or the operator (#265).
	Audience Audience `json:"audience,omitempty"`
	// ChangeRequest and HeadSHA are the facts the raised brief prints
	// verbatim; the act side does not recompute them (#265).
	ChangeRequest int    `json:"change_request,omitempty"`
	HeadSHA       string `json:"head_sha,omitempty"`
	Event         Event  `json:"event"`
}

// Plan is one tick's intents and the events they would write.
type Plan struct {
	Status Status `json:"status"`
	Free   int    `json:"free"`
	// Ready is the dispatchable item count before the concurrency limit, the
	// demand signal the lifecycle start rule reads (design D1).
	Ready   int      `json:"ready"`
	Skips   []Skip   `json:"skips"`
	Intents []Intent `json:"intents"`
	Events  []Event  `json:"events"`
	Error   string   `json:"error,omitempty"`
}

// PlanTick runs housekeeping first, then the dispatch rules. Same snapshot and time,
// same plan.
func PlanTick(s Snapshot, now time.Time) Plan {
	var plan Plan
	housekeeping(&plan, s, now)

	if !s.ForgeOK {
		plan.Status = StatusStale
		plan.Error = s.ForgeError
		plan.Events = append(plan.Events, Event{
			Actor: "decide", Kind: "tick.stale", Subject: s.Project.Key,
			Detail: s.ForgeError,
		})
		return plan
	}

	result := Decide(s)
	plan.Status, plan.Free, plan.Ready = result.Status, result.Free, result.Ready
	plan.Skips, plan.Error = result.Skips, result.Error
	for _, dispatch := range result.Dispatches {
		subject := fmt.Sprintf("#%d", dispatch.Number)
		event := Event{Actor: "decide", Kind: "dispatch.create", Subject: subject, Detail: "seat " + dispatch.Seat}
		plan.Intents = append(plan.Intents, Intent{
			Kind: IntentDispatch, Project: s.Project.Key, Item: dispatch.Number, Seat: dispatch.Seat, Event: event,
		})
		plan.Events = append(plan.Events, event)
	}
	return plan
}

func housekeeping(plan *Plan, s Snapshot, now time.Time) {
	items := make(map[int]Item, len(s.Items))
	for _, item := range s.Items {
		items[item.Number] = item
	}
	live := inflightNumbers(s.Rows)
	key := s.Project.Key

	add := func(intent Intent) {
		plan.Intents = append(plan.Intents, intent)
		plan.Events = append(plan.Events, intent.Event)
	}
	event := func(kind, subject, detail string) {
		plan.Events = append(plan.Events, Event{Actor: "decide", Kind: kind, Subject: subject, Detail: detail})
	}

	// Prune dispatches whose item is closed or gone.
	for _, dispatch := range s.Dispatches {
		item, ok := items[dispatch.Item]
		if ok && item.open() {
			continue
		}
		event("dispatch.prune", fmt.Sprintf("#%d", dispatch.Item),
			fmt.Sprintf("item closed or gone; attempt %d ended", dispatch.Attempt))
	}

	// Reset a stalled attempt, or abandon it at the attempt limit.
	for _, dispatch := range s.Dispatches {
		item, ok := items[dispatch.Item]
		if !ok || !item.open() || dispatch.EndedAt.IsZero() {
			continue
		}
		if now.Sub(dispatch.EndedAt) < StalledAfter || openCRFor(s.CRs, dispatch.Item) {
			continue
		}
		kind := "dispatch.reset"
		if dispatch.Attempt >= MaxAttempts {
			kind = "dispatch.abandon"
		}
		event(kind, fmt.Sprintf("#%d", dispatch.Item),
			fmt.Sprintf("stalled %s; attempt %d/%d", now.Sub(dispatch.EndedAt).Round(time.Minute), dispatch.Attempt, MaxAttempts))
	}

	// A live dispatch that produced no change request is a stall the reset rule
	// cannot see (#261): that rule needs EndedAt set, and this dispatch has
	// none — the seat is still holding it. One intent per live dispatch per
	// tick; across ticks the dedupe is the act raise's ledger entry, keyed by
	// (item, attempt). An open change request is the merge gate's case, not a
	// stall, and a dispatch whose age cannot be measured (no CreatedAt) is
	// never called silent on a guess.
	for _, dispatch := range s.Dispatches {
		item, ok := items[dispatch.Item]
		if !ok || !item.open() || !dispatch.EndedAt.IsZero() {
			continue
		}
		if dispatch.CreatedAt.IsZero() || now.Sub(dispatch.CreatedAt) < SilentDispatchAfter {
			continue
		}
		if openCRFor(s.CRs, dispatch.Item) {
			continue
		}
		add(Intent{
			Kind: IntentSilentDispatch, Project: key, Item: dispatch.Item, Seat: dispatch.Seat,
			Event: Event{Actor: "decide", Kind: "dispatch.silent", Subject: fmt.Sprintf("#%d", dispatch.Item),
				Detail: fmt.Sprintf("seat=%s branch=%s attempt=%d age=%s", dispatch.Seat, dispatch.Branch,
					dispatch.Attempt, now.Sub(dispatch.CreatedAt).Round(time.Minute))},
		})
	}

	// Reconcile the in-flight marker: a closed item, or an ended dispatch with no live row.
	for _, item := range s.Items {
		if !hasLabel(item.Labels, MarkerLabel) {
			continue
		}
		ended := endedDispatchFor(s.Dispatches, item.Number)
		if item.open() && (live[item.Number] != "" || !ended) {
			continue
		}
		add(Intent{
			Kind: IntentClearMarker, Project: key, Item: item.Number,
			Event: Event{Actor: "decide", Kind: "marker.clear", Subject: fmt.Sprintf("#%d", item.Number),
				Detail: "item closed or no live row"},
		})
	}

	// Re-nudge an unclaimed row on an idle seat.
	for _, row := range s.Rows {
		if !rowLive(row) || row.Destination == "" || row.CreatedAt.IsZero() {
			continue
		}
		if now.Sub(row.CreatedAt) < RenudgeAfter {
			continue
		}
		if !row.LastNudgeAt.IsZero() && now.Sub(row.LastNudgeAt) < RenudgeCooldown {
			continue
		}
		add(Intent{
			Kind: IntentNudge, Project: key, QItemID: row.QItemID, Seat: row.Destination,
			Event: Event{Actor: "decide", Kind: "row.renudge", Subject: row.QItemID,
				Detail: fmt.Sprintf("unclaimed %s; last nudge %s", now.Sub(row.CreatedAt).Round(time.Minute), nudgeGap(row.LastNudgeAt, now))},
		})
	}

	// Close an epic whose children are all closed.
	for _, epic := range s.Items {
		if epic.Kind != KindEpic || !epic.open() {
			continue
		}
		children := childrenOf(s.Items, epic.Number)
		if len(children) == 0 {
			continue
		}
		allClosed := true
		for _, child := range children {
			if child.open() {
				allClosed = false
				break
			}
		}
		if allClosed {
			add(Intent{
				Kind: IntentCloseEpic, Project: key, Item: epic.Number,
				Event: Event{Actor: "decide", Kind: "epic.close", Subject: fmt.Sprintf("#%d", epic.Number),
					Detail: fmt.Sprintf("all %d children closed", len(children))},
			})
		}
	}

	// Incident: a change request is open. Three cases: no verdict at all — the
	// operator's question (#265, design D3); a verdict whose sha is no longer
	// the head — the reviewer's re-review (D2); or an accept/reject for the
	// head — nothing to raise. The dedupe is per (item, head): a live row that
	// already names this head is the same question standing, a seat's own row
	// is work in flight, and only a question row for a different head asks
	// again. Across ticks the act raise's ledger entry is the durable backstop.
	seen := map[int]bool{}
	for _, cr := range s.CRs {
		item, ok := items[cr.Item]
		if !ok || !item.open() || seen[item.Number] {
			continue
		}
		if cr.Merged || !strings.EqualFold(cr.State, "open") {
			continue
		}
		verdict := newestVerdict(s.Verdicts, item.Number)
		var text, kind, detail string
		var audience Audience
		switch {
		case verdict == nil:
			if askedForHead(s.Rows, item.Number, cr.HeadSHA) {
				continue
			}
			head := cr.HeadSHA
			if head == "" {
				head = "unknown"
			}
			kind = "decision.no_verdict"
			audience = AudienceOperator
			text = fmt.Sprintf("%s #%d %s: change request #%d has no verdict for head %s. Approve or reject it on the item drawer (POST /api/items/%d/verdict) or reply fleet: accept / fleet: reject on the item.",
				key, item.Number, item.Title, cr.Number, head, item.Number)
			detail = fmt.Sprintf("cr=%d head=%s", cr.Number, cr.HeadSHA)
		case cr.HeadSHA != "" && !strings.EqualFold(verdict.SHA, cr.HeadSHA):
			if askedForHead(s.Rows, item.Number, cr.HeadSHA) {
				continue
			}
			// D2: a verdict on a moved head asks for a re-review of the new head.
			kind = "decision.stale"
			audience = AudienceReviewer
			text = fmt.Sprintf("%s #%d: verdict stale - %s %s is on %s, head is %s; review %s",
				key, item.Number, strings.ToUpper(verdict.Decision), verdict.SHA, verdict.SHA, cr.HeadSHA, cr.HeadSHA)
			detail = text
		default:
			continue
		}
		seen[item.Number] = true
		add(Intent{
			Kind: IntentRaiseDecision, Project: key, Item: item.Number, Text: text,
			Audience: audience, ChangeRequest: cr.Number, HeadSHA: cr.HeadSHA,
			Event: Event{Actor: "decide", Kind: kind, Subject: fmt.Sprintf("#%d", item.Number), Detail: detail},
		})
	}

	// Incident: a merged change request whose item is still open.
	for _, cr := range s.CRs {
		item, ok := items[cr.Item]
		if !ok || !item.open() || !cr.Merged || seen[item.Number] {
			continue
		}
		seen[item.Number] = true
		text := fmt.Sprintf("%s #%d: change request #%d merged but the item is still open. Close it now?",
			key, item.Number, cr.Number)
		add(Intent{
			Kind: IntentRaiseDecision, Project: key, Item: item.Number, Text: text,
			Event: Event{Actor: "decide", Kind: "decision.raise", Subject: fmt.Sprintf("#%d", item.Number), Detail: text},
		})
	}
}

func hasLabel(labels []string, want string) bool {
	for _, label := range labels {
		if label == want {
			return true
		}
	}
	return false
}

func rowLive(row Row) bool {
	state := strings.ToLower(row.Pickup)
	if state == "" {
		state = strings.ToLower(row.State)
	}
	if _, terminal := terminalRowStates[state]; terminal {
		return false
	}
	_, active := activeRowStates[state]
	return active
}

func openCRFor(crs []CRState, item int) bool {
	for _, cr := range crs {
		if cr.Item == item && !cr.Merged && strings.EqualFold(cr.State, "open") {
			return true
		}
	}
	return false
}

// shaToken matches a git sha as prose carries one: 7 to 40 hex characters on
// word boundaries. It is the plan's read of "this row is about this head",
// not a parser — a row whose text names no sha is a seat's own row.
var shaToken = regexp.MustCompile(`(?i)\b[0-9a-f]{7,40}\b`)

// rowText is the text a row's dedupe reads: the body, trimmed the way the
// dispatcher's own issue match trims it, plus the summary.
func rowText(row Row) string {
	text := row.Body
	if len(text) > 400 {
		text = text[:400]
	}
	return text + " " + row.Summary
}

func rowNamesItem(row Row, item int) bool {
	for _, match := range issueNumber.FindAllStringSubmatch(rowText(row), -1) {
		if number, err := strconv.Atoi(match[1]); err == nil && number == item {
			return true
		}
	}
	return false
}

// askedForHead is the per-(item, head) dedupe the plan can honour without
// state (#265): the question a raise left behind comes back in the snapshot
// as a live row, and D3 has the question's body name the head sha. A live row
// already naming this head is the same question standing; a live row naming
// no sha is a seat at work and keeps the old quiet; only a question row for a
// different head — some live row names a sha and none names this one — asks
// again.
func askedForHead(rows []Row, item int, head string) bool {
	naming, asked, question := false, false, false
	for _, row := range rows {
		if !rowLive(row) || !rowNamesItem(row, item) {
			continue
		}
		naming = true
		text := rowText(row)
		if head != "" && strings.Contains(text, head) {
			asked = true
		}
		if shaToken.MatchString(text) {
			question = true
		}
	}
	if !naming {
		return false // no live row: nothing has been asked
	}
	if asked {
		return true // this head's question stands
	}
	return !question // a question for another head: ask again; seat work only: stay quiet
}

func endedDispatchFor(dispatches []DispatchState, item int) bool {
	for _, dispatch := range dispatches {
		if dispatch.Item == item && !dispatch.EndedAt.IsZero() {
			return true
		}
	}
	return false
}

// newestVerdict returns the newest verdict for an item. The snapshot carries them
// newest first, so the first match is the newest.
func newestVerdict(verdicts []VerdictState, item int) *VerdictState {
	for index := range verdicts {
		if verdicts[index].Item == item {
			return &verdicts[index]
		}
	}
	return nil
}

func childrenOf(items []Item, parent int) []Item {
	var out []Item
	for _, item := range items {
		if item.Parent != nil && *item.Parent == parent {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out
}

func nudgeGap(last, now time.Time) string {
	if last.IsZero() {
		return "never"
	}
	return now.Sub(last).Round(time.Minute).String()
}
