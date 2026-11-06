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

// TestRunTickFailedRigUpSkipsTheDispatch is task 3.2: a rig whose launch failed
// skips every dispatch on it, and the tick writes no worktree and no queue row.
func TestRunTickFailedRigUpSkipsTheDispatch(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	git := &fakeGit{}
	queue := &queueStub{item: &rig.QueueItem{ID: "qitem-1", State: "pending"}, created: &rig.CreateRequest{}}
	project := store.ProjectRow{ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", ConcurrencyCap: 2, Enabled: true}
	item := forge.Item{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Pool store", Body: "body"}

	report, err := RunTick(ctx, TickInput{
		Store: st, Project: project, Forge: forge.NewFake(forge.KindGitHub), Rig: queue, Git: git,
		Gate: openGate(), Apply: true,
		Items: []forge.Item{item}, Plan: dispatchPlan(5, "dev-build-1@rig"), WorktreeRoot: t.TempDir(),
		LifecycleFailed: map[string]string{"rig": "no rig spec at /specs/rig/rig.yaml"},
	})
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Dispatches) != 0 {
		t.Fatalf("dispatches = %+v, want none on a failed launch", report.Dispatches)
	}
	if git.worktrees != 0 {
		t.Fatalf("worktrees = %d, want zero", git.worktrees)
	}
	if queue.createCalls != 0 {
		t.Fatalf("queue create calls = %d, want zero", queue.createCalls)
	}
	if len(report.Skipped) != 1 || !strings.Contains(report.Skipped[0], "rig rig up:") {
		t.Fatalf("skips = %v, want one named rig-up failure", report.Skipped)
	}
	rows, err := st.Dispatches(ctx, pid, 5)
	if err != nil {
		t.Fatalf("dispatches: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("dispatch rows = %+v, want none", rows)
	}
}

// TestRunTickFailedRigUpSkipsOnlyThatRig: a failure on one rig does not block a
// dispatch for another.
func TestRunTickFailedRigUpSkipsOnlyThatRig(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	queue := &queueStub{item: &rig.QueueItem{ID: "qitem-1", State: "pending"}, created: &rig.CreateRequest{}}
	project := store.ProjectRow{ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", ConcurrencyCap: 2, Enabled: true}
	item := forge.Item{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Pool store", Body: "body"}

	report, err := RunTick(ctx, TickInput{
		Store: st, Project: project, Forge: forge.NewFake(forge.KindGitHub), Rig: queue, Git: &fakeGit{},
		Gate: openGate(), Apply: true,
		Items: []forge.Item{item}, Plan: dispatchPlan(5, "dev-build-1@healthy"), WorktreeRoot: t.TempDir(),
		LifecycleFailed: map[string]string{"other": "boom"},
	})
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Dispatches) != 1 {
		t.Fatalf("dispatches = %+v, want the healthy rig's dispatch", report.Dispatches)
	}
}

// TestRunTickLifecycleSendsNoMessage pins task 3.4: neither a lifecycle action
// nor the dispatch it gates ever calls Send. The stub records every call.
func TestRunTickLifecycleSendsNoMessage(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	queue := &queueStub{item: &rig.QueueItem{ID: "qitem-1", State: "pending"}, created: &rig.CreateRequest{}}
	project := store.ProjectRow{ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", ConcurrencyCap: 2, Enabled: true}
	item := forge.Item{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Pool store", Body: "body"}

	if _, err := RunTick(ctx, TickInput{
		Store: st, Project: project, Forge: forge.NewFake(forge.KindGitHub), Rig: queue, Git: &fakeGit{},
		Gate: openGate(), Apply: true,
		Items: []forge.Item{item}, Plan: dispatchPlan(5, "dev-build-1@rig"), WorktreeRoot: t.TempDir(),
		LifecycleFailed: map[string]string{"rig": "boom"},
	}); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if queue.sendCalls != 0 {
		t.Fatalf("send calls = %d, want zero for a skipped dispatch", queue.sendCalls)
	}

	// The same for a lifecycle start and stop through the executor.
	executor := &LifecycleExecutor{Store: st, Rigs: &fakeLifecycleRig{}, Logf: func(string, ...any) {}}
	executor.Start(ctx, []LifecycleAction{{Kind: LifecycleStart, Rig: "r", Spec: writeSpec(t), Reason: "demand"}})
	executor.Stop(ctx, []LifecycleAction{{Kind: LifecycleStop, Rig: "r", Quiet: time.Minute}})
	if queue.sendCalls != 0 {
		t.Fatalf("send calls = %d after lifecycle actions, want zero", queue.sendCalls)
	}
}
