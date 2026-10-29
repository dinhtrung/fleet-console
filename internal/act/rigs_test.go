package act

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

func TestDispatchBriefNamesTheRig(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	queue := &queueStub{item: &rig.QueueItem{ID: "qitem-1", State: "pending"}, created: &rig.CreateRequest{}}
	actions := &Actions{Store: st, Forge: forge.NewFake(forge.KindGitHub), Rig: queue, Git: &fakeGit{}}
	result, err := actions.Dispatch(ctx, DispatchInput{
		ProjectID: pid, ProjectKey: "app",
		Item:         forge.Item{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Pool", Body: "body"},
		Seat:         "dev-build-1@rig-b",
		WorktreeRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	body, err := os.ReadFile(result.BriefPath)
	if err != nil {
		t.Fatalf("read brief: %v", err)
	}
	if !strings.Contains(string(body), "Rig: rig-b") {
		t.Fatalf("brief does not name the building rig:\n%s", body)
	}
	// The row goes to the seat, not to a shared reviewer.
	if queue.created.Destination != "dev-build-1@rig-b" {
		t.Fatalf("destination = %q, want the building seat", queue.created.Destination)
	}
}

func TestReviewGoesToTheBuildingRigsReviewer(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	if _, err := st.CreateDispatch(ctx, store.NewDispatch{
		ProjectID: pid, ItemNumber: 5, Attempt: 1, Seat: "dev-build-1@rig-b",
		Branch: "b", Worktree: "w", BaseSHA: "base",
	}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	queue := &queueStub{item: &rig.QueueItem{ID: "qitem-2", State: "pending"}, created: &rig.CreateRequest{}}
	actions := &Actions{Store: st, Rig: queue}
	project := store.ProjectRow{ID: pid, Key: "app", Rigs: []string{"rig-a", "rig-b"}}
	result, err := actions.RaiseDecision(ctx, project, 5, "head moved", "decision.stale", t.TempDir())
	if err != nil {
		t.Fatalf("raise: %v", err)
	}
	if result.NoOp || queue.created == nil || queue.created.Destination != "dev-review@rig-b" {
		t.Fatalf("destination = %+v, want dev-review@rig-b", queue.created)
	}
	body, err := os.ReadFile(queue.created.BodyFile)
	if err != nil {
		t.Fatalf("read review brief: %v", err)
	}
	if !strings.Contains(string(body), "Rig: rig-b") {
		t.Fatalf("review brief does not name the rig:\n%s", body)
	}
}

// TestReviewFallsBackToTheFirstConfiguredRig: an item with no dispatch yet has
// no building rig, so the project's first rig names the reviewer instead of a
// shared one.
func TestReviewFallsBackToTheFirstConfiguredRig(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	queue := &queueStub{item: &rig.QueueItem{ID: "qitem-3", State: "pending"}, created: &rig.CreateRequest{}}
	actions := &Actions{Store: st, Rig: queue}
	project := store.ProjectRow{ID: pid, Key: "app", Rigs: []string{"rig-a", "rig-b"}}
	if _, err := actions.RaiseDecision(ctx, project, 6, "no dispatch", "decision.stale", t.TempDir()); err != nil {
		t.Fatalf("raise: %v", err)
	}
	if queue.created.Destination != "dev-review@rig-a" {
		t.Fatalf("destination = %q, want dev-review@rig-a", queue.created.Destination)
	}
}
