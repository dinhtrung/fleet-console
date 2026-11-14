package act

import (
	"context"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/decide"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// planReasonFixture is one project whose plan refuses #5 and #6, with a change
// request on #5 so the recorded head is a real one.
func planReasonFixture(t *testing.T, now time.Time) (*store.Store, int64, TickInput) {
	t.Helper()
	st := newStore(t)
	pid := newProject(t, st)
	queue := &queueStub{item: &rig.QueueItem{ID: "qitem-1", State: "pending"}, created: &rig.CreateRequest{}}
	const head = "aaaa1111111111111111111111111111111a"
	in := TickInput{
		Store: st,
		Project: store.ProjectRow{
			ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", Enabled: true,
		},
		Forge: forge.NewFake(forge.KindGitHub), Rig: queue, Git: &fakeGit{}, Gate: openGate(), Apply: true,
		Plan: decide.Plan{Status: decide.StatusNothing, Skips: []decide.Skip{
			{Number: 5, Reason: decide.ReasonNoFreeBuilder, Detail: "every admitted seat is taken"},
			{Number: 6, Reason: decide.ReasonLiveRow, Detail: "qitem-1"},
		}},
		CRs: []forge.ChangeRequest{{Number: 20, ClosesItem: 5, State: "open", HeadSHA: head, Body: "Closes #5"}},
		Now: func() time.Time { return now },
	}
	return st, pid, in
}

// TestPlanReasonSurvivesTheTick (#428, jobs 4.3): the plan's per-item reason is a
// store row after the tick, keyed with the item's change-request head; the same
// answer on the next tick is one row at the later time, not a second row.
func TestPlanReasonSurvivesTheTick(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC)
	st, pid, in := planReasonFixture(t, now)
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick: %v", err)
	}
	reasons, err := st.PlanReasons(ctx, pid)
	if err != nil {
		t.Fatalf("reasons: %v", err)
	}
	if len(reasons) != 2 {
		t.Fatalf("reasons = %+v, want one row per skipped item", reasons)
	}
	if reasons[0].ItemNumber != 5 || reasons[0].Reason != string(decide.ReasonNoFreeBuilder) ||
		reasons[0].Detail != "every admitted seat is taken" || reasons[0].Head == "" {
		t.Fatalf("#5 = %+v, want the refusal with its head", reasons[0])
	}
	if reasons[1].ItemNumber != 6 || reasons[1].Reason != string(decide.ReasonLiveRow) ||
		reasons[1].Detail != "qitem-1" || reasons[1].Head != "" {
		t.Fatalf("#6 = %+v, want the live row and no head", reasons[1])
	}

	// The next tick answers the same thing: one row per item, at the later time.
	later := now.Add(15 * time.Second)
	in.Now = func() time.Time { return later }
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	reasons, err = st.PlanReasons(ctx, pid)
	if err != nil {
		t.Fatalf("reasons 2: %v", err)
	}
	if len(reasons) != 2 {
		t.Fatalf("reasons after two ticks = %+v, want still one row per item", reasons)
	}
	if !reasons[0].TickAt.Equal(later) {
		t.Fatalf("tick_at = %s, want %s", reasons[0].TickAt, later)
	}
}

// TestPlanReasonIsOverwrittenNotAppended: a changed reason replaces the row. The
// table is the last plan's answer, so the board can never show a reason the last
// plan did not give.
func TestPlanReasonIsOverwrittenNotAppended(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC)
	st, pid, in := planReasonFixture(t, now)
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick: %v", err)
	}
	in.Plan.Skips = []decide.Skip{
		{Number: 5, Reason: decide.ReasonBlocked, Detail: "#7"},
		{Number: 6, Reason: decide.ReasonLiveRow, Detail: "qitem-1"},
	}
	in.Now = func() time.Time { return now.Add(15 * time.Second) }
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	reasons, err := st.PlanReasons(ctx, pid)
	if err != nil {
		t.Fatalf("reasons: %v", err)
	}
	if len(reasons) != 2 {
		t.Fatalf("reasons = %+v, want two rows, not history", reasons)
	}
	if reasons[0].Reason != string(decide.ReasonBlocked) || reasons[0].Detail != "#7" {
		t.Fatalf("#5 = %+v, want the changed reason", reasons[0])
	}
}

// TestPlanReasonGoesWhenTheItemDispatches: the row is deleted in the same tick
// the item is dispatched, because a dispatched item is no longer in any plan
// skip — the table holds the plan's whole answer, not an addition to it.
func TestPlanReasonGoesWhenTheItemDispatches(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC)
	st, pid, in := planReasonFixture(t, now)
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick: %v", err)
	}
	before, err := st.PlanReasons(ctx, pid)
	if err != nil || len(before) != 2 {
		t.Fatalf("reasons = %+v (%v), want two before the dispatch", before, err)
	}

	// #5 is dispatched this tick: the plan names it as an intent and does not
	// skip it, and #6 stays refused.
	in.Items = []forge.Item{{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Five", Body: "body"}}
	// The change request is gone too: the dispatch guard refuses to dispatch an
	// item that already has one, and this test is about the row leaving.
	in.CRs = nil
	in.Plan = decide.Plan{
		Status:  decide.StatusPlanned,
		Intents: []decide.Intent{{Kind: decide.IntentDispatch, Project: "app", Item: 5, Seat: "seat@rig"}},
		Skips:   []decide.Skip{{Number: 6, Reason: decide.ReasonLiveRow, Detail: "qitem-1"}},
	}
	in.WorktreeRoot = t.TempDir()
	in.Now = func() time.Time { return now.Add(15 * time.Second) }
	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	if len(report.Dispatches) != 1 {
		t.Fatalf("dispatches = %+v, want the item dispatched", report.Dispatches)
	}
	reasons, err := st.PlanReasons(ctx, pid)
	if err != nil {
		t.Fatalf("reasons: %v", err)
	}
	if len(reasons) != 1 || reasons[0].ItemNumber != 6 {
		t.Fatalf("reasons = %+v, want #5 gone and #6 kept", reasons)
	}
}

// TestPlanReasonGoesWhenTheItemLeavesTheForge: an item that is closed, or gone
// from the mirror entirely, is in no plan skip, so its row leaves with it.
func TestPlanReasonGoesWhenTheItemLeavesTheForge(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC)
	st, pid, in := planReasonFixture(t, now)
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick: %v", err)
	}
	// #5 closes; #6 is still refused.
	in.Items = []forge.Item{{Number: 5, Kind: forge.KindTask, State: forge.StateClosed, Title: "Five"}}
	in.Plan.Skips = []decide.Skip{{Number: 6, Reason: decide.ReasonLiveRow, Detail: "qitem-1"}}
	in.Now = func() time.Time { return now.Add(15 * time.Second) }
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	reasons, err := st.PlanReasons(ctx, pid)
	if err != nil {
		t.Fatalf("reasons: %v", err)
	}
	if len(reasons) != 1 || reasons[0].ItemNumber != 6 {
		t.Fatalf("reasons = %+v, want #5 gone", reasons)
	}

	// The whole project stops skipping: the table empties, because the last plan
	// had no reason to give.
	in.Plan.Skips = nil
	in.Plan.Status = decide.StatusNothing
	in.Now = func() time.Time { return now.Add(30 * time.Second) }
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick 3: %v", err)
	}
	reasons, err = st.PlanReasons(ctx, pid)
	if err != nil {
		t.Fatalf("reasons 3: %v", err)
	}
	if len(reasons) != 0 {
		t.Fatalf("reasons = %+v, want none", reasons)
	}
}

// TestStalePlanKeepsTheLastReason: a failed forge read planned nothing, and a
// read that failed proves nothing about the item — the last real answer stands.
// This is the same rule that keeps a stale read from vouching for demand.
func TestStalePlanKeepsTheLastReason(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC)
	st, pid, in := planReasonFixture(t, now)
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick: %v", err)
	}
	in.Plan = decide.Plan{Status: decide.StatusStale, Error: "forge read failed"}
	in.Now = func() time.Time { return now.Add(15 * time.Second) }
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	reasons, err := st.PlanReasons(ctx, pid)
	if err != nil {
		t.Fatalf("reasons: %v", err)
	}
	if len(reasons) != 2 {
		t.Fatalf("reasons = %+v, want the last real plan's answer kept", reasons)
	}
	if !reasons[0].TickAt.Equal(now) {
		t.Fatalf("tick_at = %s, want the last real tick %s", reasons[0].TickAt, now)
	}
}

// TestPlanReasonRowsCollapseADuplicateItem: one item, one row. A plan that skips
// the same item twice has a bug, and the table must not turn that into two rows
// — the primary key would refuse the insert and take the whole write with it.
func TestPlanReasonRowsCollapseADuplicateItem(t *testing.T) {
	at := time.Date(2026, 11, 14, 2, 0, 0, 0, time.UTC)
	rows := planReasonRows(nil, decide.Plan{Skips: []decide.Skip{
		{Number: 9, Reason: decide.ReasonNoFreeBuilder, Detail: "first"},
		{Number: 5, Reason: decide.ReasonLiveRow, Detail: "qitem-1"},
		{Number: 9, Reason: decide.ReasonBlocked, Detail: "second"},
	}}, at)
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want one row per item", rows)
	}
	if rows[0].ItemNumber != 5 {
		t.Fatalf("rows[0] = %+v, want the lower item number first", rows[0])
	}
	if rows[1].ItemNumber != 9 || rows[1].Reason != string(decide.ReasonBlocked) || rows[1].Detail != "second" {
		t.Fatalf("#9 = %+v, want the last answer for the item", rows[1])
	}
	if !rows[1].TickAt.Equal(at) {
		t.Fatalf("tick_at = %s, want %s", rows[1].TickAt, at)
	}
}
