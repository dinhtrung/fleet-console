package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/config"
	"github.com/dinhtrung/fleet-console/internal/store"
)

func registryStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "fleet.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func registryProjects() []config.Project {
	return []config.Project{
		{Key: "a", Name: "A", Forge: "github", Repo: "o/a", Rig: "a", ConcurrencyCap: 1},
		{Key: "b", Name: "B", Forge: "github", Repo: "o/b", Rig: "b", ConcurrencyCap: 1},
	}
}

func projectByKey(t *testing.T, st *store.Store, key string) *store.ProjectRow {
	t.Helper()
	row, err := st.ProjectByKey(context.Background(), key)
	if err != nil {
		t.Fatalf("project %s: %v", key, err)
	}
	return row
}

func TestApplyDisablesAbsentAndReenables(t *testing.T) {
	ctx := context.Background()
	st := registryStore(t)
	projects := registryProjects()
	if _, err := applyProjects(ctx, st, projects, false, true); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	before := projectByKey(t, st, "b")
	if before == nil || !before.Enabled {
		t.Fatalf("b = %+v, want enabled", before)
	}
	if err := st.SetProjectOnHold(ctx, before.ID, true); err != nil {
		t.Fatalf("onhold: %v", err)
	}

	applied, err := applyProjects(ctx, st, projects[:1], false, true)
	if err != nil {
		t.Fatalf("apply after removal: %v", err)
	}
	if len(applied) == 0 || applied[len(applied)-1].Result != "disabled" || applied[len(applied)-1].Key != "b" {
		t.Fatalf("applied = %+v, want a disabled b line", applied)
	}
	disabled := projectByKey(t, st, "b")
	if disabled == nil || disabled.Enabled {
		t.Fatalf("b = %+v, want disabled", disabled)
	}
	if disabled.ID != before.ID || !disabled.OnHold {
		t.Fatalf("b = %+v, want the same id %d and onhold kept", disabled, before.ID)
	}

	applied, err = applyProjects(ctx, st, projects, false, true)
	if err != nil {
		t.Fatalf("re-add apply: %v", err)
	}
	reenabled := projectByKey(t, st, "b")
	if reenabled == nil || !reenabled.Enabled || reenabled.ID != before.ID || !reenabled.OnHold {
		t.Fatalf("b = %+v, want enabled, same id, onhold kept", reenabled)
	}
	if len(applied) == 0 || applied[len(applied)-1].Key != "b" || applied[len(applied)-1].Result != store.UpsertUpdated {
		t.Fatalf("applied = %+v, want b updated", applied)
	}
}

func TestPruneRefusesLiveDispatchThenDeletes(t *testing.T) {
	ctx := context.Background()
	st := registryStore(t)
	projects := registryProjects()
	if _, err := applyProjects(ctx, st, projects, false, true); err != nil {
		t.Fatalf("apply: %v", err)
	}
	b := projectByKey(t, st, "b")
	dispatchID, err := st.CreateDispatch(ctx, store.NewDispatch{
		ProjectID: b.ID, ItemNumber: 7, Attempt: 1, Seat: "builder", Branch: "feature/7",
		Worktree: "/tmp/wt", BaseSHA: "base",
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := st.ReplaceItems(ctx, b.ID, []store.ItemRecord{{
		Number: 7, Kind: "task", State: "open", Title: "Seven", URL: "u", UpdatedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatalf("items: %v", err)
	}

	_, err = pruneProjects(ctx, st, projects[:1])
	if err == nil || !strings.Contains(err.Error(), fmt.Sprint(dispatchID)) {
		t.Fatalf("prune error = %v, want it to name dispatch %d", err, dispatchID)
	}
	if projectByKey(t, st, "b") == nil {
		t.Fatal("prune deleted b despite the live dispatch")
	}

	if err := st.EndDispatch(ctx, dispatchID, "merged"); err != nil {
		t.Fatalf("end dispatch: %v", err)
	}
	pruned, err := pruneProjects(ctx, st, projects[:1])
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if len(pruned) != 1 || pruned[0] != "b" {
		t.Fatalf("pruned = %v, want [b]", pruned)
	}
	if projectByKey(t, st, "b") != nil {
		t.Fatal("b survived prune")
	}
	if projectByKey(t, st, "a") == nil {
		t.Fatal("prune deleted the configured project a")
	}
	items, err := st.Items(ctx)
	if err != nil {
		t.Fatalf("items: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("items = %+v, want b's items deleted with the project", items)
	}
}
