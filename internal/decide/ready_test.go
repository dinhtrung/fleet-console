package decide

import (
	"testing"
	"time"
)

// TestDecideReadyCountsBeforeTheLimit is the demand signal the lifecycle reads:
// three ready items and one free seat must report Ready=3 while dispatching one.
func TestDecideReadyCountsBeforeTheLimit(t *testing.T) {
	snapshot := base(task(1), task(2), task(3))
	snapshot.Builders = []string{"seat-1"}

	result := Decide(snapshot)

	if result.Ready != 3 {
		t.Fatalf("Ready = %d, want 3 (before the concurrency limit)", result.Ready)
	}
	if result.Free != 1 {
		t.Fatalf("Free = %d, want 1", result.Free)
	}
	if len(result.Dispatches) != 1 {
		t.Fatalf("dispatches = %+v, want exactly one", result.Dispatches)
	}
	if result.Status != StatusPlanned {
		t.Fatalf("status = %q, want planned", result.Status)
	}
}

// TestDecideReadyIsZeroWithNoSeats: a stopped rig reports the demand the
// lifecycle needs — ready items, no free seat — without dispatching anything.
func TestDecideReadyIsZeroWithNoSeats(t *testing.T) {
	snapshot := base(task(1), task(2))
	snapshot.Builders = nil

	result := Decide(snapshot)

	if result.Ready != 2 || result.Free != 0 {
		t.Fatalf("Ready/Free = %d/%d, want 2/0", result.Ready, result.Free)
	}
	if len(result.Dispatches) != 0 || result.Status != StatusNothing {
		t.Fatalf("result = %+v, want nothing dispatched", result)
	}
}

// TestPlanTickCarriesReady pins the value through PlanTick: the plan is what the
// tick hands the lifecycle.
func TestPlanTickCarriesReady(t *testing.T) {
	snapshot := base(task(1), task(2))
	snapshot.Builders = []string{"seat-1"}

	plan := PlanTick(snapshot, time.Now())

	if plan.Ready != 2 {
		t.Fatalf("plan.Ready = %d, want 2", plan.Ready)
	}
}
