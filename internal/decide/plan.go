package decide

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// MarkerLabel is the in-flight marker the app owns on both forges.
const MarkerLabel = "status::ongoing"

const (
	// StalledAfter is how long an ended dispatch with no open change request waits
	// before the next attempt replaces it.
	StalledAfter = 30 * time.Minute
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
)

// Event is the audit row an intent writes.
type Event struct {
	Actor   string `json:"actor"`
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
	Detail  string `json:"detail"`
}

// Intent is one planned action. The decide loop never performs it.
type Intent struct {
	Kind    IntentKind `json:"kind"`
	Project string     `json:"project"`
	Item    int        `json:"item,omitempty"`
	Seat    string     `json:"seat,omitempty"`
	QItemID string     `json:"qitem_id,omitempty"`
	Text    string     `json:"text,omitempty"`
	Event   Event      `json:"event"`
}

// Plan is one tick's intents and the events they would write.
type Plan struct {
	Status  Status   `json:"status"`
	Free    int      `json:"free"`
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
	plan.Status, plan.Free, plan.Skips, plan.Error = result.Status, result.Free, result.Skips, result.Error
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

	// Incident: a change request is open, no verdict exists and no row is live.
	seen := map[int]bool{}
	for _, cr := range s.CRs {
		item, ok := items[cr.Item]
		if !ok || !item.open() || seen[item.Number] {
			continue
		}
		if !cr.Merged && strings.EqualFold(cr.State, "open") && live[item.Number] == "" &&
			!hasAcceptedVerdict(s.Verdicts, item.Number, cr.HeadSHA) {
			seen[item.Number] = true
			text := fmt.Sprintf("%s #%d: %s has an open change request with no verdict and no live row. Accept it, reject it, or reopen the seat?",
				key, item.Number, item.Title)
			add(Intent{
				Kind: IntentRaiseDecision, Project: key, Item: item.Number, Text: text,
				Event: Event{Actor: "decide", Kind: "decision.raise", Subject: fmt.Sprintf("#%d", item.Number), Detail: text},
			})
		}
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

func endedDispatchFor(dispatches []DispatchState, item int) bool {
	for _, dispatch := range dispatches {
		if dispatch.Item == item && !dispatch.EndedAt.IsZero() {
			return true
		}
	}
	return false
}

func hasAcceptedVerdict(verdicts []VerdictState, item int, sha string) bool {
	for _, verdict := range verdicts {
		if verdict.Item != item || !strings.EqualFold(verdict.Decision, "accept") {
			continue
		}
		if sha == "" || verdict.SHA == sha {
			return true
		}
	}
	return false
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
