package act

import (
	"bytes"
	"context"
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
