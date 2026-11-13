package act

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/forge"
)

// gitRepo commits an empty tree twice and returns the first and second shas,
// plus a third commit that lives only on a side branch — the foreign sha.
func gitRepo(t *testing.T) (repo, ancestor, head, foreign string) {
	t.Helper()
	repo = t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(cmd.Environ(),
			"GIT_AUTHOR_NAME=a", "GIT_AUTHOR_EMAIL=a@b",
			"GIT_COMMITTER_NAME=a", "GIT_COMMITTER_EMAIL=a@b")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("git", "init", "-q", "-b", "main", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	run("commit", "--allow-empty", "-q", "-m", "one")
	ancestor = run("rev-parse", "HEAD")
	run("commit", "--allow-empty", "-q", "-m", "two")
	head = run("rev-parse", "HEAD")
	run("checkout", "-q", "-b", "side", "HEAD~1")
	run("commit", "--allow-empty", "-q", "-m", "side")
	foreign = run("rev-parse", "HEAD")
	run("checkout", "-q", "main")
	return repo, ancestor, head, foreign
}

// TestSupersededVerdictThroughARealRepository runs the tick against a real
// repository: the classification reaches git, not a fixture map. An ancestor
// asks nobody; a sha from a side branch asks the operator. The head is a real
// commit, so the merge path stays on the superseded branch it is meant to test.
func TestSupersededVerdictThroughARealRepository(t *testing.T) {
	repo, ancestor, head, foreign := gitRepo(t)

	// The change request's head must be the repository's second commit. The
	// fixture hard-codes its head, so this case rebuilds the change request
	// with the real one.
	newTick := func(t *testing.T, note string) (*supersededFixture, TickReport) {
		t.Helper()
		f := newSupersededFixture(t, note, nil, nil)
		f.in.Git = ExecGit{RepoPath: repo}
		f.fake.NotesValue = []forge.Comment{{ID: "n1", Body: note, CreatedAt: "2026-11-13T11:00:00Z"}}
		f.fake.RequestsValue[0].HeadSHA = head
		f.in.CRs = f.fake.RequestsValue
		report, err := RunTick(f.ctx, f.in)
		if err != nil {
			t.Fatalf("tick: %v", err)
		}
		return f, report
	}

	t.Run("ancestor asks nobody", func(t *testing.T) {
		f, report := newTick(t, "REJECT "+ancestor)
		if f.queue.createCalls != 0 {
			t.Fatalf("queue creates = %d, want 0: the repository says the commit is an ancestor", f.queue.createCalls)
		}
		if !hasSkip(report, "superseded") {
			t.Fatalf("skipped = %v, want the superseded line", report.Skipped)
		}
		if len(report.Merges) != 0 {
			t.Fatalf("merges = %+v, want none", report.Merges)
		}
	})

	t.Run("abbreviated ancestor asks nobody", func(t *testing.T) {
		f, _ := newTick(t, "REJECT "+ancestor[:7])
		if f.queue.createCalls != 0 {
			t.Fatalf("queue creates = %d, want 0: git resolves the abbreviation", f.queue.createCalls)
		}
	})

	t.Run("side branch asks the operator", func(t *testing.T) {
		f, _ := newTick(t, "REJECT "+foreign)
		if f.queue.createCalls != 1 {
			t.Fatalf("queue creates = %d, want 1: a commit outside the head's history is the operator's", f.queue.createCalls)
		}
	})
}
