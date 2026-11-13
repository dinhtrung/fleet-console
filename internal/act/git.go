package act

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Git is the worktree seam. Tests use a fake; the app uses ExecGit.
type Git interface {
	Fetch(ctx context.Context) error
	BaseSHA(ctx context.Context) (string, error)
	// WorktreeAddOrReuse reuses an existing worktree, adopts an existing branch, or
	// creates both. It never deletes anything.
	WorktreeAddOrReuse(ctx context.Context, worktree, branch, base string) (reused bool, err error)
	// IsAncestor reports whether ancestor is an ancestor of descendant in this
	// repository. The answer is the repository's, never a string comparison
	// (superseded-verdict, design D1); an unknown commit is an error, so the
	// caller can ask rather than assume (design D2).
	IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error)
}

// ExecGit runs git in one repository.
type ExecGit struct {
	RepoPath      string
	DefaultBranch string
}

func (g ExecGit) run(ctx context.Context, args ...string) ([]byte, error) {
	full := append([]string{"-C", g.RepoPath}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// Fetch updates origin.
func (g ExecGit) Fetch(ctx context.Context) error {
	_, err := g.run(ctx, "fetch", "origin")
	return err
}

// BaseSHA resolves origin/<default_branch>.
func (g ExecGit) BaseSHA(ctx context.Context) (string, error) {
	branch := g.DefaultBranch
	if branch == "" {
		branch = "main"
	}
	out, err := g.run(ctx, "rev-parse", "origin/"+branch)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// IsAncestor asks the repository whether one commit is an ancestor of another.
// `git merge-base --is-ancestor` answers with its exit status: 0 is "yes", 1 is
// "no, but both commits exist". Anything else — an unknown object, a broken
// repository, a timeout — is an error, and the caller treats it as "not
// superseded" so a note nobody could check still reaches the operator
// (superseded-verdict, design D2). Abbreviated shas are the repository's
// business, not the caller's: git resolves them here.
func (g ExecGit) IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error) {
	if ancestor == "" || descendant == "" {
		return false, fmt.Errorf("git merge-base: two commits are required, got %q and %q", ancestor, descendant)
	}
	cmd := exec.CommandContext(ctx, "git", "-C", g.RepoPath, "merge-base", "--is-ancestor", ancestor, descendant)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("git merge-base --is-ancestor %s %s: %w: %s",
		ancestor, descendant, err, strings.TrimSpace(stderr.String()))
}

// WorktreeAddOrReuse implements the reuse rule: never delete, reuse the right
// worktree, adopt an existing branch, and create only when neither exists.
func (g ExecGit) WorktreeAddOrReuse(ctx context.Context, worktree, branch, base string) (bool, error) {
	if _, err := os.Stat(filepath.Join(worktree, ".git")); err == nil {
		return true, nil
	}
	if _, err := g.run(ctx, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		if _, err := g.run(ctx, "worktree", "add", worktree, branch); err != nil {
			return false, err
		}
		return false, nil
	}
	if out, err := g.run(ctx, "ls-remote", "--exit-code", "--heads", "origin", branch); err == nil && len(out) > 0 {
		if _, err := g.run(ctx, "worktree", "add", "-b", branch, worktree, "origin/"+branch); err != nil {
			return false, err
		}
		return false, nil
	}
	if _, err := g.run(ctx, "worktree", "add", "-b", branch, worktree, base); err != nil {
		return false, err
	}
	return false, nil
}
