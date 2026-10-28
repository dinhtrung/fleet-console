package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
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
