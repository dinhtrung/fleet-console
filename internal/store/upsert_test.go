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

func TestProjectRigsRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := upsertStore(t)
	in := baseUpsert()
	in.Project.Rig = ""
	in.Project.Rigs = []string{"rig-a", "rig-b"}
	if _, id, err := st.UpsertProject(ctx, in); err != nil || id == 0 {
		t.Fatalf("create: %v", err)
	}
	projects, _ := st.Projects(ctx)
	if len(projects) != 1 || len(projects[0].Rigs) != 2 || projects[0].Rigs[0] != "rig-a" ||
		projects[0].Rigs[1] != "rig-b" || projects[0].Rig != "rig-a" {
		t.Fatalf("project = %+v", projects)
	}
	in.Project.Rigs = []string{"rig-a"}
	if result, _, err := st.UpsertProject(ctx, in); err != nil || result != UpsertUpdated {
		t.Fatalf("update = %s %v", result, err)
	}
	projects, _ = st.Projects(ctx)
	if len(projects[0].Rigs) != 1 || projects[0].Rigs[0] != "rig-a" {
		t.Fatalf("rigs after update = %+v", projects[0].Rigs)
	}
}

// TestSchemaV3BackfillsRigsFromTheLegacyColumn: a database written before the
// list column keeps its rig, as the one-element list.
func TestSchemaV3BackfillsRigsFromTheLegacyColumn(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := st.CreateProject(ctx, ProjectInput{
		Key: "old", Name: "old", ForgeKind: "github", ForgeProject: "o/old",
		Rig: "old-rig", Enabled: true,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Rewind to schemaV2 and drop the column, as a pre-D5 database looked.
	if _, err := st.db.ExecContext(ctx, `ALTER TABLE project DROP COLUMN rigs_json`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `PRAGMA user_version = 2`); err != nil {
		t.Fatalf("rewind: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	projects, err := reopened.Projects(ctx)
	if err != nil {
		t.Fatalf("projects: %v", err)
	}
	if len(projects) != 1 || len(projects[0].Rigs) != 1 || projects[0].Rigs[0] != "old-rig" {
		t.Fatalf("rigs = %+v, want the legacy rig backfilled", projects)
	}
}
