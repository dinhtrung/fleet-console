package store

import (
	"context"
	"testing"
	"time"
)

func TestLiveDispatch(t *testing.T) {
	ctx := context.Background()
	st := upsertStore(t)
	_, id, err := st.UpsertProject(ctx, baseUpsert())
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	dispatchID, err := st.CreateDispatch(ctx, NewDispatch{
		ProjectID: id, ItemNumber: 7, Attempt: 1, Seat: "s", Branch: "b", Worktree: "w", BaseSHA: "base",
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	got, live, err := st.LiveDispatch(ctx, id)
	if err != nil || !live || got != dispatchID {
		t.Fatalf("live = %d %v, want %d true", got, err, dispatchID)
	}
	if err := st.EndDispatch(ctx, dispatchID, "merged"); err != nil {
		t.Fatalf("end: %v", err)
	}
	if _, live, err := st.LiveDispatch(ctx, id); err != nil || live {
		t.Fatalf("after end live=%v err=%v, want false", live, err)
	}
}

func TestDeleteProjectRemovesAppRows(t *testing.T) {
	ctx := context.Background()
	st := upsertStore(t)
	in := baseUpsert()
	_, id, err := st.UpsertProject(ctx, in)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if err := st.ReplaceItems(ctx, id, []ItemRecord{{
		Number: 7, Kind: "task", State: "open", Title: "Seven", URL: "u", UpdatedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatalf("items: %v", err)
	}
	dispatchID, err := st.CreateDispatch(ctx, NewDispatch{
		ProjectID: id, ItemNumber: 7, Attempt: 1, Seat: "s", Branch: "b", Worktree: "w", BaseSHA: "base",
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if _, err := st.AddVerdict(ctx, NewVerdict{
		DispatchID: dispatchID, SHA: "abc", Decision: "accept", Source: "review",
	}); err != nil {
		t.Fatalf("verdict: %v", err)
	}
	if err := st.PutSnapshot(ctx, id, "forge", map[string]string{"items": "1"}, time.Now().UTC()); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if err := st.ReplacePlanReasons(ctx, id, []PlanReason{{
		ProjectID: id, ItemNumber: 7, Reason: "no_free_builder", Detail: "every admitted seat is taken",
		TickAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatalf("plan reasons: %v", err)
	}
	if err := st.DeleteProject(ctx, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	projects, err := st.Projects(ctx)
	if err != nil || len(projects) != 0 {
		t.Fatalf("projects = %+v, %v", projects, err)
	}
	if items, err := st.Items(ctx); err != nil || len(items) != 0 {
		t.Fatalf("items = %+v, %v", items, err)
	}
	if dispatches, err := st.DispatchesForProject(ctx, id); err != nil || len(dispatches) != 0 {
		t.Fatalf("dispatches = %+v, %v", dispatches, err)
	}
	if verdicts, err := st.VerdictsForProject(ctx, id); err != nil || len(verdicts) != 0 {
		t.Fatalf("verdicts = %+v, %v", verdicts, err)
	}
	if payload, at, err := st.GetSnapshot(ctx, id, "forge"); err != nil || payload != nil || !at.IsZero() {
		t.Fatalf("forge snapshot = %s, %v, %v; want deleted", payload, at, err)
	}
	if path, err := st.RepoPath(ctx, id); err != nil || path != "" {
		t.Fatalf("repo path = %q, %v", path, err)
	}
	if reasons, err := st.PlanReasons(ctx, id); err != nil || len(reasons) != 0 {
		t.Fatalf("plan reasons = %+v, %v; want deleted with the project", reasons, err)
	}

	// project.id has no AUTOINCREMENT, so SQLite hands the freed rowid to the
	// next project. A family left behind would be inherited by a project that
	// never wrote it — the reasons of a deleted project showing on a new one.
	_, again, err := st.UpsertProject(ctx, baseUpsert())
	if err != nil {
		t.Fatalf("second project: %v", err)
	}
	if again != id {
		t.Logf("second project got id %d, not the freed %d; the inheritance guard is untested here", again, id)
	}
	if reasons, err := st.PlanReasons(ctx, again); err != nil || len(reasons) != 0 {
		t.Fatalf("recreated project %d inherited plan reasons = %+v, %v", again, reasons, err)
	}
}
