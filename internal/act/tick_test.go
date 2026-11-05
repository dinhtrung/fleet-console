package act

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/decide"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

func openGate() Gate {
	return NewGate(rig.Report{OK: true, Status: rig.StatusOK})
}

func dispatchPlan(item int, seat string) decide.Plan {
	return decide.Plan{
		Status: decide.StatusPlanned,
		Intents: []decide.Intent{{
			Kind: decide.IntentDispatch, Project: "app", Item: item, Seat: seat,
		}},
	}
}

func TestRunTickDispatchesOnce(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	git := &fakeGit{}
	fake := forge.NewFake(forge.KindGitHub)
	queue := &queueStub{item: &rig.QueueItem{ID: "qitem-1", State: "pending"}, created: &rig.CreateRequest{}}
	project := store.ProjectRow{ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", ConcurrencyCap: 2, Enabled: true}
	item := forge.Item{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Pool store", Body: "body"}
	in := TickInput{
		Store: st, Project: project, Forge: fake, Rig: queue, Git: git, Gate: openGate(), Apply: true,
		Items: []forge.Item{item}, Plan: dispatchPlan(5, "seat@rig"), WorktreeRoot: t.TempDir(),
	}

	first, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("first tick: %v", err)
	}
	if len(first.Dispatches) != 1 || first.Dispatches[0].QItemID != "qitem-1" {
		t.Fatalf("first = %+v, want one dispatch", first)
	}
	if queue.createCalls != 1 || !hasWrite(fake.Writes, "marker", 5, "on") {
		t.Fatalf("writes = %d %+v", queue.createCalls, fake.Writes)
	}
	rows, _ := st.Dispatches(ctx, pid, 5)
	if len(rows) != 1 || rows[0].QItemID != "qitem-1" {
		t.Fatalf("dispatch rows = %+v", rows)
	}

	second, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if len(second.Dispatches) != 0 {
		t.Fatalf("second = %+v, want no new dispatch", second)
	}
	if queue.createCalls != 1 || git.worktrees != 1 {
		t.Fatalf("second tick wrote again: create=%d worktrees=%d", queue.createCalls, git.worktrees)
	}
	if len(second.Skipped) == 0 || !strings.Contains(second.Skipped[0], "already dispatched") {
		t.Fatalf("skipped = %v, want the existing row named", second.Skipped)
	}
}

func TestRunTickMergeWaitsForAccept(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	st := newStore(t)
	pid := newProject(t, st)
	dispatchID, err := st.CreateDispatch(ctx, store.NewDispatch{
		ProjectID: pid, ItemNumber: 5, Attempt: 1, Seat: "seat@rig",
		Branch: "feature/5-x", Worktree: "wt", BaseSHA: "base", CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	fake := forge.NewFake(forge.KindGitHub)
	fake.ItemsValue = []forge.Item{{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Five"}}
	fake.RequestsValue = []forge.ChangeRequest{{
		Number: 9, HeadSHA: "abc1234", Mergeable: true, Body: "Closes #5", DocsOnly: true,
	}}
	project := store.ProjectRow{ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", ConcurrencyCap: 2, Enabled: true}
	in := TickInput{
		Store: st, Project: project, Forge: fake, Rig: &queueStub{}, Git: &fakeGit{},
		Gate: openGate(), Apply: true, Items: fake.ItemsValue, CRs: fake.RequestsValue,
		Now: func() time.Time { return now },
	}

	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick without a verdict: %v", err)
	}
	if len(report.Merges) != 0 || !hasSkip(report, "merge waiting") {
		t.Fatalf("report = %+v, want the merge refused while waiting for ACCEPT", report)
	}
	if hasWrite(fake.Writes, "merge", 9, "") {
		t.Fatalf("merged without a verdict: %+v", fake.Writes)
	}

	fake.NotesValue = []forge.Comment{{
		ID: "n1", Body: "ACCEPT abc1234", CreatedAt: "2026-10-29T11:00:00Z",
	}}
	report, err = RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick with a verdict: %v", err)
	}
	if len(report.Merges) != 1 || !report.Merges[0].Merged {
		t.Fatalf("report = %+v, want one merge", report)
	}
	if !hasWrite(fake.Writes, "merge", 9, "abc1234") {
		t.Fatalf("writes = %+v, want the merge at the verdict's sha", fake.Writes)
	}
	dispatch, err := st.Dispatch(ctx, dispatchID)
	if err != nil || dispatch == nil || dispatch.EndedAt == nil || dispatch.EndReason != "merged" {
		t.Fatalf("dispatch = %+v, %v, want ended merged", dispatch, err)
	}
}

func TestRunTickBlockedGateWritesNothing(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	queue := &queueStub{item: &rig.QueueItem{ID: "qitem-1"}, created: &rig.CreateRequest{}}
	fake := forge.NewFake(forge.KindGitHub)
	project := store.ProjectRow{ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", Enabled: true}
	gate := NewGate(rig.Report{OK: false, Status: rig.StatusBlocked, Error: "queue list: required field missing"})
	report, err := RunTick(ctx, TickInput{
		Store: st, Project: project, Forge: fake, Rig: queue, Git: &fakeGit{}, Gate: gate, Apply: true,
		Items: []forge.Item{{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Five"}},
		Plan:  dispatchPlan(5, "seat@rig"), WorktreeRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if !report.Blocked || report.Reason == "" {
		t.Fatalf("report = %+v, want blocked with a reason", report)
	}
	if queue.createCalls != 0 || len(fake.Writes) != 0 {
		t.Fatalf("blocked gate wrote: create=%d writes=%+v", queue.createCalls, fake.Writes)
	}
	events, _ := st.RecentEvents(ctx, 5)
	if len(events) != 0 {
		t.Fatalf("blocked gate wrote events: %+v", events)
	}
}

func TestRunTickDryRunWritesNothing(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	queue := &queueStub{item: &rig.QueueItem{ID: "qitem-1"}, created: &rig.CreateRequest{}}
	fake := forge.NewFake(forge.KindGitHub)
	project := store.ProjectRow{ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", Enabled: true}
	report, err := RunTick(ctx, TickInput{
		Store: st, Project: project, Forge: fake, Rig: queue, Git: &fakeGit{}, Gate: openGate(), Apply: false,
		Items: []forge.Item{{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Five"}},
		Plan:  dispatchPlan(5, "seat@rig"), WorktreeRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if report.Applied || queue.createCalls != 0 || len(fake.Writes) != 0 {
		t.Fatalf("dry run wrote: %+v create=%d writes=%+v", report, queue.createCalls, fake.Writes)
	}
}

func hasSkip(report TickReport, needle string) bool {
	for _, skipped := range report.Skipped {
		if strings.Contains(skipped, needle) {
			return true
		}
	}
	return false
}

// TestRunTickRaisesOneStaleVerdictDecision: D2 — one reviewer row per (item, head),
// never a loop, and no merge on the stale verdict.
func TestRunTickRaisesOneStaleVerdictDecision(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	st := newStore(t)
	pid := newProject(t, st)
	dispatchID, err := st.CreateDispatch(ctx, store.NewDispatch{
		ProjectID: pid, ItemNumber: 5, Attempt: 1, Seat: "seat@rig",
		Branch: "feature/5-x", Worktree: "wt", BaseSHA: "base", CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if _, err := st.AddVerdict(ctx, store.NewVerdict{
		DispatchID: dispatchID, SHA: "oldsha1", Decision: "accept", Source: "note 1", At: now,
	}); err != nil {
		t.Fatalf("verdict: %v", err)
	}
	queue := &queueStub{item: &rig.QueueItem{ID: "qitem-review-1", State: "pending"}}
	fake := forge.NewFake(forge.KindGitHub)
	fake.RequestsValue = []forge.ChangeRequest{{
		Number: 9, HeadSHA: "newsha1", Mergeable: true, Body: "Closes #5", DocsOnly: true,
	}}
	// The plan is computed by the same decide pass the server runs: a stale
	// verdict produces a decision.stale intent, which the tick must execute once.
	plan := decide.PlanTick(decide.Snapshot{
		Project:  decide.Project{Key: "app", Forge: decide.ForgeGitHub, Enabled: true},
		Items:    []decide.Item{{Number: 5, Kind: decide.KindTask, State: decide.StateOpen, Title: "Five"}},
		CRs:      []decide.CRState{{Number: 9, Item: 5, State: "open", HeadSHA: "newsha1"}},
		Verdicts: []decide.VerdictState{{Item: 5, SHA: "oldsha1", Decision: "accept"}},
		ForgeOK:  true,
	}, now)
	var raise *decide.Intent
	for index := range plan.Intents {
		if plan.Intents[index].Kind == decide.IntentRaiseDecision {
			raise = &plan.Intents[index]
		}
	}
	if raise == nil || raise.Event.Kind != "decision.stale" {
		t.Fatalf("plan = %+v, want a decision.stale intent", plan)
	}
	project := store.ProjectRow{ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", Enabled: true}
	in := TickInput{
		Store: st, Project: project, Forge: fake, Rig: queue, Git: &fakeGit{},
		Gate: openGate(), Apply: true, Items: fake.ItemsValue, CRs: fake.RequestsValue,
		Plan: plan, WorktreeRoot: t.TempDir(), Now: func() time.Time { return now },
	}

	first, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("first tick: %v", err)
	}
	if len(first.Decisions) != 1 || first.Decisions[0] != "qitem-review-1" {
		t.Fatalf("first = %+v, want one reviewer row", first)
	}
	if hasWrite(fake.Writes, "merge", 9, "") {
		t.Fatalf("merged on a stale verdict: %+v", fake.Writes)
	}

	second, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if len(second.Decisions) != 0 {
		t.Fatalf("second = %+v, want no second row", second)
	}
	if queue.createCalls != 1 {
		t.Fatalf("queue creates = %d, want one per (item, head)", queue.createCalls)
	}
	events, _ := st.EventsForItem(ctx, 5, 20)
	stale := 0
	for _, event := range events {
		if event.Kind == "decision.stale" {
			stale++
		}
	}
	if stale != 1 {
		t.Fatalf("decision.stale events = %d, want one", stale)
	}
}

// TestRunTickReadsVerdictsFromTheChangeRequest is the boundary test for #90.
//
// A verdict is posted on the change request, and on GitHub a pull request's
// number is not the number of the issue it closes. The tick used to hand the
// item's number to the verdict reader, which read the issue's conversation,
// found no verdict, and left the merge refused — with a green pipeline and an
// ACCEPT sitting one stream away. So the assertion is about the number, not
// about the classification: CaptureVerdict's own tests cannot see this.
func TestRunTickReadsVerdictsFromTheChangeRequest(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 30, 17, 0, 0, 0, time.UTC)
	st := newStore(t)
	pid := newProject(t, st)

	const itemNumber, changeRequestNumber = 63, 75
	if _, err := st.CreateDispatch(ctx, store.NewDispatch{
		ProjectID: pid, ItemNumber: itemNumber, Attempt: 1, Seat: "seat@rig",
		Branch: "feature/63-x", Worktree: "wt", BaseSHA: "base", CreatedAt: now,
	}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	fake := forge.NewFake(forge.KindGitHub)
	fake.ItemsValue = []forge.Item{{Number: itemNumber, Kind: forge.KindTask, State: forge.StateOpen, Title: "Sixty-three"}}
	fake.RequestsValue = []forge.ChangeRequest{{
		Number: changeRequestNumber, HeadSHA: "d00f0dd", Mergeable: true,
		Body: "Closes #63", DocsOnly: true,
	}}
	fake.NotesValue = []forge.Comment{{
		ID: "n1", Body: "ACCEPT d00f0dd", CreatedAt: "2026-10-30T16:00:00Z",
	}}

	project := store.ProjectRow{ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", ConcurrencyCap: 2, Enabled: true}
	report, err := RunTick(ctx, TickInput{
		Store: st, Project: project, Forge: fake, Rig: &queueStub{}, Git: &fakeGit{},
		Gate: openGate(), Apply: true, Items: fake.ItemsValue, CRs: fake.RequestsValue,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("tick: %v", err)
	}

	// The gate must read the change request's conversation...
	if want := []int{changeRequestNumber}; len(fake.CRNotesAsked) != 1 || fake.CRNotesAsked[0] != want[0] {
		t.Fatalf("CRNotes asked about %v, want %v", fake.CRNotesAsked, want)
	}
	// ...and must not read the item's, which is a different conversation.
	if len(fake.NotesAsked) != 0 {
		t.Fatalf("Notes asked about %v, want no item read for a verdict", fake.NotesAsked)
	}
	// The verdict is therefore visible, and the merge lands at the verdict's sha.
	if len(report.Verdicts) != 1 {
		t.Fatalf("verdicts = %+v, want one captured", report.Verdicts)
	}
	if len(report.Merges) != 1 || !report.Merges[0].Merged {
		t.Fatalf("merges = %+v, want one merged", report.Merges)
	}
	if !hasWrite(fake.Writes, "merge", changeRequestNumber, "d00f0dd") {
		t.Fatalf("writes = %+v, want the merge at the verdict's sha", fake.Writes)
	}
}

// TestCaptureFromForgeRefusesNoChangeRequest pins the guard: a caller with no
// change request cannot be given a verdict, and must not silently read item 0's
// conversation (which is also nobody's conversation).
func TestCaptureFromForgeRefusesNoChangeRequest(t *testing.T) {
	fake := forge.NewFake(forge.KindGitHub)
	a := &Actions{Store: newStore(t), Forge: fake}
	if _, err := a.CaptureFromForge(context.Background(), 1, 63, 0, "d00f0dd"); err == nil {
		t.Fatal("CaptureFromForge accepted change request 0")
	}
	if len(fake.CRNotesAsked) != 0 || len(fake.NotesAsked) != 0 {
		t.Fatalf("read %v / %v, want no read when the number is unusable", fake.CRNotesAsked, fake.NotesAsked)
	}
}

// TestRunTickNotifiesEachMergeAndNoDecisionAlone puts the trigger where it
// belongs: the tick reports what it raised and what it merged, and nothing else.
// The phone is the caller's business, so what is asserted here is the seam the
// caller hooks (task 3.1, 3.3).
func TestRunTickNotifiesEachMergeAndNoDecisionAlone(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 30, 18, 0, 0, 0, time.UTC)

	// A tick that merges nothing raises nothing.
	t.Run("nothing raised, nothing merged: no callbacks", func(t *testing.T) {
		st := newStore(t)
		pid := newProject(t, st)
		fake := forge.NewFake(forge.KindGitHub)
		fake.ItemsValue = []forge.Item{{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Five"}}
		var raised []DecisionRaised
		_, err := RunTick(ctx, TickInput{
			Store: st, Project: store.ProjectRow{ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", ConcurrencyCap: 2, Enabled: true},
			Forge: fake, Rig: &queueStub{}, Git: &fakeGit{}, Gate: openGate(), Apply: true,
			Items: fake.ItemsValue, Now: func() time.Time { return now },
			OnDecision: func(d DecisionRaised) { raised = append(raised, d) },
		})
		if err != nil {
			t.Fatalf("tick: %v", err)
		}
		if len(raised) != 0 {
			t.Fatalf("raised = %+v, want none", raised)
		}
	})

	// A raised decision reaches the hook exactly once, with the row's id.
	t.Run("a raised decision is reported once", func(t *testing.T) {
		st := newStore(t)
		pid := newProject(t, st)
		fake := forge.NewFake(forge.KindGitHub)
		fake.ItemsValue = []forge.Item{{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Five"}}
		var raised []DecisionRaised
		plan := decide.Plan{Status: decide.StatusPlanned, Intents: []decide.Intent{{
			Kind: decide.IntentRaiseDecision, Project: "app", Item: 5, Text: "re-review needed",
			Event: decide.Event{Kind: "review.stale"},
		}}}
		_, err := RunTick(ctx, TickInput{
			Store: st, Project: store.ProjectRow{ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", ConcurrencyCap: 2, Enabled: true},
			Forge: fake, Rig: &queueStub{}, Git: &fakeGit{}, Gate: openGate(), Apply: true,
			Items: fake.ItemsValue, Plan: plan, WorktreeRoot: t.TempDir(), Now: func() time.Time { return now },
			OnDecision: func(d DecisionRaised) { raised = append(raised, d) },
		})
		if err != nil {
			t.Fatalf("tick: %v", err)
		}
		if len(raised) != 1 {
			t.Fatalf("raised = %+v, want one", raised)
		}
		if raised[0].Item != 5 || raised[0].Text != "re-review needed" || raised[0].QItemID == "" {
			t.Fatalf("raised[0] = %+v, want item 5, the plan's text and a row id", raised[0])
		}
	})
}

// A rejected attempt leaves its pull request closed. A closed pull request can
// never merge again, so it must not keep the item out of the dispatch queue: the
// item is free work for the next builder.
func TestRunTickDispatchesAfterRejectedCRClosed(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	st := newStore(t)
	pid := newProject(t, st)
	if _, err := st.CreateDispatch(ctx, store.NewDispatch{
		ProjectID: pid, ItemNumber: 5, Attempt: 1, Seat: "seat@rig",
		Branch: "feature/5-x", Worktree: "wt", BaseSHA: "base", CreatedAt: now,
	}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	rows, err := st.Dispatches(ctx, pid, 5)
	if err != nil || len(rows) != 1 {
		t.Fatalf("dispatch rows = %+v err=%v", rows, err)
	}
	if err := st.EndDispatch(ctx, rows[0].ID, "superseded"); err != nil {
		t.Fatalf("end dispatch: %v", err)
	}

	fake := forge.NewFake(forge.KindGitHub)
	fake.ItemsValue = []forge.Item{{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Five"}}
	// The rejected attempt's pull request is closed; it must not block attempt 2.
	fake.RequestsValue = []forge.ChangeRequest{{
		Number: 9, HeadSHA: "abc1234", State: "closed", Body: "Closes #5",
	}}
	queue := &queueStub{item: &rig.QueueItem{ID: "qitem-2", State: "pending"}, created: &rig.CreateRequest{}}
	project := store.ProjectRow{ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", ConcurrencyCap: 2, Enabled: true}
	in := TickInput{
		Store: st, Project: project, Forge: fake, Rig: queue, Git: &fakeGit{}, Gate: openGate(), Apply: true,
		Items: fake.ItemsValue, CRs: fake.RequestsValue, Plan: dispatchPlan(5, "seat@rig"),
		WorktreeRoot: t.TempDir(), Now: func() time.Time { return now },
	}
	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Dispatches) != 1 {
		t.Fatalf("report = %+v, want one dispatch after the closed CR", report)
	}
	if hasSkip(report, "already has a change request") {
		t.Fatalf("closed CR still blocked the dispatch: %v", report.Skipped)
	}
}
