package housekeeping

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
)

// Input is everything one project's housekeeping pass decides from, using the
// facts the tick has already read. ForgeOK false means the change-request read
// failed: an empty board from a broken read is not an empty board, so no
// removal is even proposed.
type Input struct {
	Project      string // project key, for the log line
	WorktreeRoot string // paths.worktrees: only its direct children are candidates
	ForgeOK      bool
	Items        []forge.Item          // the plan's own item read
	CRs          []forge.ChangeRequest // open change requests
	QueueRows    []rig.QueueRow        // the rig's queue rows
	LiveItems    []int                 // items with a live dispatch row
	// Git answers the two per-worktree questions (branch, dirty). It is
	// required for a removal: a candidate whose branch cannot be read is a
	// keep, because the open-change-request condition cannot be checked.
	Git Git
	// Caller compensates for what an older tick does not expose (cleanupPaths,
	// liveRows). It is optional and never overrides Git.
	Caller Caller
}

// Caller is the slice of the tick that housekeeping reads but an older tick does
// not carry: the pending tree removals and the live queue rows. Each method may
// be nil, in which case the corresponding fact is absent (no marked paths, no
// extra rows) — never silently the opposite.
type Caller interface {
	CleanupPaths(ctx context.Context) ([]CleanupPath, error)
	LiveRows(ctx context.Context) ([]WeightedRow, error)
}

// CleanupPath is one worktree the dispatch phase has already marked for removal.
type CleanupPath struct {
	Path   string
	Item   int
	Branch string
}

// WeightedRow is a queue row plus the item numbers its body and summary name.
// The tick reads the row; parsing the numbers out of it is what decides whether
// the row still speaks for an item.
type WeightedRow struct {
	Row         rig.QueueRow
	ItemNumbers []int
}

// Path is one candidate path found under the worktree root.
type Path struct {
	Path   string
	Item   int
	Branch string
}

// Result is one project's plan: the decisions and whether the forge read failed.
type Result struct {
	Project    string
	ForgeStale bool
	Decisions  []Decision
}

// Plan builds the candidate set and decides each one. It is the read-only half
// a dry run prints; Execute is the half that removes. A stale forge read is not
// an empty board: it returns no decisions at all, so nothing is even proposed.
func Plan(ctx context.Context, in Input) (Result, error) {
	result := Result{Project: in.Project}
	if !in.ForgeOK {
		result.ForgeStale = true
		return result, nil
	}

	paths, skips, err := Candidates(ctx, in)
	if err != nil {
		return result, err
	}

	openCRs := openCRBranches(in.CRs)
	liveItems := make(map[int]bool, len(in.LiveItems))
	for _, item := range in.LiveItems {
		liveItems[item] = true
	}
	liveRows := liveRowItems(in.QueueRows)
	if in.Caller != nil {
		if weighted, err := in.Caller.LiveRows(ctx); err == nil {
			for _, row := range weighted {
				for _, item := range row.ItemNumbers {
					liveRows[item] = true
				}
			}
		}
	}

	candidates := make([]Candidate, 0, len(paths))
	var extraSkips []Decision
	for _, path := range paths {
		branch := path.Branch
		if branch == "" && in.Git != nil {
			name, err := in.Git.Branch(ctx, path.Path)
			if err != nil {
				// The branch decides whether an open change request protects this
				// worktree. An unreadable branch is a keep: better to leave a dead
				// worktree than to delete the one an open change request is on.
				extraSkips = append(extraSkips, Decision{
					Candidate: Candidate{Item: path.Item, Path: path.Path},
					Reason:    ReasonGitRefused, Detail: err.Error(),
				})
				continue
			}
			branch = name
		}
		dirty, err := dirtyOf(ctx, in.Git, path.Path)
		if err != nil {
			extraSkips = append(extraSkips, Decision{
				Candidate: Candidate{Item: path.Item, Path: path.Path, Branch: branch},
				Reason:    ReasonGitRefused, Detail: err.Error(),
			})
			continue
		}
		candidates = append(candidates, Candidate{
			Item:         path.Item,
			Path:         path.Path,
			Branch:       branch,
			Dirty:        dirty,
			HasOpenCR:    openCRs[branch],
			LiveDispatch: liveItems[path.Item],
			LiveRow:      liveRows[path.Item],
		})
	}
	// Skips lead: a symlink or an out-of-root path is a thing the operator should
	// see before any removal is read.
	result.Decisions = append(append(skips, extraSkips...), Select(candidates)...)
	return result, nil
}

// dirtyOf reads one worktree's dirtiness, preferring git. A workspace with no
// git seam at all has no removal candidates: the branch could not be read
// either, so every path already became a keep above.
func dirtyOf(ctx context.Context, git Git, path string) (bool, error) {
	if git == nil {
		return false, errors.New("housekeeping: no git seam configured")
	}
	return git.Dirty(ctx, path)
}

// Candidates lists the worktree directories under the project's root that the
// GC may consider, plus the entries it refused to consider. Only direct children
// whose name matches wt-<key>-<n> are candidates: the main checkout is not under
// this root, a symlink is skipped so a GC cannot follow one into somebody's
// source tree, and anything else is left alone.
func Candidates(ctx context.Context, in Input) ([]Path, []Decision, error) {
	root := in.WorktreeRoot
	if root == "" {
		return nil, nil, nil
	}
	if in.Caller != nil {
		marked, err := in.Caller.CleanupPaths(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("housekeeping: cleanup paths: %w", err)
		}
		if len(marked) > 0 {
			paths, skips := fromCleanupPaths(root, in.Project, marked)
			return paths, skips, nil
		}
	}
	return scanRoot(root, in.Project)
}

// fromCleanupPaths keeps only the marked paths that are direct children of the
// root, so a caller cannot widen the blast radius past the scope rule. A path
// outside the root is a skip decision, not a silent drop.
func fromCleanupPaths(root, project string, marked []CleanupPath) ([]Path, []Decision) {
	root = filepath.Clean(root)
	out := make([]Path, 0, len(marked))
	var skips []Decision
	for _, entry := range marked {
		path := filepath.Clean(entry.Path)
		if filepath.Dir(path) != root {
			skips = append(skips, Decision{
				Candidate: Candidate{Path: path, Item: entry.Item, Branch: entry.Branch},
				Reason:    ReasonOutsideRoot, Detail: path,
			})
			continue
		}
		// A symlink is never followed, whatever the caller marked: following one is
		// how a GC deletes somebody else's source tree. Lstat, not Stat.
		if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
			skips = append(skips, Decision{
				Candidate: Candidate{Path: path, Item: entry.Item, Branch: entry.Branch},
				Reason:    ReasonSymlink, Detail: path,
			})
			continue
		}
		item := entry.Item
		if item == 0 {
			item = worktreeItem(project, filepath.Base(path))
		}
		if item == 0 {
			// Under the root but not a wt-<key>-<n> name and no item named it:
			// nothing here says which item it belongs to, so it is not a candidate.
			skips = append(skips, Decision{
				Candidate: Candidate{Path: path}, Reason: ReasonOutsideRoot, Detail: path,
			})
			continue
		}
		out = append(out, Path{Path: path, Item: item, Branch: entry.Branch})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Path < out[b].Path })
	return out, skips
}

// scanRoot reads the root itself: wt-<key>-<n> direct children are candidates,
// and a symlink matching the pattern is a ReasonSymlink skip decision so the
// operator can see it was noticed and refused, not silently ignored.
func scanRoot(root, project string) ([]Path, []Decision, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("housekeeping: read worktree root %s: %w", root, err)
	}
	out := make([]Path, 0, len(entries))
	var skips []Decision
	for _, entry := range entries {
		item := worktreeItem(project, entry.Name())
		if item == 0 {
			continue
		}
		path := filepath.Join(root, entry.Name())
		if entry.Type()&os.ModeSymlink != 0 {
			skips = append(skips, Decision{
				Candidate: Candidate{Path: path, Item: item},
				Reason:    ReasonSymlink, Detail: path,
			})
			continue
		}
		out = append(out, Path{Path: path, Item: item})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Path < out[b].Path })
	return out, skips, nil
}

var worktreePattern = regexp.MustCompile(`^wt-(.+)-(\d+)$`)

// worktreeItem returns the item number a `wt-<key>-<n>` directory encodes, or 0
// when the name is not a worktree for this project. A key may itself contain a
// dash, so the number is the last dash-separated run and the key is everything
// between `wt-` and it.
func worktreeItem(project, name string) int {
	match := worktreePattern.FindStringSubmatch(name)
	if match == nil {
		return 0
	}
	if match[1] != project {
		return 0
	}
	number, err := strconv.Atoi(match[2])
	if err != nil || number <= 0 {
		return 0
	}
	return number
}

// openCRBranches is the set of head branches with an open change request.
func openCRBranches(crs []forge.ChangeRequest) map[string]bool {
	open := map[string]bool{}
	for _, cr := range crs {
		if cr.Merged || !strings.EqualFold(cr.State, forge.StateOpen) {
			continue
		}
		if cr.Branch != "" {
			open[cr.Branch] = true
		}
	}
	return open
}

// liveRowItems maps every item a non-terminal queue row names. The rule is the
// one the dispatcher and the progress snapshot already use: a row is live when
// its pickup state (or its state, when pickup is empty) is not terminal, and the
// item is read from the row's own text.
func liveRowItems(rows []rig.QueueRow) map[int]bool {
	live := map[int]bool{}
	for _, row := range rows {
		if !rowLive(row) {
			continue
		}
		for _, item := range itemNumbers(row.Body, row.Summary) {
			live[item] = true
		}
	}
	return live
}

func rowLive(row rig.QueueRow) bool {
	state := strings.ToLower(strings.TrimSpace(row.PickupState))
	if state == "" {
		state = strings.ToLower(strings.TrimSpace(row.State))
	}
	switch state {
	case "", "terminal", "closed", "done", "cancelled", "canceled", "denied", "failed",
		"handed-off", "handed_off", "handedoff":
		return false
	}
	return true
}

var itemNumberPattern = regexp.MustCompile(`#(\d+)\b`)

// itemNumbers reads the #<n> numbers out of a row's text. Only the head of a
// long body is searched, the same bound the dispatcher uses.
func itemNumbers(texts ...string) []int {
	seen := map[int]bool{}
	var out []int
	for _, text := range texts {
		if len(text) > 400 {
			text = text[:400]
		}
		for _, match := range itemNumberPattern.FindAllStringSubmatch(text, -1) {
			number, err := strconv.Atoi(match[1])
			if err != nil || number <= 0 || seen[number] {
				continue
			}
			seen[number] = true
			out = append(out, number)
		}
	}
	return out
}

// ExecGit runs git against one repository. It is the production Git.
type ExecGit struct {
	RepoPath string
}

// Branch returns the branch a worktree has checked out.
func (g ExecGit) Branch(ctx context.Context, path string) (string, error) {
	out, err := runGit(ctx, path, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Dirty reports whether the worktree has uncommitted changes.
func (g ExecGit) Dirty(ctx context.Context, path string) (bool, error) {
	out, err := runGit(ctx, path, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) != "", nil
}

// RemoveWorktree removes one worktree. `--force` is never passed.
func (g ExecGit) RemoveWorktree(ctx context.Context, path string) error {
	_, err := runGit(ctx, g.RepoPath, "worktree", "remove", path)
	return err
}

// PruneWorktrees drops the bookkeeping of worktrees whose directories are gone.
func (g ExecGit) PruneWorktrees(ctx context.Context) error {
	_, err := runGit(ctx, g.RepoPath, "worktree", "prune")
	return err
}

// runGit runs one git command against a repository and returns stdout. The
// stderr tail is folded into the error, because a refused `worktree remove`
// says why there and that message is what a keep carries.
func runGit(ctx context.Context, repo string, args ...string) ([]byte, error) {
	full := append([]string{"-C", repo}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
