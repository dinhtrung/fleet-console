package decide

import (
	"testing"
	"time"
)

var planNow = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

func hasEvent(plan Plan, kind, subject string) bool {
	for _, event := range plan.Events {
		if event.Kind == kind && event.Subject == subject {
			return true
		}
	}
	return false
}

func findIntents(plan Plan, kind IntentKind) []Intent {
	var out []Intent
	for _, intent := range plan.Intents {
		if intent.Kind == kind {
			out = append(out, intent)
		}
	}
	return out
}

func closedTask(number int, parent *int) Item {
	return Item{Number: number, Kind: KindTask, State: StateClosed, Parent: parent}
}

func TestHousekeepingPrune(t *testing.T) {
	s := base(closedTask(1, nil))
	s.Dispatches = []DispatchState{{Item: 1, Attempt: 1, EndedAt: planNow.Add(-time.Hour)}}
	plan := PlanTick(s, planNow)
	if !hasEvent(plan, "dispatch.prune", "#1") {
		t.Fatalf("events = %+v, want dispatch.prune for #1", plan.Events)
	}
}

func TestHousekeepingResetStalled(t *testing.T) {
	s := base(task(1))
	s.Dispatches = []DispatchState{{Item: 1, Attempt: 1, CreatedAt: planNow.Add(-2 * time.Hour), EndedAt: planNow.Add(-45 * time.Minute)}}
	plan := PlanTick(s, planNow)
	if !hasEvent(plan, "dispatch.reset", "#1") {
		t.Fatalf("events = %+v, want dispatch.reset for #1", plan.Events)
	}
}

func TestHousekeepingAbandonsAtMaxAttempts(t *testing.T) {
	s := base(task(1))
	s.Dispatches = []DispatchState{{Item: 1, Attempt: MaxAttempts, EndedAt: planNow.Add(-time.Hour)}}
	plan := PlanTick(s, planNow)
	if !hasEvent(plan, "dispatch.abandon", "#1") {
		t.Fatalf("events = %+v, want dispatch.abandon for #1", plan.Events)
	}
}

func TestHousekeepingReconcileMarker(t *testing.T) {
	s := base(Item{Number: 1, Kind: KindTask, State: StateClosed, Labels: []string{MarkerLabel}})
	plan := PlanTick(s, planNow)
	intents := findIntents(plan, IntentClearMarker)
	if len(intents) != 1 || intents[0].Item != 1 {
		t.Fatalf("intents = %+v, want one clear_marker for #1", plan.Intents)
	}
	if !hasEvent(plan, "marker.clear", "#1") {
		t.Fatalf("events = %+v, want marker.clear", plan.Events)
	}
}

func TestHousekeepingRenudge(t *testing.T) {
	s := base(task(1))
	s.Rows = []Row{{
		QItemID: "qitem-1", State: "pending", Pickup: "unclaimed", Destination: "seat-1",
		CreatedAt: planNow.Add(-20 * time.Minute), LastNudgeAt: planNow.Add(-40 * time.Minute),
	}}
	plan := PlanTick(s, planNow)
	intents := findIntents(plan, IntentNudge)
	if len(intents) != 1 || intents[0].QItemID != "qitem-1" {
		t.Fatalf("intents = %+v, want one nudge for qitem-1", plan.Intents)
	}
	if !hasEvent(plan, "row.renudge", "qitem-1") {
		t.Fatalf("events = %+v, want row.renudge", plan.Events)
	}
}

func TestHousekeepingRenudgeHonoursCooldown(t *testing.T) {
	s := base(task(1))
	s.Rows = []Row{{
		QItemID: "qitem-1", State: "pending", Pickup: "unclaimed", Destination: "seat-1",
		CreatedAt: planNow.Add(-20 * time.Minute), LastNudgeAt: planNow.Add(-10 * time.Minute),
	}}
	plan := PlanTick(s, planNow)
	if len(findIntents(plan, IntentNudge)) != 0 {
		t.Fatalf("intents = %+v, want no nudge inside the cooldown", plan.Intents)
	}
}

func TestHousekeepingCloseEpic(t *testing.T) {
	parent := 10
	s := base(epic(10), closedTask(11, &parent), closedTask(12, &parent))
	plan := PlanTick(s, planNow)
	intents := findIntents(plan, IntentCloseEpic)
	if len(intents) != 1 || intents[0].Item != 10 {
		t.Fatalf("intents = %+v, want close_epic for #10", plan.Intents)
	}
	if !hasEvent(plan, "epic.close", "#10") {
		t.Fatalf("events = %+v, want epic.close", plan.Events)
	}
}

// TestSilentDispatchIntent is the #261 table: a live dispatch that produced no
// change request becomes one dispatch.silent intent after SilentDispatchAfter,
// and the four look-alike states stay silent — an open change request (the
// merge gate's case), an ended dispatch (the reset rule's), a young dispatch,
// and a closed item.
func TestSilentDispatchIntent(t *testing.T) {
	live := func(age time.Duration) DispatchState {
		return DispatchState{Item: 1, Seat: "dev-build-1@r", Branch: "feature/1-x",
			Attempt: 1, CreatedAt: planNow.Add(-age)}
	}
	ended := func(age, ago time.Duration) DispatchState {
		d := live(age)
		d.EndedAt = planNow.Add(-ago)
		return d
	}
	cases := []struct {
		name      string
		dispatch  DispatchState
		crs       []CRState
		item      Item
		wantCount int
	}{
		{"open 31m, no change request", live(31 * time.Minute), nil, task(1), 1},
		{"open 31m, open change request", live(31 * time.Minute),
			[]CRState{{Number: 9, Item: 1, State: "open"}}, task(1), 0},
		{"ended 31m ago", ended(2*time.Hour, 31*time.Minute), nil, task(1), 0},
		{"open 10m", live(10 * time.Minute), nil, task(1), 0},
		{"item closed", live(31 * time.Minute), nil, closedTask(1, nil), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := base(tc.item)
			s.Dispatches = []DispatchState{tc.dispatch}
			s.CRs = tc.crs
			plan := PlanTick(s, planNow)
			intents := findIntents(plan, IntentSilentDispatch)
			if len(intents) != tc.wantCount {
				t.Fatalf("intents = %+v, want %d silent dispatch intents", plan.Intents, tc.wantCount)
			}
			if tc.wantCount == 0 {
				return
			}
			if !hasEvent(plan, "dispatch.silent", "#1") {
				t.Fatalf("events = %+v, want dispatch.silent for #1", plan.Events)
			}
			want := "seat=dev-build-1@r branch=feature/1-x attempt=1 age=31m0s"
			if intents[0].Event.Detail != want {
				t.Fatalf("detail = %q, want %q", intents[0].Event.Detail, want)
			}
			if intents[0].Seat != "dev-build-1@r" {
				t.Fatalf("intent seat = %q, want the dispatch's seat", intents[0].Seat)
			}
		})
	}
}

func TestIncidentParked(t *testing.T) {
	s := base(Item{Number: 5, Kind: KindTask, State: StateOpen, Title: "Connection pool"})
	s.CRs = []CRState{{Number: 20, Item: 5, State: "open", HeadSHA: "abc123"}}
	plan := PlanTick(s, planNow)
	intents := findIntents(plan, IntentRaiseDecision)
	want := "test #5: Connection pool has an open change request with no verdict and no live row. Accept it, reject it, or reopen the seat?"
	if len(intents) != 1 || intents[0].Text != want {
		t.Fatalf("intents = %+v, want question %q", plan.Intents, want)
	}
}

func TestIncidentParkedSuppressedByLiveRow(t *testing.T) {
	s := base(Item{Number: 5, Kind: KindTask, State: StateOpen, Title: "Connection pool"})
	s.CRs = []CRState{{Number: 20, Item: 5, State: "open", HeadSHA: "abc123"}}
	s.Rows = []Row{{QItemID: "qitem-5", State: "in-progress", Pickup: "working", Destination: "seat-1", Body: "#5"}}
	plan := PlanTick(s, planNow)
	if len(findIntents(plan, IntentRaiseDecision)) != 0 {
		t.Fatalf("intents = %+v, want no decision while a row is live", plan.Intents)
	}
}

func TestIncidentMarkerLeak(t *testing.T) {
	s := base(Item{Number: 5, Kind: KindTask, State: StateOpen, Title: "Connection pool"})
	s.CRs = []CRState{{Number: 20, Item: 5, State: "merged", Merged: true}}
	plan := PlanTick(s, planNow)
	intents := findIntents(plan, IntentRaiseDecision)
	want := "test #5: change request #20 merged but the item is still open. Close it now?"
	if len(intents) != 1 || intents[0].Text != want {
		t.Fatalf("intents = %+v, want question %q", plan.Intents, want)
	}
}

func TestPlanStaleNeverDispatches(t *testing.T) {
	s := base(task(1))
	s.ForgeOK = false
	s.ForgeError = "gh: 401 unauthorized"
	plan := PlanTick(s, planNow)
	if plan.Status != StatusStale || plan.Error != s.ForgeError {
		t.Fatalf("plan = %+v, want stale with the forge error", plan)
	}
	if len(findIntents(plan, IntentDispatch)) != 0 {
		t.Fatalf("intents = %+v, want no dispatch on a stale snapshot", plan.Intents)
	}
	if !hasEvent(plan, "tick.stale", "test") {
		t.Fatalf("events = %+v, want tick.stale", plan.Events)
	}
}

func TestPlanDispatchIntent(t *testing.T) {
	plan := PlanTick(base(task(1)), planNow)
	intents := findIntents(plan, IntentDispatch)
	if len(intents) != 1 || intents[0].Item != 1 || intents[0].Seat != "seat-1" {
		t.Fatalf("intents = %+v, want one dispatch for #1 on seat-1", plan.Intents)
	}
	if !hasEvent(plan, "dispatch.create", "#1") {
		t.Fatalf("events = %+v, want dispatch.create", plan.Events)
	}
}

// TestIncidentStaleVerdict: D2 — a verdict on a sha that is no longer the head is
// its own state and asks for a re-review, with both shas named.
func TestIncidentStaleVerdict(t *testing.T) {
	s := base(Item{Number: 5, Kind: KindTask, State: StateOpen, Title: "Connection pool"})
	s.CRs = []CRState{{Number: 20, Item: 5, State: "open", HeadSHA: "newsha1"}}
	s.Verdicts = []VerdictState{{Item: 5, SHA: "oldsha1", Decision: "accept"}}
	plan := PlanTick(s, planNow)
	intents := findIntents(plan, IntentRaiseDecision)
	want := "test #5: verdict stale - ACCEPT oldsha1 is on oldsha1, head is newsha1; review newsha1"
	if len(intents) != 1 || intents[0].Text != want {
		t.Fatalf("intents = %+v, want %q", plan.Intents, want)
	}
	if intents[0].Event.Kind != "decision.stale" {
		t.Fatalf("event kind = %q, want decision.stale", intents[0].Event.Kind)
	}
}

// TestIncidentVerdictForHeadRaisesNothing: an accept that names the head is the
// normal review state, not a decision.
func TestIncidentVerdictForHeadRaisesNothing(t *testing.T) {
	s := base(Item{Number: 5, Kind: KindTask, State: StateOpen, Title: "Connection pool"})
	s.CRs = []CRState{{Number: 20, Item: 5, State: "open", HeadSHA: "head123"}}
	s.Verdicts = []VerdictState{{Item: 5, SHA: "head123", Decision: "accept"}}
	plan := PlanTick(s, planNow)
	if len(findIntents(plan, IntentRaiseDecision)) != 0 {
		t.Fatalf("intents = %+v, want none for an accepted head", plan.Intents)
	}
}
