package store

import (
	"context"
	"testing"
	"time"
)

// TestSetProjectOnHoldSurvivesUpsertWithoutSync: onhold is DB-owned (design
// D5). Setting it reports the change; a config upsert without SyncOnHold leaves
// it alone; with SyncOnHold the input value wins, which is the override the
// board promises.
func TestSetProjectOnHoldSurvivesUpsertWithoutSync(t *testing.T) {
	ctx := context.Background()
	st := upsertStore(t)
	in := baseUpsert()
	_, id, err := st.UpsertProject(ctx, in)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	changed, err := st.SetProjectOnHold(ctx, id, true)
	if err != nil || !changed {
		t.Fatalf("set onhold = %v, %v; want changed", changed, err)
	}

	in.Project.Name = "renamed"
	if result, _, err := st.UpsertProject(ctx, in); err != nil || result != UpsertUpdated {
		t.Fatalf("upsert = %s, %v; want updated", result, err)
	}
	projects, err := st.Projects(ctx)
	if err != nil || len(projects) != 1 {
		t.Fatalf("projects = %+v, %v", projects, err)
	}
	if !projects[0].OnHold {
		t.Fatalf("a config update without SyncOnHold cleared the pause: %+v", projects[0])
	}

	in.SyncOnHold = true
	in.Project.OnHold = false
	if _, _, err := st.UpsertProject(ctx, in); err != nil {
		t.Fatalf("synced upsert: %v", err)
	}
	projects, err = st.Projects(ctx)
	if err != nil {
		t.Fatalf("projects: %v", err)
	}
	if projects[0].OnHold {
		t.Fatalf("SyncOnHold with onhold false did not resume the project: %+v", projects[0])
	}
}

// TestProjectCounts: two projects with different row counts, answered by one
// query. The live dispatch is the smallest open dispatch id — an ended one
// with a smaller id does not count — and a verdict joined through no dispatch
// (an adoption) is not counted, because the delete cascade does not remove it.
func TestProjectCounts(t *testing.T) {
	ctx := context.Background()
	st := upsertStore(t)
	now := time.Now().UTC()

	first, err := st.CreateProject(ctx, ProjectInput{Key: "app", Name: "app", ForgeKind: "github",
		ForgeProject: "o/r", Rig: "rig", ConcurrencyCap: 2, Enabled: true})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	second, err := st.CreateProject(ctx, ProjectInput{Key: "other", Name: "other", ForgeKind: "github",
		ForgeProject: "o/other", Rig: "rig", ConcurrencyCap: 2, Enabled: true})
	if err != nil {
		t.Fatalf("create other: %v", err)
	}
	third, err := st.CreateProject(ctx, ProjectInput{Key: "empty", Name: "empty", ForgeKind: "github",
		ForgeProject: "o/empty", Rig: "rig", ConcurrencyCap: 2, Enabled: true})
	if err != nil {
		t.Fatalf("create empty: %v", err)
	}

	// first: two items, three dispatches — one ended, two open — one verdict on
	// the ended dispatch, one adopted verdict with no dispatch, one snapshot.
	records := []ItemRecord{
		{Number: 1, Kind: "task", State: "open", Title: "One", UpdatedAt: now},
		{Number: 2, Kind: "task", State: "open", Title: "Two", UpdatedAt: now},
	}
	if err := st.ReplaceItems(ctx, first, records); err != nil {
		t.Fatalf("replace items: %v", err)
	}
	ended, err := st.CreateDispatch(ctx, NewDispatch{ProjectID: first, ItemNumber: 1, Attempt: 1,
		Seat: "dev-build@rig", Branch: "b1", Worktree: "w1", BaseSHA: "s1"})
	if err != nil {
		t.Fatalf("create ended dispatch: %v", err)
	}
	if err := st.EndDispatch(ctx, ended, "merged"); err != nil {
		t.Fatalf("end dispatch: %v", err)
	}
	open1, err := st.CreateDispatch(ctx, NewDispatch{ProjectID: first, ItemNumber: 2, Attempt: 1,
		Seat: "dev-build@rig", Branch: "b2", Worktree: "w2", BaseSHA: "s2"})
	if err != nil {
		t.Fatalf("create open dispatch: %v", err)
	}
	if _, err := st.CreateDispatch(ctx, NewDispatch{ProjectID: first, ItemNumber: 2, Attempt: 2,
		Seat: "dev-build@rig", Branch: "b3", Worktree: "w3", BaseSHA: "s3"}); err != nil {
		t.Fatalf("create second open dispatch: %v", err)
	}
	if _, err := st.AddVerdict(ctx, NewVerdict{DispatchID: ended, ProjectID: first, ItemNumber: 1,
		SHA: "aaaa111", Decision: "accept", Source: "board"}); err != nil {
		t.Fatalf("add dispatch verdict: %v", err)
	}
	if _, err := st.AddVerdict(ctx, NewVerdict{ProjectID: first, ItemNumber: 2,
		SHA: "bbbb222", Decision: "accept", Source: "adoption"}); err != nil {
		t.Fatalf("add adopted verdict: %v", err)
	}
	if err := st.PutSnapshot(ctx, first, "board", map[string]string{"state": "ok"}, now); err != nil {
		t.Fatalf("put snapshot: %v", err)
	}

	// second: one item, nothing else.
	if err := st.ReplaceItems(ctx, second, records[:1]); err != nil {
		t.Fatalf("replace items: %v", err)
	}

	counts, err := st.ProjectCounts(ctx)
	if err != nil {
		t.Fatalf("project counts: %v", err)
	}
	want := ProjectCount{Items: 2, Dispatches: 3, Verdicts: 1, Snapshots: 1, LiveDispatch: open1}
	if got := counts[first]; got != want {
		t.Fatalf("app counts = %+v, want %+v", got, want)
	}
	if got := counts[second]; got.Items != 1 || got.Dispatches != 0 || got.Verdicts != 0 ||
		got.Snapshots != 0 || got.LiveDispatch != 0 {
		t.Fatalf("other counts = %+v, want one item and zeros", got)
	}
	if _, ok := counts[third]; ok {
		t.Fatalf("the empty project appears with %+v; the caller treats a missing key as zeros", counts[third])
	}
	if _, ok := counts[9999]; ok {
		t.Fatalf("an unknown id appears in the counts: %+v", counts[9999])
	}
}

// TestClearItems: the same statement ReplaceItems opens with, callable on its
// own, and harmless when there is nothing to clear.
func TestClearItems(t *testing.T) {
	ctx := context.Background()
	st := upsertStore(t)
	now := time.Now().UTC()

	id, err := st.CreateProject(ctx, ProjectInput{Key: "app", Name: "app", ForgeKind: "github",
		ForgeProject: "o/r", Rig: "rig", ConcurrencyCap: 2, Enabled: true})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	records := []ItemRecord{
		{Number: 1, Kind: "task", State: "open", Title: "One", UpdatedAt: now},
		{Number: 2, Kind: "task", State: "open", Title: "Two", UpdatedAt: now},
	}
	if err := st.ReplaceItems(ctx, id, records); err != nil {
		t.Fatalf("replace items: %v", err)
	}
	items, err := st.Items(ctx)
	if err != nil || len(items) != 2 {
		t.Fatalf("items before clear = %d, %v; want 2", len(items), err)
	}

	if err := st.ClearItems(ctx, id); err != nil {
		t.Fatalf("clear items: %v", err)
	}
	items, err = st.Items(ctx)
	if err != nil || len(items) != 0 {
		t.Fatalf("items after clear = %d, %v; want 0", len(items), err)
	}

	if err := st.ClearItems(ctx, id); err != nil {
		t.Fatalf("clear on an empty project: %v", err)
	}
}

// TestReplaceItemsSkipsDeletedProject: a tick that read the project row before
// a delete landed writes nothing for the deleted id — no error, no orphan rows
// — and leaves every other project's items alone (design D4).
func TestReplaceItemsSkipsDeletedProject(t *testing.T) {
	ctx := context.Background()
	st := upsertStore(t)
	now := time.Now().UTC()

	doomed, err := st.CreateProject(ctx, ProjectInput{Key: "doomed", Name: "doomed", ForgeKind: "github",
		ForgeProject: "o/doomed", Rig: "rig", ConcurrencyCap: 2, Enabled: true})
	if err != nil {
		t.Fatalf("create doomed: %v", err)
	}
	survivor, err := st.CreateProject(ctx, ProjectInput{Key: "survivor", Name: "survivor", ForgeKind: "github",
		ForgeProject: "o/survivor", Rig: "rig", ConcurrencyCap: 2, Enabled: true})
	if err != nil {
		t.Fatalf("create survivor: %v", err)
	}
	record := func() ItemRecord {
		return ItemRecord{Number: 1, Kind: "task", State: "open", Title: "One", UpdatedAt: now}
	}
	if err := st.ReplaceItems(ctx, doomed, []ItemRecord{record()}); err != nil {
		t.Fatalf("first replace: %v", err)
	}
	if err := st.ReplaceItems(ctx, survivor, []ItemRecord{record()}); err != nil {
		t.Fatalf("survivor replace: %v", err)
	}

	if err := st.DeleteProject(ctx, doomed); err != nil {
		t.Fatalf("delete project: %v", err)
	}
	// The tick's insert runs after the delete landed.
	if err := st.ReplaceItems(ctx, doomed, []ItemRecord{record(), {Number: 2, Kind: "task",
		State: "open", Title: "Two", UpdatedAt: now}}); err != nil {
		t.Fatalf("replace items for a deleted project = %v; want a silent no-op", err)
	}
	items, err := st.Items(ctx)
	if err != nil {
		t.Fatalf("items: %v", err)
	}
	for _, item := range items {
		if item.ProjectID == doomed {
			t.Fatalf("an orphan row survived: %+v", item)
		}
	}
	if len(items) != 1 || items[0].ProjectID != survivor {
		t.Fatalf("items = %+v, want exactly the survivor's row", items)
	}
}
