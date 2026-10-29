package store

import (
	"context"
	"path/filepath"
	"testing"
)

func upsertStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(context.Background(), filepath.Join(t.TempDir(), "fleet.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func baseUpsert() UpsertInput {
	return UpsertInput{
		Project: ProjectInput{
			Key: "app", Name: "app", ForgeKind: "github", ForgeProject: "o/r",
			DefaultBranch: "main", Rig: "rig", RigTemplate: "default", ConcurrencyCap: 2, Enabled: true,
		},
		LabelsJSON: `{"area":"backend"}`, RepoKey: "main",
		RepoPath: "/srv/app", WorktreeRoot: "/srv/worktrees",
	}
}

func TestUpsertProjectCreatedThenUnchanged(t *testing.T) {
	ctx := context.Background()
	st := upsertStore(t)
	in := baseUpsert()
	result, id, err := st.UpsertProject(ctx, in)
	if err != nil || result != UpsertCreated || id == 0 {
		t.Fatalf("first = %s %d %v", result, id, err)
	}
	result, again, err := st.UpsertProject(ctx, in)
	if err != nil || result != UpsertUnchanged || again != id {
		t.Fatalf("second = %s %d %v, want unchanged", result, again, err)
	}
	projects, _ := st.Projects(ctx)
	if len(projects) != 1 || projects[0].LabelsJSON != `{"area":"backend"}` {
		t.Fatalf("projects = %+v", projects)
	}
	path, _ := st.RepoPath(ctx, id)
	if path != "/srv/app" {
		t.Fatalf("repo path = %q", path)
	}
}

func TestUpsertPreservesOnHoldUnlessSynced(t *testing.T) {
	ctx := context.Background()
	st := upsertStore(t)
	in := baseUpsert()
	if _, id, err := st.UpsertProject(ctx, in); err != nil {
		t.Fatalf("first: %v", err)
	} else if err := st.SetProjectOnHold(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	in.Project.Name = "renamed"
	result, _, err := st.UpsertProject(ctx, in)
	if err != nil || result != UpsertUpdated {
		t.Fatalf("update = %s %v", result, err)
	}
	projects, _ := st.Projects(ctx)
	if !projects[0].OnHold {
		t.Fatal("a config update silently resumed the project")
	}
	off := false
	in.Project.OnHold = off
	in.SyncOnHold = true
	if _, _, err := st.UpsertProject(ctx, in); err != nil {
		t.Fatal(err)
	}
	projects, _ = st.Projects(ctx)
	if projects[0].OnHold {
		t.Fatal("--sync-onhold did not copy onhold")
	}
}

func TestUpsertUpdatesRepoAndKeepsRemovedProjects(t *testing.T) {
	ctx := context.Background()
	st := upsertStore(t)
	first := baseUpsert()
	if _, id, err := st.UpsertProject(ctx, first); err != nil || id == 0 {
		t.Fatalf("first: %v", err)
	}
	second := baseUpsert()
	second.Project.Key = "docs"
	second.Project.ForgeProject = "o/docs"
	second.RepoPath = "/srv/docs"
	if _, _, err := st.UpsertProject(ctx, second); err != nil {
		t.Fatalf("second: %v", err)
	}
	first.RepoPath = "/srv/app-moved"
	if result, _, err := st.UpsertProject(ctx, first); err != nil || result != UpsertUpdated {
		t.Fatalf("repo update = %s %v", result, err)
	}
	projects, _ := st.Projects(ctx)
	if len(projects) != 2 {
		t.Fatalf("projects = %+v; a missing config entry must not delete", projects)
	}
	firstID := projects[0].ID
	if projects[0].Key == "docs" {
		firstID = projects[1].ID
	}
	if path, _ := st.RepoPath(ctx, firstID); path != "/srv/app-moved" {
		t.Fatalf("repo path = %q", path)
	}
}
