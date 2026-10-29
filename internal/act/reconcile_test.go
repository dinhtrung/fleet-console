package act

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

func reconcileProject(t *testing.T, st *store.Store, item int, qid string) store.ProjectRow {
	t.Helper()
	pid := newProject(t, st)
	if _, err := st.CreateDispatch(context.Background(), store.NewDispatch{
		ProjectID: pid, ItemNumber: item, Attempt: 1, Seat: "seat@rig",
		Branch: "feature/x", Worktree: "wt", BaseSHA: "base", QItemID: qid,
		CreatedAt: time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	return store.ProjectRow{ID: pid, Key: "app"}
}

func eventCount(t *testing.T, st *store.Store, item int, kind string) int {
	t.Helper()
	events, err := st.EventsForItem(context.Background(), item, 20)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	n := 0
	for _, event := range events {
		if event.Kind == kind {
			n++
		}
	}
	return n
}

// TestReconcileSuperseded: (a) a terminal queue row ends an open dispatch as
// superseded with one event naming the row.
func TestReconcileSuperseded(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	project := reconcileProject(t, st, 5, "qitem-1")
	rows := []rig.QueueRow{{ID: "qitem-1", State: "pending", PickupState: "done"}}

	result, err := Reconcile(ctx, st, project, rows, nil, now)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(result.Changed) != 1 || result.Changed[0] != 5 || result.Reasons[5] != "superseded" {
		t.Fatalf("result = %+v, want #5 superseded", result)
	}
	dispatches, _ := st.DispatchesForProject(ctx, project.ID)
	if len(dispatches) != 1 || dispatches[0].EndedAt == nil || dispatches[0].EndReason != "superseded" {
		t.Fatalf("dispatch = %+v, want ended superseded", dispatches)
	}
	events, _ := st.EventsForItem(ctx, 5, 20)
	found := false
	for _, event := range events {
		if event.Kind == "dispatch.superseded" {
			found = true
			if !strings.Contains(event.Detail, "qitem-1") {
				t.Fatalf("event = %+v, want the row named", event)
			}
		}
	}
	if !found {
		t.Fatalf("events = %+v, want dispatch.superseded", events)
	}

	// The same read again is a no-op: the dispatch is already ended.
	result, err = Reconcile(ctx, st, project, rows, nil, now.Add(time.Minute))
	if err != nil || len(result.Changed) != 0 {
		t.Fatalf("second reconcile = %+v, %v, want no change", result, err)
	}
	if got := eventCount(t, st, 5, "dispatch.superseded"); got != 1 {
		t.Fatalf("superseded events = %d, want one", got)
	}
}

// TestReconcileMerged: (b) a merged CR ends an open dispatch as merged.
func TestReconcileMerged(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	project := reconcileProject(t, st, 6, "qitem-2")
	crs := []forge.ChangeRequest{{Number: 9, ClosesItem: 6, Merged: true}}
	result, err := Reconcile(ctx, st, project, nil, crs, now)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(result.Changed) != 1 || result.Reasons[6] != "merged" {
		t.Fatalf("result = %+v, want #6 merged", result)
	}
	dispatches, _ := st.DispatchesForProject(ctx, project.ID)
	if dispatches[0].EndReason != "merged" || dispatches[0].EndedAt == nil {
		t.Fatalf("dispatch = %+v, want ended merged", dispatches)
	}
	if got := eventCount(t, st, 6, "dispatch.merged"); got != 1 {
		t.Fatalf("merged events = %d, want one", got)
	}
}

// TestReconcileAbandonedAfterAFullTick: (c) one tick marks the missing row, the
// next abandons it with the item and qitem id in the event.
func TestReconcileAbandonedAfterAFullTick(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	project := reconcileProject(t, st, 7, "qitem-3")

	first, err := Reconcile(ctx, st, project, nil, nil, now)
	if err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if len(first.Changed) != 0 {
		t.Fatalf("first = %+v, want the absence recorded only", first)
	}
	if got := eventCount(t, st, 7, "dispatch.abandoned"); got != 0 {
		t.Fatalf("abandoned events = %d after one tick, want 0", got)
	}

	second, err := Reconcile(ctx, st, project, nil, nil, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if len(second.Changed) != 1 || second.Reasons[7] != "abandoned" {
		t.Fatalf("second = %+v, want #7 abandoned", second)
	}
	events, _ := st.EventsForItem(ctx, 7, 20)
	found := false
	for _, event := range events {
		if event.Kind == "dispatch.abandoned" {
			found = true
			if !strings.Contains(event.Detail, "qitem-3") || !strings.Contains(event.Detail, "#7") {
				t.Fatalf("event = %+v, want the item and the missing qitem id", event)
			}
		}
	}
	if !found {
		t.Fatalf("events = %+v, want dispatch.abandoned", events)
	}

	third, err := Reconcile(ctx, st, project, nil, nil, now.Add(2*time.Minute))
	if err != nil || len(third.Changed) != 0 {
		t.Fatalf("third = %+v, %v, want no change", third, err)
	}
}
