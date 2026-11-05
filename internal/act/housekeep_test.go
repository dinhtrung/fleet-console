package act

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/housekeeping"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// hkGit is a worktree seam that answers per path and records what it removed.
type hkGit struct {
	branches map[string]string
	dirty    map[string]bool
	refuse   map[string]error
	removed  []string
	prunes   int
}

func (g *hkGit) Branch(_ context.Context, path string) (string, error) {
	if err, ok := g.refuse[path]; ok {
		return "", err
	}
	return g.branches[path], nil
}

func (g *hkGit) Dirty(_ context.Context, path string) (bool, error) {
	return g.dirty[path], nil
}

func (g *hkGit) RemoveWorktree(_ context.Context, path string) error {
	if err, ok := g.refuse[path]; ok {
		return err
	}
	g.removed = append(g.removed, path)
	return nil
}

func (g *hkGit) PruneWorktrees(context.Context) error { g.prunes++; return nil }

// hkRoot makes a worktree root with the named directories inside it.
func hkRoot(t *testing.T, names ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range names {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	return root
}

// events reads the audit trail the run wrote, newest last.
func hkEvents(t *testing.T, st *store.Store, kind string) []store.EventRow {
	t.Helper()
	page, err := st.Events(context.Background(), store.EventFilter{Kind: kind, Limit: 100})
	if err != nil {
		t.Fatalf("events %s: %v", kind, err)
	}
	return page.Events
}

// The dry run prints the plan and changes nothing: no directory moves and no
// audit event, because nothing happened.
func TestHousekeepDryRunChangesNothing(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	root := hkRoot(t, "wt-app-9")
	path := filepath.Join(root, "wt-app-9")
	git := &hkGit{branches: map[string]string{path: "feature/9-x"}}

	report, err := Housekeep(ctx, HousekeepInput{
		Store: st, Project: store.ProjectRow{ID: pid, Key: "app"},
		ForgeOK: func() bool { return true }, WorktreeRoot: root, Git: git, Apply: false,
	})
	if err != nil {
		t.Fatalf("housekeep: %v", err)
	}
	if len(report.Removed) != 1 {
		t.Fatalf("removed = %v, want the one candidate listed", report.Removed)
	}
	if len(git.removed) != 0 {
		t.Fatalf("a dry run removed %v", git.removed)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the directory is gone after a dry run: %v", err)
	}
	if rows := hkEvents(t, st, EventHousekeepingRemove); len(rows) != 0 {
		t.Fatalf("a dry run wrote %d remove events", len(rows))
	}
	if rows := hkEvents(t, st, EventHousekeepingSummary); len(rows) != 0 {
		t.Fatalf("a dry run wrote %d summary events", len(rows))
	}
}

// An applied run removes the candidate and writes one remove event plus one
// summary carrying the counts. The event rows are the evidence the task asks to
// quote, so this asserts their actual fields.
func TestHousekeepApplyWritesRemoveAndSummaryEvents(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	root := hkRoot(t, "wt-app-9")
	path := filepath.Join(root, "wt-app-9")
	git := &hkGit{branches: map[string]string{path: "feature/9-x"}}

	report, err := Housekeep(ctx, HousekeepInput{
		Store: st, Project: store.ProjectRow{ID: pid, Key: "app"},
		ForgeOK: func() bool { return true }, WorktreeRoot: root, Git: git, Apply: true,
	})
	if err != nil {
		t.Fatalf("housekeep: %v", err)
	}
	if len(report.Removed) != 1 {
		t.Fatalf("removed = %v, want one", report.Removed)
	}
	if len(git.removed) != 1 {
		t.Fatalf("git removed %v, want one", git.removed)
	}
	if git.prunes != 1 {
		t.Fatalf("prunes = %d, want 1", git.prunes)
	}

	removes := hkEvents(t, st, EventHousekeepingRemove)
	if len(removes) != 1 {
		t.Fatalf("remove events = %d, want 1", len(removes))
	}
	removal := removes[0]
	if removal.Actor != "housekeeping" {
		t.Errorf("actor = %q, want housekeeping", removal.Actor)
	}
	if !strings.Contains(removal.Subject, "9") {
		t.Errorf("subject = %q, want it to name item 9", removal.Subject)
	}
	if !strings.Contains(removal.Detail, "removed") || !strings.Contains(removal.Detail, "feature/9-x") {
		t.Errorf("detail = %q, want the removal and its branch", removal.Detail)
	}
	if removal.ExitCode != nil {
		t.Errorf("a successful removal carries exit_code %d", *removal.ExitCode)
	}

	summaries := hkEvents(t, st, EventHousekeepingSummary)
	if len(summaries) != 1 {
		t.Fatalf("summary events = %d, want 1", len(summaries))
	}
	if !strings.Contains(summaries[0].Detail, "removed=1") {
		t.Errorf("summary detail = %q, want removed=1", summaries[0].Detail)
	}
}

// The ordering proof design D5 rests on: an item dispatched on this tick has a
// live dispatch row by the time the sweep looks, so its worktree is kept. This
// drives the real RunTick first, then the sweep, against the same store.
func TestHousekeepKeepsWorktreeOfAnItemDispatchedThisTick(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	root := hkRoot(t, "wt-app-5")
	path := filepath.Join(root, "wt-app-5")
	git := &fakeGit{}
	queue := &queueStub{item: &rig.QueueItem{ID: "qitem-5", State: "pending"}, created: &rig.CreateRequest{}}
	project := store.ProjectRow{ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", ConcurrencyCap: 2, Enabled: true}
	item := forge.Item{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Dispatched now", Body: "body"}

	// The dispatch phase runs first and creates the live row.
	report, err := RunTick(ctx, TickInput{
		Store: st, Project: project, Forge: forge.NewFake(forge.KindGitHub), Rig: queue, Git: git,
		Gate: openGate(), Apply: true,
		Items: []forge.Item{item}, Plan: dispatchPlan(5, "dev-build-1@rig"), WorktreeRoot: root,
	})
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Dispatches) != 1 {
		t.Fatalf("dispatches = %+v, want the one dispatch", report.Dispatches)
	}

	// The sweep runs after it. The row now exists, so the worktree must be kept.
	hkgit := &hkGit{branches: map[string]string{path: "feature/5-x"}}
	dispatches, err := st.DispatchesForProject(ctx, pid)
	if err != nil {
		t.Fatalf("dispatches: %v", err)
	}
	live := make([]int, 0, len(dispatches))
	for _, dispatch := range dispatches {
		if dispatch.EndedAt == nil {
			live = append(live, dispatch.ItemNumber)
		}
	}
	if len(live) != 1 {
		t.Fatalf("live items = %v, want the dispatch this tick created", live)
	}
	sweep, err := Housekeep(ctx, HousekeepInput{
		Store: st, Project: project, ForgeOK: func() bool { return true },
		Items: []forge.Item{item}, LiveItems: live, WorktreeRoot: root, Git: hkgit, Apply: true,
	})
	if err != nil {
		t.Fatalf("housekeep: %v", err)
	}
	if len(sweep.Removed) != 0 {
		t.Fatalf("the sweep removed %v; a live dispatch must keep its worktree", sweep.Removed)
	}
	if sweep.Kept[housekeeping.ReasonLiveDispatch] != 1 {
		t.Fatalf("kept reasons = %v, want one live-dispatch keep", sweep.Kept)
	}
	if len(hkgit.removed) != 0 {
		t.Fatalf("git removed %v despite a live dispatch", hkgit.removed)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the worktree is gone: %v", err)
	}
}

// A failed forge read proposes nothing: "the read failed" is not "nothing is
// left", so no worktree is deleted and the summary records the staleness.
func TestHousekeepForgeStaleRemovesNothing(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	root := hkRoot(t, "wt-app-9")
	path := filepath.Join(root, "wt-app-9")
	git := &hkGit{branches: map[string]string{path: "feature/9-x"}}

	report, err := Housekeep(ctx, HousekeepInput{
		Store: st, Project: store.ProjectRow{ID: pid, Key: "app"},
		ForgeOK: func() bool { return false }, WorktreeRoot: root, Git: git, Apply: true,
	})
	if err != nil {
		t.Fatalf("housekeep: %v", err)
	}
	if !report.ForgeStale {
		t.Fatal("forge_stale = false, want true")
	}
	if len(report.Removed) != 0 || len(git.removed) != 0 {
		t.Fatalf("a stale forge read removed %v", git.removed)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the worktree is gone after a stale read: %v", err)
	}
	if rows := hkEvents(t, st, EventHousekeepingRemove); len(rows) != 0 {
		t.Fatalf("a stale read wrote %d remove events", len(rows))
	}
	summaries := hkEvents(t, st, EventHousekeepingSummary)
	if len(summaries) != 1 {
		t.Fatalf("summary events = %d, want 1", len(summaries))
	}
	if !strings.Contains(summaries[0].Detail, "forge-stale=1") {
		t.Errorf("summary = %q, want it to record the stale read", summaries[0].Detail)
	}
}

// A git refusal keeps the worktree, carries the error on its event, and is
// counted as failed rather than removed.
func TestHousekeepGitRefusalCarriesErrorAndCountsFailed(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	root := hkRoot(t, "wt-app-9")
	path := filepath.Join(root, "wt-app-9")
	git := &hkGit{
		branches: map[string]string{path: "feature/9-x"},
		refuse:   map[string]error{path: errors.New("contains modified or untracked files, use --force")},
	}

	report, err := Housekeep(ctx, HousekeepInput{
		Store: st, Project: store.ProjectRow{ID: pid, Key: "app"},
		ForgeOK: func() bool { return true }, WorktreeRoot: root, Git: git, Apply: true,
	})
	if err != nil {
		t.Fatalf("housekeep: %v", err)
	}
	if len(report.Removed) != 0 {
		t.Fatalf("removed = %v, want none after a refusal", report.Removed)
	}
	if report.Failed != 1 {
		t.Fatalf("failed = %d, want 1", report.Failed)
	}
	removes := hkEvents(t, st, EventHousekeepingRemove)
	if len(removes) != 1 {
		t.Fatalf("remove events = %d, want 1 carrying the refusal", len(removes))
	}
	if !strings.Contains(removes[0].Detail, "--force") {
		t.Errorf("detail = %q, want git's own refusal words", removes[0].Detail)
	}
	if removes[0].ExitCode == nil || *removes[0].ExitCode == 0 {
		t.Error("a failed removal carries no non-zero exit code")
	}
	summaries := hkEvents(t, st, EventHousekeepingSummary)
	if !strings.Contains(summaries[0].Detail, "failed=1") {
		t.Errorf("summary = %q, want failed=1", summaries[0].Detail)
	}
	if !strings.Contains(summaries[0].Detail, "removed=0") {
		t.Errorf("summary = %q, want removed=0; a refusal is not a removal", summaries[0].Detail)
	}
}

// Nothing runs before the first interval elapses, and a zero interval never runs.
// This is design D6: a restart loop must not become a delete loop.
func TestSchedulerNothingRunsBeforeTheInterval(t *testing.T) {
	start := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

	t.Run("off by default", func(t *testing.T) {
		s := NewScheduler(0, start)
		if s.Due(start) {
			t.Fatal("a zero interval ran")
		}
		if s.Due(start.Add(24 * time.Hour)) {
			t.Fatal("a zero interval ran a day later")
		}
	})

	t.Run("waits one full interval", func(t *testing.T) {
		s := NewScheduler(6*time.Hour, start)
		if s.Due(start.Add(2 * time.Minute)) {
			t.Fatal("it ran two minutes after boot; the first run must wait six hours")
		}
		if s.Due(start.Add(6*time.Hour - time.Second)) {
			t.Fatal("it ran one second early")
		}
		if !s.Due(start.Add(6 * time.Hour)) {
			t.Fatal("it did not run at the interval")
		}
	})

	t.Run("interval restarts after a run", func(t *testing.T) {
		s := NewScheduler(time.Hour, start)
		if !s.Due(start.Add(time.Hour)) {
			t.Fatal("first run did not happen")
		}
		if s.Due(start.Add(time.Hour + 30*time.Minute)) {
			t.Fatal("it ran 30 minutes after the last run; the interval restarts")
		}
		if !s.Due(start.Add(2 * time.Hour)) {
			t.Fatal("it did not run one hour after the last run")
		}
	})
}

// The summary is written even when nothing was removed: "the sweep ran and found
// nothing" is a fact, and its absence would be indistinguishable from "it never
// ran".
func TestHousekeepEmptySweepStillSummarises(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	root := hkRoot(t)
	git := &hkGit{}

	report, err := Housekeep(ctx, HousekeepInput{
		Store: st, Project: store.ProjectRow{ID: pid, Key: "app"},
		ForgeOK: func() bool { return true }, WorktreeRoot: root, Git: git, Apply: true,
	})
	if err != nil {
		t.Fatalf("housekeep: %v", err)
	}
	if len(report.Removed) != 0 {
		t.Fatalf("removed = %v, want none", report.Removed)
	}
	summaries := hkEvents(t, st, EventHousekeepingSummary)
	if len(summaries) != 1 {
		t.Fatalf("summary events = %d, want 1 even with nothing removed", len(summaries))
	}
	if !strings.Contains(summaries[0].Detail, "removed=0") {
		t.Errorf("summary = %q, want removed=0", summaries[0].Detail)
	}
}
