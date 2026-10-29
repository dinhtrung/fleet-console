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
