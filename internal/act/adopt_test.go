package act

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

func pendingBrief(t *testing.T, dir, title string) string {
	t.Helper()
	path := filepath.Join(dir, "brief.md")
	body := "# app #5: " + title + "\n\nRig: rig\nWorktree: /tmp/wt\nBranch: feature/5-pool\nBase sha: abc\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func pendingDispatch(t *testing.T, st *store.Store, projectID int64, briefPath string) int64 {
	t.Helper()
	id, err := st.CreateDispatch(context.Background(), store.NewDispatch{
		ProjectID: projectID, ItemNumber: 5, Attempt: 1, Seat: "dev-build-1@rig",
		Branch: "feature/5-pool", Worktree: "/tmp/wt", BaseSHA: "abc",
		BriefPath: briefPath, State: "pending",
	})
	if err != nil {
		t.Fatalf("pending dispatch: %v", err)
	}
	return id
}

func TestAdoptPendingStampsTheMatchingRow(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	project, _ := st.ProjectByKey(ctx, "app")
	brief := pendingBrief(t, t.TempDir(), "Pool store")
	dispatchID := pendingDispatch(t, st, pid, brief)
	queue := &queueStub{rows: []rig.QueueRow{{
		ID: "qitem-1", Destination: "dev-build-1@rig", State: "pending", Summary: "#5 Pool store",
	}}}

	result, err := AdoptPending(ctx, st, queue, *project, "")
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if len(result.Adopted) != 1 || result.Adopted[0] != dispatchID || len(result.Abandoned) != 0 {
		t.Fatalf("result = %+v, want adopted", result)
	}
	rows, _ := st.Dispatches(ctx, pid, 5)
	if len(rows) != 1 || rows[0].QItemID != "qitem-1" || rows[0].State != "active" {
		t.Fatalf("dispatch = %+v, want active with the row stamped", rows)
	}
	if queue.createCalls != 0 {
		t.Fatalf("createCalls = %d, adoption must not create a second row", queue.createCalls)
	}
}

func TestAdoptPendingAbandonsWithoutARow(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	project, _ := st.ProjectByKey(ctx, "app")
	dispatchID := pendingDispatch(t, st, pid, pendingBrief(t, t.TempDir(), "Pool store"))
	queue := &queueStub{}

	result, err := AdoptPending(ctx, st, queue, *project, "")
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if len(result.Abandoned) != 1 || result.Abandoned[0] != dispatchID {
		t.Fatalf("result = %+v, want abandoned", result)
	}
	rows, _ := st.Dispatches(ctx, pid, 5)
	if len(rows) != 1 || rows[0].State != "abandoned" || rows[0].EndedAt == nil {
		t.Fatalf("dispatch = %+v, want abandoned and ended", rows)
	}
	if queue.createCalls != 0 {
		t.Fatalf("createCalls = %d, the empty case creates nothing", queue.createCalls)
	}
}

func TestAdoptPendingAmbiguityRaisesOneDecision(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	project, _ := st.ProjectByKey(ctx, "app")
	dispatchID := pendingDispatch(t, st, pid, pendingBrief(t, t.TempDir(), "Pool store"))
	queue := &queueStub{
		created: &rig.CreateRequest{},
		rows: []rig.QueueRow{
			{ID: "qitem-1", Destination: "dev-build-1@rig", State: "pending", Summary: "#5 Pool store"},
			{ID: "qitem-2", Destination: "dev-build-1@rig", State: "in-progress", Summary: "#5 Pool store"},
		},
	}

	result, err := AdoptPending(ctx, st, queue, *project, "")
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if len(result.Ambiguous) != 1 || result.Ambiguous[0] != dispatchID || len(result.Decisions) != 1 {
		t.Fatalf("result = %+v, want abandoned and one decision", result)
	}
	if queue.createCalls != 1 || queue.created.Destination != "human@kernel" {
		t.Fatalf("decision = %+v calls=%d, want one human decision", queue.created, queue.createCalls)
	}
	rows, _ := st.Dispatches(ctx, pid, 5)
	if rows[0].State != "abandoned" {
		t.Fatalf("dispatch = %+v, want abandoned", rows[0])
	}
}

func TestAdoptedDispatchIsNotDispatchedAgain(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	project, _ := st.ProjectByKey(ctx, "app")
	pendingDispatch(t, st, pid, pendingBrief(t, t.TempDir(), "Pool store"))
	queue := &queueStub{rows: []rig.QueueRow{{
		ID: "qitem-1", Destination: "dev-build-1@rig", State: "pending", Summary: "#5 Pool store",
	}}}
	if _, err := AdoptPending(ctx, st, queue, *project, ""); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	actions := &Actions{Store: st, Forge: forge.NewFake(forge.KindGitHub), Rig: queue, Git: &fakeGit{}}
	result, err := actions.Dispatch(ctx, DispatchInput{
		ProjectID: pid, ProjectKey: "app",
		Item:         forge.Item{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Pool store"},
		Seat:         "dev-build-1@rig",
		WorktreeRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !result.NoOp {
		t.Fatalf("dispatch = %+v, want the adopted dispatch reused", result)
	}
	rows, _ := st.Dispatches(ctx, pid, 5)
	if len(rows) != 1 || queue.createCalls != 0 {
		t.Fatalf("rows=%d createCalls=%d, want no second row", len(rows), queue.createCalls)
	}
}

// A pending dispatch is adopted when its row sits on any of the project's rigs,
// not only the first. The row here is on the second rig listed.
func TestAdoptPendingAdoptsASeatOnTheSecondRig(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid, err := st.CreateProject(ctx, store.ProjectInput{
		Key: "app", Name: "app", ForgeKind: "github", ForgeProject: "o/r",
		Rigs: []string{"rig-a", "rig-b"}, ConcurrencyCap: 2, Enabled: true,
	})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	project, err := st.ProjectByKey(ctx, "app")
	if err != nil {
		t.Fatalf("project by key: %v", err)
	}
	brief := pendingBrief(t, t.TempDir(), "Pool store")
	dispatchID, err := st.CreateDispatch(ctx, store.NewDispatch{
		ProjectID: pid, ItemNumber: 5, Attempt: 1, Seat: "dev-build-1@rig-b",
		Branch: "feature/5-pool", Worktree: "/tmp/wt", BaseSHA: "abc",
		BriefPath: brief, State: "pending",
	})
	if err != nil {
		t.Fatalf("pending dispatch: %v", err)
	}
	queue := &queueStub{rows: []rig.QueueRow{{
		ID: "qitem-2", Destination: "dev-build-1@rig-b", State: "pending", Summary: "#5 Pool store",
	}}}

	result, err := AdoptPending(ctx, st, queue, *project, "")
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if len(result.Adopted) != 1 || result.Adopted[0] != dispatchID || len(result.Abandoned) != 0 {
		t.Fatalf("result = %+v, want the rig-b dispatch adopted", result)
	}
	rows, _ := st.Dispatches(ctx, pid, 5)
	if rows[0].State != "active" || rows[0].QItemID != "qitem-2" {
		t.Fatalf("dispatch = %+v, want active with qitem-2 stamped", rows[0])
	}
	if queue.createCalls != 0 {
		t.Fatalf("createCalls = %d, adoption must not create a row", queue.createCalls)
	}
}
