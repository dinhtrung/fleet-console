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
	want := []string{"gh", "pr", "merge", "9", "--squash", "--match-head-commit", "abc1234"}
	if len(runner.calls) != 1 || !reflect.DeepEqual(runner.calls[0], want) {
		t.Fatalf("argv = %v, want %v", runner.calls, want)
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
		{"gh", "issue", "edit", "5", "--add-label", MarkerLabel},
		{"gh", "issue", "close", "5"},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("argv = %v, want %v", runner.calls, want)
	}
}
