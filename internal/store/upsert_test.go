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
	} else if _, err := st.SetProjectOnHold(ctx, id, true); err != nil {
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
	if len(projects) != 1 || len(projects[0].Rigs) != 2 || projects[0].Rigs[1] != "rig-b" || projects[0].Rig != "rig-a" {
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

// A project that declares a tier or a non-default trigger persists an object form in
// rigs_json. decodeRigs must read all the names back from it: it is what the mirror
// filters rows and seats with, so collapsing to one name silently hides work queued on
// the project's later rigs. This is the round-trip the object form had no test for.
func TestProjectRigTiersRoundTripKeepsEveryName(t *testing.T) {
	ctx := context.Background()
	st := upsertStore(t)
	in := baseUpsert()
	in.Project.Rig = ""
	in.Project.Rigs = []string{"primary"}
	in.Project.RigTiers = []RigTier{
		{Name: "primary", Tier: 0, Spill: "on-full"},
		{Name: "standby", Tier: 1, Spill: "on-unavailable"},
		{Name: "cold", Tier: 2, Spill: "never"},
	}
	if _, id, err := st.UpsertProject(ctx, in); err != nil || id == 0 {
		t.Fatalf("create: %v", err)
	}
	projects, err := st.Projects(ctx)
	if err != nil {
		t.Fatalf("projects: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("projects = %d, want 1", len(projects))
	}
	got := projects[0]
	// The names, in declaration order, must survive the object form.
	want := []string{"primary", "standby", "cold"}
	if len(got.Rigs) != len(want) {
		t.Fatalf("Rigs = %v, want %v: the object form collapsed the list", got.Rigs, want)
	}
	for i, name := range want {
		if got.Rigs[i] != name {
			t.Fatalf("Rigs = %v, want %v in order", got.Rigs, want)
		}
	}
	// The tiers ride along, so the two views of the same column agree.
	if len(got.RigTiers) != len(want) {
		t.Fatalf("RigTiers = %+v, want %d entries", got.RigTiers, len(want))
	}
	if got.RigTiers[1].Tier != 1 || got.RigTiers[1].Spill != "on-unavailable" {
		t.Fatalf("RigTiers[1] = %+v, want tier 1 on-unavailable", got.RigTiers[1])
	}
}

// TestUpsertRelayIsDBOwnedUnlessSynced: the relay is the row's, like onhold
// (#375). A config apply with no block leaves what the board wrote alone; a
// block writes it; a later apply without a block must not erase it.
func TestUpsertRelayIsDBOwnedUnlessSynced(t *testing.T) {
	ctx := context.Background()
	st := upsertStore(t)
	in := baseUpsert()
	if _, _, err := st.UpsertProject(ctx, in); err != nil {
		t.Fatalf("first: %v", err)
	}
	// The board wrote the pair: a config apply with no block must keep it.
	board := in
	board.Project.RelayMode, board.Project.RelaySeat, board.SyncRelay = "operator", "operator-agent@kernel", true
	if _, _, err := st.UpsertProject(ctx, board); err != nil {
		t.Fatalf("board write: %v", err)
	}
	projects, _ := st.Projects(ctx)
	if projects[0].RelayMode != "operator" || projects[0].RelaySeat != "operator-agent@kernel" {
		t.Fatalf("relay = (%q, %q), want the board's pair", projects[0].RelayMode, projects[0].RelaySeat)
	}
	in.Project.Name = "renamed"
	if _, _, err := st.UpsertProject(ctx, in); err != nil {
		t.Fatalf("apply without block: %v", err)
	}
	projects, _ = st.Projects(ctx)
	if projects[0].RelayMode != "operator" || projects[0].RelaySeat != "operator-agent@kernel" {
		t.Fatalf("relay = (%q, %q), a blockless apply erased it", projects[0].RelayMode, projects[0].RelaySeat)
	}
	// An empty block is an explicit off: mode "" with SyncRelay clears the row.
	off := in
	off.SyncRelay = true
	if _, _, err := st.UpsertProject(ctx, off); err != nil {
		t.Fatalf("clear: %v", err)
	}
	projects, _ = st.Projects(ctx)
	if projects[0].RelayMode != "" || projects[0].RelaySeat != "" {
		t.Fatalf("relay = (%q, %q), want cleared by an empty block", projects[0].RelayMode, projects[0].RelaySeat)
	}
}
