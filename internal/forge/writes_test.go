package forge

import (
	"context"
	"reflect"
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
	const declaredMethods = 12
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
