package housekeeping

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
)

// fakeGit records the argv of every removal and prune, and answers the branch
// and dirty questions from maps. It is the whole seam Execute and Plan use.
type fakeGit struct {
	removed     []string
	prunes      int
	branches    map[string]string
	dirty       map[string]bool
	removeErr   map[string]error
	dirtyErr    map[string]error
	branchErr   map[string]error
	branchCalls int
}

func (f *fakeGit) Branch(_ context.Context, path string) (string, error) {
	f.branchCalls++
	if err := f.branchErr[path]; err != nil {
		return "", err
	}
	return f.branches[path], nil
}

func (f *fakeGit) Dirty(_ context.Context, path string) (bool, error) {
	if err := f.dirtyErr[path]; err != nil {
		return false, err
	}
	return f.dirty[path], nil
}

func (f *fakeGit) RemoveWorktree(_ context.Context, path string) error {
	if err := f.removeErr[path]; err != nil {
		return err
	}
	f.removed = append(f.removed, path)
	return nil
}

func (f *fakeGit) PruneWorktrees(_ context.Context) error {
	f.prunes++
	return nil
}

// Execute removes exactly the approved candidates, never a kept or skipped one,
// and prunes once.
func TestExecuteRemovesOnlyApproved(t *testing.T) {
	git := &fakeGit{}
	decisions := []Decision{
		{Candidate: Candidate{Item: 1, Path: "/wt/wt-app-1"}, Remove: true},
		{Candidate: Candidate{Item: 2, Path: "/wt/wt-app-2"}, Reason: ReasonDirty},
		{Candidate: Candidate{Item: 3, Path: "/wt/wt-app-3"}, Reason: ReasonSymlink},
		{Candidate: Candidate{Item: 4, Path: "/wt/wt-app-4"}, Remove: true},
	}
	got, err := Execute(context.Background(), decisions, git)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(git.removed) != 2 || git.removed[0] != "/wt/wt-app-1" || git.removed[1] != "/wt/wt-app-4" {
		t.Fatalf("removed = %v, want exactly the two approved paths", git.removed)
	}
	if git.prunes != 1 {
		t.Fatalf("prunes = %d, want 1 after the removals", git.prunes)
	}
	if len(got) != 4 || !got[0].Remove || got[1].Remove || got[2].Remove || !got[3].Remove {
		t.Fatalf("decisions after execute = %+v", got)
	}
}

// Nothing approved means no prune: an empty run does not touch the repo.
func TestExecuteSkipsPruneWhenNothingRemoved(t *testing.T) {
	git := &fakeGit{}
	decisions := []Decision{{Candidate: Candidate{Item: 1, Path: "/wt/wt-app-1"}, Reason: ReasonLiveRow}}
	if _, err := Execute(context.Background(), decisions, git); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(git.removed) != 0 || git.prunes != 0 {
		t.Fatalf("removed=%v prunes=%d, want none and no prune", git.removed, git.prunes)
	}
}

// Git's refusal is a keep carrying git's own words, not a lost removal.
func TestExecuteGitRefusalBecomesAKeep(t *testing.T) {
	git := &fakeGit{removeErr: map[string]error{
		"/wt/wt-app-5": errors.New("git worktree remove: contains modified or untracked files"),
	}}
	decisions := []Decision{{Candidate: Candidate{Item: 5, Path: "/wt/wt-app-5"}, Remove: true}}
	got, err := Execute(context.Background(), decisions, git)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got[0].Remove {
		t.Fatal("a refused removal is still marked Remove")
	}
	if got[0].Reason != ReasonGitRefused {
		t.Fatalf("reason = %q, want %q", got[0].Reason, ReasonGitRefused)
	}
	if !strings.Contains(got[0].Detail, "contains modified") {
		t.Fatalf("detail = %q, want git's own message", got[0].Detail)
	}
	if len(git.removed) != 0 {
		t.Fatalf("removed = %v, want none", git.removed)
	}
}

// A symlink under the root is skipped with a reason and never handed to git; a
// candidate outside the root is refused the same way.
func TestCandidatesSkipSymlinkAndOutsideRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "wt-app-3"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(root, "wt-app-4")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	// A plain directory that is not a worktree name is ignored entirely.
	if err := os.Mkdir(filepath.Join(root, "notes"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	marked := []CleanupPath{
		{Path: filepath.Join(root, "wt-app-3"), Item: 3},
		{Path: filepath.Join(root, "wt-app-4"), Item: 4},
		{Path: filepath.Join(filepath.Dir(root), "wt-app-5"), Item: 5}, // outside the root
	}
	in := Input{
		Project: "app", WorktreeRoot: root, ForgeOK: true,
		Caller: &fakeCaller{marked: marked},
	}
	paths, skips, err := Candidates(context.Background(), in)
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	if len(paths) != 1 || paths[0].Item != 3 {
		t.Fatalf("paths = %+v, want only the in-root wt-app-3", paths)
	}
	reasons := map[Reason]int{}
	for _, skip := range skips {
		reasons[skip.Reason]++
	}
	if reasons[ReasonOutsideRoot] != 1 {
		t.Fatalf("skips = %+v, want one outside-root", skips)
	}

	// The same through Plan: the outside-root path is a skip decision, and the
	// in-root worktree is judged normally.
	result, err := Plan(context.Background(), in)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if got := reasonCount(result.Decisions, ReasonOutsideRoot); got != 1 {
		t.Fatalf("outside-root decisions = %d, want 1 (%+v)", got, result.Decisions)
	}
}

// scanRoot (no caller-marked paths) reports a symlink as ReasonSymlink.
func TestScanRootSkipsSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "wt-app-1"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "wt-app-2")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	paths, skips, err := scanRoot(root, "app")
	if err != nil {
		t.Fatalf("scanRoot: %v", err)
	}
	if len(paths) != 1 || paths[0].Item != 1 {
		t.Fatalf("paths = %+v, want only wt-app-1", paths)
	}
	if len(skips) != 1 || skips[0].Reason != ReasonSymlink {
		t.Fatalf("skips = %+v, want one symlink reason", skips)
	}
	if skips[0].Candidate.Item != 2 {
		t.Fatalf("symlink skip item = %d, want 2", skips[0].Candidate.Item)
	}
}

// The worktree name carries the project key and the item: wt-<key>-<n>, and a
// key containing a dash is still parsed to the right item.
func TestWorktreeItemNames(t *testing.T) {
	tests := []struct {
		project, name string
		want          int
	}{
		{"app", "wt-app-7", 7},
		{"fleet-console", "wt-fleet-console-167", 167},
		{"app", "wt-other-7", 0},
		{"app", "wt-app-x", 0},
		{"app", "notes", 0},
		{"app", "wt-app-0", 0},
	}
	for _, test := range tests {
		if got := worktreeItem(test.project, test.name); got != test.want {
			t.Fatalf("worktreeItem(%q, %q) = %d, want %d", test.project, test.name, got, test.want)
		}
	}
}

// fakeCaller stands in for the app-side facts an older tick does not expose.
// Branch and dirty come from fakeGit, because Plan asks git directly for them.
type fakeCaller struct {
	marked   []CleanupPath
	liveRows []WeightedRow
}

func (f *fakeCaller) CleanupPaths(context.Context) ([]CleanupPath, error) { return f.marked, nil }
func (f *fakeCaller) LiveRows(context.Context) ([]WeightedRow, error)     { return f.liveRows, nil }

func reasonCount(decisions []Decision, reason Reason) int {
	count := 0
	for _, decision := range decisions {
		if decision.Reason == reason {
			count++
		}
	}
	return count
}

// A dirty candidate is kept by Plan, and Execute never sees it as removable, so
// no removal is recorded.
func TestPlanKeepsDirtyAndExecuteDoesNotRemove(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "wt-app-9")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	git := &fakeGit{dirty: map[string]bool{path: true}}
	in := Input{
		Project: "app", WorktreeRoot: root, ForgeOK: true, Git: git,
		Caller: &fakeCaller{
			marked: []CleanupPath{{Path: path, Item: 9, Branch: "feature/9"}},
		},
	}
	result, err := Plan(context.Background(), in)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(result.Decisions) != 1 {
		t.Fatalf("decisions = %+v, want 1", result.Decisions)
	}
	if result.Decisions[0].Remove || result.Decisions[0].Reason != ReasonDirty {
		t.Fatalf("decision = %+v, want a dirty keep", result.Decisions[0])
	}
	if _, err := Execute(context.Background(), result.Decisions, git); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(git.removed) != 0 {
		t.Fatalf("removed = %v, want none for a dirty tree", git.removed)
	}
}

// An open change request's head branch keeps its worktree even though nothing
// else speaks for it, and a closed clean item's worktree is a removal.
func TestPlanOpenCRKeepsClosedRemoves(t *testing.T) {
	root := t.TempDir()
	openPath := filepath.Join(root, "wt-app-20")
	closedPath := filepath.Join(root, "wt-app-21")
	git := &fakeGit{}
	in := Input{
		Project: "app", WorktreeRoot: root, ForgeOK: true, Git: git,
		CRs: []forge.ChangeRequest{
			{Number: 300, State: forge.StateOpen, Branch: "feature/20-open"},
		},
		Caller: &fakeCaller{
			marked: []CleanupPath{
				{Path: openPath, Item: 20, Branch: "feature/20-open"},
				{Path: closedPath, Item: 21, Branch: "feature/21-closed"},
			},
			liveRows: []WeightedRow{
				{Row: rig.QueueRow{State: "done", PickupState: "terminal", Summary: "#21 shipped"}},
			},
		},
	}
	result, err := Plan(context.Background(), in)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	byItem := map[int]Decision{}
	for _, decision := range result.Decisions {
		byItem[decision.Candidate.Item] = decision
	}
	if kept := byItem[20]; kept.Remove || kept.Reason != ReasonOpenCR {
		t.Fatalf("item 20 = %+v, want kept for an open change request", kept)
	}
	if gone := byItem[21]; !gone.Remove {
		t.Fatalf("item 21 = %+v, want removable (closed item, terminal row, clean)", gone)
	}

	got, err := Execute(context.Background(), result.Decisions, git)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(git.removed) != 1 || git.removed[0] != closedPath {
		t.Fatalf("removed = %v, want only %s", git.removed, closedPath)
	}
	if reasonCount(got, ReasonOpenCR) != 1 {
		t.Fatalf("open-cr decisions = %d, want 1", reasonCount(got, ReasonOpenCR))
	}
}

// A stale forge read proposes nothing: no decisions, the forge-stale flag set.
func TestPlanForgeStaleRemovesNothing(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "wt-app-30"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	git := &fakeGit{}
	in := Input{Project: "app", WorktreeRoot: root, ForgeOK: false}
	result, err := Plan(context.Background(), in)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !result.ForgeStale {
		t.Fatal("ForgeStale = false, want true")
	}
	if len(result.Decisions) != 0 {
		t.Fatalf("decisions = %+v, want none on a stale read", result.Decisions)
	}
	if _, err := Execute(context.Background(), result.Decisions, git); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(git.removed) != 0 {
		t.Fatalf("removed = %v, want none", git.removed)
	}
}

// A live dispatch row keeps the worktree: the fact is the app's own table.
func TestPlanLiveDispatchKeeps(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "wt-app-40")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	git := &fakeGit{}
	in := Input{
		Project: "app", WorktreeRoot: root, ForgeOK: true, Git: git,
		LiveItems: []int{40},
		Caller: &fakeCaller{
			marked: []CleanupPath{{Path: path, Item: 40, Branch: "feature/40"}},
		},
	}
	result, err := Plan(context.Background(), in)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(result.Decisions) != 1 || result.Decisions[0].Remove {
		t.Fatalf("decisions = %+v, want one keep", result.Decisions)
	}
	if result.Decisions[0].Reason != ReasonLiveDispatch {
		t.Fatalf("reason = %q, want %q", result.Decisions[0].Reason, ReasonLiveDispatch)
	}
	if _, err := Execute(context.Background(), result.Decisions, git); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(git.removed) != 0 {
		t.Fatalf("removed = %v, want none while a dispatch is live", git.removed)
	}
}

// With no Caller at all, Plan falls back to scanning the root itself: the
// wt-<key>-<n> children are candidates, the symlink is a skip, and the
// unrelated directory is not considered.
func TestPlanWithoutCallerScansRoot(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"wt-app-50", "notes"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "wt-app-51")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	git := &fakeGit{}
	result, err := Plan(context.Background(), Input{Project: "app", WorktreeRoot: root, ForgeOK: true, Git: git})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(result.Decisions) != 2 {
		t.Fatalf("decisions = %+v, want the candidate and the symlink skip", result.Decisions)
	}
	// A skip leads its candidate, so index into the result by item, not by order.
	byItem := map[int]Decision{}
	for _, decision := range result.Decisions {
		byItem[decision.Candidate.Item] = decision
	}
	if candidate := byItem[50]; !candidate.Remove || candidate.Reason != "" {
		t.Fatalf("item 50 = %+v, want wt-app-50 removable", candidate)
	}
	if skip := byItem[51]; skip.Remove || skip.Reason != ReasonSymlink {
		t.Fatalf("item 51 = %+v, want a symlink skip", skip)
	}
}

// ExecGit is the production seam: the argv of a removal is literally
// `git -C <repo> worktree remove <path>` and never carries `--force`. Run
// against a real repository so the assertion is about the bytes git receives.
func TestExecGitRemovesWithoutForce(t *testing.T) {
	repo := t.TempDir()
	worktree := filepath.Join(t.TempDir(), "wt-app-60")
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("commit", "--allow-empty", "-q", "-m", "base")
	run("worktree", "add", "-q", "-b", "feature/60", worktree)

	git := ExecGit{RepoPath: repo}
	if err := git.RemoveWorktree(context.Background(), worktree); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktree, ".git")); err == nil {
		t.Fatalf("worktree %s still present after removal", worktree)
	}
	if err := git.PruneWorktrees(context.Background()); err != nil {
		t.Fatalf("prune: %v", err)
	}
}

// The strongest proof that `--force` is absent: real git refuses to remove a
// worktree with untracked files, and that refusal is exactly what a force would
// bypass. The dirty-tree keep D1 protects depends on this refusal.
func TestExecGitRefusesDirtyWorktreeWithoutForce(t *testing.T) {
	repo := t.TempDir()
	worktree := filepath.Join(t.TempDir(), "wt-app-70")
	runGitTest(t, repo, "init", "-q", "-b", "main")
	runGitTest(t, repo, "commit", "--allow-empty", "-q", "-m", "base")
	runGitTest(t, repo, "worktree", "add", "-q", "-b", "feature/70", worktree)
	if err := os.WriteFile(filepath.Join(worktree, "uncommitted.txt"), []byte("work"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	git := ExecGit{RepoPath: repo}
	if err := git.RemoveWorktree(context.Background(), worktree); err == nil {
		t.Fatal("git removed a worktree with untracked files: --force must not be passed")
	}
	if _, err := os.Stat(filepath.Join(worktree, "uncommitted.txt")); err != nil {
		t.Fatalf("the untracked file was destroyed: %v", err)
	}
}

// runGitTest runs one git command in a test repository with a fixed identity.
func runGitTest(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}
