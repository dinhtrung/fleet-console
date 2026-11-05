package housekeeping

import "context"

// Git is the worktree seam. Tests use a fake that records argv; the app uses
// ExecGit. None of these may pass `--force`: the dirty work the keep conditions
// protect is exactly what a force would discard. A path git does not recognise
// as a worktree makes RemoveWorktree fail, which is a keep, not a deletion.
type Git interface {
	// Branch is the branch a worktree has checked out.
	Branch(ctx context.Context, path string) (string, error)
	// Dirty reports whether `git status --porcelain` in the worktree is non-empty.
	Dirty(ctx context.Context, path string) (bool, error)
	// RemoveWorktree runs `git worktree remove <path>`, never with `--force`.
	RemoveWorktree(ctx context.Context, path string) error
	// PruneWorktrees runs `git worktree prune`.
	PruneWorktrees(ctx context.Context) error
}

// Execute performs each removal decision. A candidate only reaches here when
// all four keep conditions were false, and even then git has the last word: a
// refusal becomes a keep carrying git's own message rather than a failed
// removal. Prune runs once, after the removals, so a worktree whose directory
// git declines to delete does not leave a stale record behind.
//
// Execute is called only with Apply; Plan is the entry point that decides when.
func Execute(ctx context.Context, decisions []Decision, git Git) ([]Decision, error) {
	attempted := false
	for index := range decisions {
		if !decisions[index].Remove {
			continue
		}
		attempted = true
		path := decisions[index].Candidate.Path
		if err := git.RemoveWorktree(ctx, path); err != nil {
			decisions[index].Remove = false
			decisions[index].Reason = ReasonGitRefused
			decisions[index].Detail = err.Error()
		}
	}
	if !attempted {
		return decisions, nil
	}
	if err := git.PruneWorktrees(ctx); err != nil {
		return decisions, err
	}
	return decisions, nil
}
