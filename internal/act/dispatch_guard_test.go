package act

import (
	"context"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// TestRunTickRefusesDispatchWhenAChangeRequestAlreadyExists pins the guard that is
// not decide's: decide skips an item only when its branch name encodes the number
// (branch_open_cr), so a change request on a differently named branch reaches the
// dispatch loop as a ready item and would be handed to a seat a second time. It also
// counts in Result.Ready, so without this guard a lifecycle start fires for work that
// already has a change request. Remove the guard and this test fails.
func TestRunTickRefusesDispatchWhenAChangeRequestAlreadyExists(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	git := &fakeGit{}
	queue := &queueStub{item: &rig.QueueItem{ID: "qitem-1", State: "pending"}, created: &rig.CreateRequest{}}
	project := store.ProjectRow{
		ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r",
		Rig: "rig", ConcurrencyCap: 2, Enabled: true,
	}
	item := forge.Item{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Pool store", Body: "body"}

	report, err := RunTick(ctx, TickInput{
		Store: st, Project: project, Forge: forge.NewFake(forge.KindGitHub), Rig: queue, Git: git,
		Gate: openGate(), Apply: true,
		Items: []forge.Item{item},
		// A change request that closes #5 but whose branch name does not encode it:
		// decide's branch rule cannot see this one.
		CRs:  []forge.ChangeRequest{{Number: 9, State: "open", Branch: "topic/pool-store", Body: "Closes #5"}},
		Plan: dispatchPlan(5, "dev-build-1@rig"), WorktreeRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Dispatches) != 0 {
		t.Fatalf("dispatches = %+v, want none: the item already has a change request", report.Dispatches)
	}
	if !hasSkip(report, "already has a change request") {
		t.Fatalf("skips = %v, want the change-request refusal named", report.Skipped)
	}
	if git.worktrees != 0 {
		t.Fatalf("worktrees = %d, want zero", git.worktrees)
	}
	if queue.createCalls != 0 {
		t.Fatalf("queue create calls = %d, want zero", queue.createCalls)
	}
	rows, err := st.Dispatches(ctx, pid, 5)
	if err != nil {
		t.Fatalf("dispatches: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("dispatch rows = %+v, want none", rows)
	}
}
