package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "ls-remote-heads.txt"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

func TestBranchIndex(t *testing.T) {
	branches := ParseRemoteHeads(fixture(t))
	if len(branches) != 3 {
		t.Fatalf("branches = %+v, want 3", branches)
	}
	want := []Branch{
		{Name: "feature/12-connection-pool", Item: 12},
		{Name: "feature/7-web-foundation", Item: 7},
		{Name: "feature/9-board", Item: 9},
	}
	for i := range want {
		if branches[i] != want[i] {
			t.Fatalf("branch %d = %+v, want %+v", i, branches[i], want[i])
		}
	}
}

type fakeRunner struct{ out []byte }

func (f fakeRunner) Run(context.Context, ...string) ([]byte, error) { return f.out, nil }

func TestIndexInvokesLSRemote(t *testing.T) {
	var got [][]string
	runner := runnerFunc(func(_ context.Context, args ...string) ([]byte, error) {
		got = append(got, args)
		return fixture(t), nil
	})
	branches, err := Index(context.Background(), runner)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	if len(branches) != 3 {
		t.Fatalf("branches = %+v", branches)
	}
	if len(got) != 1 || fmt.Sprint(got[0]) != "[ls-remote --heads origin]" {
		t.Fatalf("args = %v", got)
	}
}

type runnerFunc func(context.Context, ...string) ([]byte, error)

func (f runnerFunc) Run(ctx context.Context, args ...string) ([]byte, error) { return f(ctx, args...) }

// TestRunTimesOut puts a wedged git on PATH and requires the call to return the
// deadline error instead of blocking the tick.
func TestRunTimesOut(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "git")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	runner := ExecRunner{RepoPath: ".", Timeout: 100 * time.Millisecond}
	start := time.Now()
	_, err := runner.Run(context.Background(), "ls-remote", "--heads", "origin")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Run returned nil, want a timeout error")
	}
	t.Logf("timeout error after %s: %v", elapsed, err)
	if !strings.Contains(err.Error(), "timed out after 100ms") {
		t.Fatalf("error = %q, want the deadline", err)
	}
	if !strings.Contains(err.Error(), "ls-remote") {
		t.Fatalf("error = %q, want the command named", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Run took %s, want the 100ms deadline", elapsed)
	}
}
