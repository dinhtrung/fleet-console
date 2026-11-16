package forge

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
)

type recordRunner struct{ calls [][]string }

func (r *recordRunner) Run(_ context.Context, argv ...string) (string, string, error) {
	r.calls = append(r.calls, append([]string(nil), argv...))
	return "", "", nil
}

func TestMergeMatchHeadCommit(t *testing.T) {
	runner := &recordRunner{}
	github := NewGitHub("dinhtrung/fleet-console", runner, &Budget{Max: 10})
	if err := github.Merge(context.Background(), 9, "abc1234"); err != nil {
		t.Fatalf("merge: %v", err)
	}
	want := []string{"gh", "pr", "merge", "9", "--squash", "--match-head-commit", "abc1234", "-R", "dinhtrung/fleet-console"}
	if len(runner.calls) != 1 || !reflect.DeepEqual(runner.calls[0], want) {
		t.Fatalf("argv = %v, want %v", runner.calls, want)
	}
}

// TestEveryGitHubVerbCarriesTheRepo is the structural guard behind #112.
//
// The service runs in $HOME, where `gh` resolves a repository from the working
// directory; a verb without -R dies with "failed to run git: ... not a git
// repository" and the write silently does not happen. `gh api` is exempt because
// the slug rides in its path, which is exactly why the omission was invisible:
// most of this adapter reads, and only the verbs broke.
//
// Per-call assertions would have covered the three verbs known today. This walks
// every method the Forge interface declares, so a fourth verb added without -R
// fails here too — and adding a method to the interface fails the count below
// rather than passing silently.
//
// Why no test caught #112: this suite runs with the working directory inside the
// repository, where `gh` resolves a repository on its own and every verb works
// with or without -R. The service runs in $HOME. A argv assertion is the only
// form that holds in both, which is why the guard is on the recorded argv rather
// than on a live call.
func TestEveryGitHubVerbCarriesTheRepo(t *testing.T) {
	const declaredMethods = 15
	if got := reflect.TypeOf((*Forge)(nil)).Elem().NumMethod(); got != declaredMethods {
		t.Fatalf("Forge declares %d methods, want %d: exercise the new one in this test and bump the count",
			got, declaredMethods)
	}

	runner := &recordRunner{}
	github := NewGitHub("dinhtrung/fleet-console", runner, &Budget{Max: 100})
	ctx := context.Background()
	// Every Forge method, called once. A method that needs a JSON answer errors
	// here because recordRunner returns none; the argv is recorded before that.
	_ = github.Kind()
	_, _ = github.Items(ctx, Timeout{})
	_, _ = github.ChangeRequests(ctx)
	_, _ = github.Notes(ctx, 5)
	_, _ = github.CRNotes(ctx, 9)
	_, _ = github.ItemState(ctx, 5)
	_, _ = github.SubIssues(ctx, 5)
	_ = github.SetMarker(ctx, 5, false)
	_ = github.Comment(ctx, 5, "body")
	_ = github.CloseItem(ctx, 5)
	_ = github.Ready(ctx, 9)
	_ = github.Merge(ctx, 9, "abc1234")
	// A full sha keeps EnsureBranch to the one POST; a branch name would add the
	// resolving read this guard counts.
	_ = github.EnsureBranch(ctx, "epic/476-x", "0123456789abcdef0123456789abcdef01234567")
	_, _ = github.OpenChangeRequest(ctx, OpenChangeRequestInput{Head: "feature/1-x", Base: "epic/476-x", Title: "t", Body: "b"})
	_ = github.UpdateBranch(ctx, 9)

	// Kind reports a constant and runs no CLI call; every other method does exactly
	// one. Asserted exactly rather than as a lower bound, so a method that stops
	// calling the CLI is noticed too.
	if want := declaredMethods - 1; len(runner.calls) != want {
		t.Fatalf("recorded %d CLI calls for %d methods, want %d: Kind is the only one that does not shell out",
			len(runner.calls), declaredMethods, want)
	}
	for _, argv := range runner.calls {
		// Every call starts with the binary name; drop it before classifying.
		rest := argv[1:]
		if len(rest) >= 1 && rest[0] == "api" {
			continue // the slug is in the path
		}
		if !carriesRepo(rest, "dinhtrung/fleet-console") {
			t.Errorf("verb %v does not carry -R: the service runs in $HOME where gh finds no repository", rest)
		}
	}
}

// carriesRepo reports whether argv names the repository, in either spelling the
// CLI accepts: `-R <slug>` for gh verbs, `--repo <slug>` where a verb has no -R.
func carriesRepo(argv []string, slug string) bool {
	for i, arg := range argv {
		if (arg == "-R" || arg == "--repo") && i+1 < len(argv) && argv[i+1] == slug {
			return true
		}
	}
	return false
}

// bodyCaptureRunner records argv and keeps the contents of a --body-file, so a
// test can see what OpenChangeRequest wrote even though the adapter removes the
// temp file on return.
type bodyCaptureRunner struct {
	calls [][]string
	body  string
}

func (r *bodyCaptureRunner) Run(_ context.Context, argv ...string) (string, string, error) {
	r.calls = append(r.calls, append([]string(nil), argv...))
	for i, arg := range argv {
		if arg == "--body-file" && i+1 < len(argv) {
			if raw, err := os.ReadFile(argv[i+1]); err == nil {
				r.body = string(raw)
			}
		}
	}
	return "", "", nil
}

// TestEpicBranchWriteArgv records the exact argv of the three epic-branch writes
// on each forge (#486): ensure a branch, open a change request with an explicit
// base, and update a branch. The argv is the contract with the CLI, and the
// temp-file body is asserted by content because its path is not stable.
func TestEpicBranchWriteArgv(t *testing.T) {
	ctx := context.Background()
	const sha = "0123456789abcdef0123456789abcdef01234567"

	assertFlags := func(t *testing.T, argv []string, want ...string) {
		t.Helper()
		joined := strings.Join(argv, " ")
		for _, flag := range want {
			if !strings.Contains(joined, flag) {
				t.Fatalf("argv %v does not carry %q", argv, flag)
			}
		}
	}

	t.Run("github", func(t *testing.T) {
		runner := &bodyCaptureRunner{}
		github := NewGitHub("dinhtrung/fleet-console", runner, &Budget{Max: 10})
		if err := github.EnsureBranch(ctx, "epic/476-x", sha); err != nil {
			t.Fatalf("ensure: %v", err)
		}
		if _, err := github.OpenChangeRequest(ctx, OpenChangeRequestInput{
			Head: "feature/1-x", Base: "epic/476-x", Title: "Epic 476", Body: "child list", Draft: true,
		}); err != nil {
			t.Fatalf("open: %v", err)
		}
		if err := github.UpdateBranch(ctx, 9); err != nil {
			t.Fatalf("update: %v", err)
		}
		if len(runner.calls) != 3 {
			t.Fatalf("calls = %d, want 3", len(runner.calls))
		}
		for _, argv := range runner.calls {
			t.Logf("argv: %v", argv)
		}
		wantEnsure := []string{"gh", "api", "--method", "POST",
			"repos/dinhtrung/fleet-console/git/refs", "-f", "ref=refs/heads/epic/476-x", "-f", "sha=" + sha}
		if !reflect.DeepEqual(runner.calls[0], wantEnsure) {
			t.Fatalf("ensure argv = %v, want %v", runner.calls[0], wantEnsure)
		}
		assertFlags(t, runner.calls[1], "pr create", "--base epic/476-x", "--head feature/1-x",
			"--title Epic 476", "--body-file", "--draft", "-R dinhtrung/fleet-console")
		if runner.body != "child list" {
			t.Fatalf("body file carried %q, want %q", runner.body, "child list")
		}
		wantUpdate := []string{"gh", "api", "--method", "PUT",
			"repos/dinhtrung/fleet-console/pulls/9/update-branch"}
		if !reflect.DeepEqual(runner.calls[2], wantUpdate) {
			t.Fatalf("update argv = %v, want %v", runner.calls[2], wantUpdate)
		}
	})

	t.Run("gitlab", func(t *testing.T) {
		runner := &bodyCaptureRunner{}
		gitlab := NewGitLab("dinhtrung/fleet-console", "", runner, &Budget{Max: 10})
		if err := gitlab.EnsureBranch(ctx, "epic/476-x", sha); err != nil {
			t.Fatalf("ensure: %v", err)
		}
		if _, err := gitlab.OpenChangeRequest(ctx, OpenChangeRequestInput{
			Head: "feature/1-x", Base: "epic/476-x", Title: "Epic 476", Body: "child list", Draft: true,
		}); err != nil {
			t.Fatalf("open: %v", err)
		}
		if err := gitlab.UpdateBranch(ctx, 9); err != nil {
			t.Fatalf("update: %v", err)
		}
		if len(runner.calls) != 3 {
			t.Fatalf("calls = %d, want 3", len(runner.calls))
		}
		for _, argv := range runner.calls {
			t.Logf("argv: %v", argv)
		}
		wantEnsure := []string{"glab", "api", "--method", "POST",
			"projects/dinhtrung%2Ffleet-console/repository/branches", "-f", "branch=epic/476-x", "-f", "ref=" + sha}
		if !reflect.DeepEqual(runner.calls[0], wantEnsure) {
			t.Fatalf("ensure argv = %v, want %v", runner.calls[0], wantEnsure)
		}
		assertFlags(t, runner.calls[1], "mr create", "--target-branch epic/476-x",
			"--source-branch feature/1-x", "--title Epic 476", "--description child list",
			"--repo dinhtrung/fleet-console", "--draft")
		wantUpdate := []string{"glab", "api", "--method", "PUT",
			"projects/dinhtrung%2Ffleet-console/merge_requests/9/rebase"}
		if !reflect.DeepEqual(runner.calls[2], wantUpdate) {
			t.Fatalf("update argv = %v, want %v", runner.calls[2], wantUpdate)
		}
	})
}

// TestEnsureBranchTreatsExistingAsSuccess: an already-existing branch is the
// state the caller wanted, not a failure (the brief's boundary). Any other
// create failure is returned for the caller to decide.
func TestEnsureBranchTreatsExistingAsSuccess(t *testing.T) {
	ctx := context.Background()
	exists := &fixtureRunner{root: "testdata", stderr: map[string]string{
		"gh api --method POST repos/dinhtrung/fleet-console/git/refs -f ref=refs/heads/epic/476-x -f sha=0123456789abcdef0123456789abcdef01234567": "Reference already exists",
	}}
	github := NewGitHub("dinhtrung/fleet-console", exists, &Budget{Max: 10})
	if err := github.EnsureBranch(ctx, "epic/476-x", "0123456789abcdef0123456789abcdef01234567"); err != nil {
		t.Fatalf("an existing branch is not an error: %v", err)
	}
	failed := &fixtureRunner{root: "testdata", stderr: map[string]string{
		"gh api --method POST repos/dinhtrung/fleet-console/git/refs -f ref=refs/heads/epic/476-x -f sha=0123456789abcdef0123456789abcdef01234567": "HTTP 500",
	}}
	github = NewGitHub("dinhtrung/fleet-console", failed, &Budget{Max: 10})
	if err := github.EnsureBranch(ctx, "epic/476-x", "0123456789abcdef0123456789abcdef01234567"); err == nil {
		t.Fatal("a server error must surface for the caller to decide")
	}
}

func TestMarkerAndCloseArgv(t *testing.T) {
	runner := &recordRunner{}
	github := NewGitHub("dinhtrung/fleet-console", runner, &Budget{Max: 10})
	if err := github.SetMarker(context.Background(), 5, true); err != nil {
		t.Fatal(err)
	}
	if err := github.CloseItem(context.Background(), 5); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"gh", "issue", "edit", "5", "--add-label", MarkerLabel, "-R", "dinhtrung/fleet-console"},
		{"gh", "issue", "close", "5", "-R", "dinhtrung/fleet-console"},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("argv = %v, want %v", runner.calls, want)
	}
}
