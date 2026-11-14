package store

import (
	"context"
	"testing"
	"time"
)

// TestPlanReasonsKeepOneRowPerItem (#428, jobs 4.3): the table is the last plan's
// answer, not a log. Two ticks with the same reason leave one row carrying the
// later time, a changed reason overwrites the row rather than appending, and the
// reason for the item that is no longer skipped is gone — which is how a
// dispatch and a closed item both read.
func TestPlanReasonsKeepOneRowPerItem(t *testing.T) {
	ctx := context.Background()
	st := upsertStore(t)
	pid := planReasonProject(t, st, "app")
	first := time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC)
	second := first.Add(15 * time.Second)

	if err := st.ReplacePlanReasons(ctx, pid, []PlanReason{
		{ItemNumber: 5, Reason: "no_free_builder", Detail: "every admitted seat is taken", TickAt: first},
		{ItemNumber: 6, Reason: "live_row", Detail: "qitem-1", Head: "aaaa1111", TickAt: first},
	}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	// Same answer on the next tick, with the reason for #6 changed.
	if err := st.ReplacePlanReasons(ctx, pid, []PlanReason{
		{ItemNumber: 5, Reason: "no_free_builder", Detail: "every admitted seat is taken", TickAt: second},
		{ItemNumber: 6, Reason: "branch_open_cr", Detail: "feat/6", TickAt: second},
	}); err != nil {
		t.Fatalf("replace 2: %v", err)
	}
	reasons, err := st.PlanReasons(ctx, pid)
	if err != nil {
		t.Fatalf("reasons: %v", err)
	}
	if len(reasons) != 2 {
		t.Fatalf("reasons = %d rows, want one per item", len(reasons))
	}
	if reasons[0].ItemNumber != 5 || reasons[1].ItemNumber != 6 {
		t.Fatalf("reasons = %+v, want item order", reasons)
	}
	if !reasons[0].TickAt.Equal(second) {
		t.Fatalf("tick_at = %s, want the later tick %s", reasons[0].TickAt, second)
	}
	if reasons[1].Reason != "branch_open_cr" || reasons[1].Head != "" {
		t.Fatalf("#6 = %+v, want the changed reason and no head", reasons[1])
	}

	// #6 dispatched: the plan no longer skips it, so its row goes with it.
	if err := st.ReplacePlanReasons(ctx, pid, []PlanReason{
		{ItemNumber: 5, Reason: "no_free_builder", Detail: "every admitted seat is taken", TickAt: second},
	}); err != nil {
		t.Fatalf("replace 3: %v", err)
	}
	reasons, err = st.PlanReasons(ctx, pid)
	if err != nil {
		t.Fatalf("reasons 3: %v", err)
	}
	if len(reasons) != 1 || reasons[0].ItemNumber != 5 {
		t.Fatalf("reasons = %+v, want only #5", reasons)
	}

	// An item that leaves the forge leaves the table: a plan that skips nothing
	// clears the project.
	if err := st.ReplacePlanReasons(ctx, pid, nil); err != nil {
		t.Fatalf("replace 4: %v", err)
	}
	reasons, err = st.PlanReasons(ctx, pid)
	if err != nil {
		t.Fatalf("reasons 4: %v", err)
	}
	if len(reasons) != 0 {
		t.Fatalf("reasons = %+v, want none", reasons)
	}
}

// TestPlanReasonsAreScopedToTheProject: item numbers are unique only within a
// project, so a write for one project must never clear another's rows.
func TestPlanReasonsAreScopedToTheProject(t *testing.T) {
	ctx := context.Background()
	st := upsertStore(t)
	one := planReasonProject(t, st, "app-one")
	two := planReasonProject(t, st, "app-two")
	at := time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC)
	for _, pid := range []int64{one, two} {
		if err := st.ReplacePlanReasons(ctx, pid, []PlanReason{
			{ItemNumber: 5, Reason: "no_free_builder", TickAt: at},
		}); err != nil {
			t.Fatalf("replace %d: %v", pid, err)
		}
	}
	if err := st.ReplacePlanReasons(ctx, one, nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	other, err := st.PlanReasons(ctx, two)
	if err != nil {
		t.Fatalf("reasons: %v", err)
	}
	if len(other) != 1 || other[0].ItemNumber != 5 {
		t.Fatalf("project two = %+v, want its own row untouched", other)
	}
}

// planReasonProject creates one project under key and returns its id.
func planReasonProject(t *testing.T, st *Store, key string) int64 {
	t.Helper()
	in := baseUpsert()
	in.Project.Key = key
	in.Project.Name = key
	_, id, err := st.UpsertProject(context.Background(), in)
	if err != nil {
		t.Fatalf("project %s: %v", key, err)
	}
	return id
}
