package forge

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// stateRunner answers `api` reads by path and records what it was asked. ItemState
// is a single-item read on purpose, so the assertions here are about *which* read
// happened: a full item list would return the closed item only by accident.
type stateRunner struct {
	answers map[string]string
	errs    map[string]error
	calls   []string
}

func (r *stateRunner) Run(_ context.Context, argv ...string) (string, string, error) {
	key := strings.Join(argv, " ")
	r.calls = append(r.calls, key)
	path := ""
	for _, arg := range argv {
		if strings.HasPrefix(arg, "repos/") || strings.HasPrefix(arg, "projects/") {
			path = arg
		}
	}
	if err, ok := r.errs[path]; ok {
		return "", "gh: Not Found (HTTP 404)", err
	}
	if answer, ok := r.answers[path]; ok {
		return answer, "", nil
	}
	return "", "unexpected read " + key, errors.New("exit status 1")
}

// TestGitHubItemStateReadsAClosedItem is the re-home arm's precondition on the
// real adapter: Items asks `state=open`, so a closed item never appears there
// (#464). This read has to answer for it, and it must be the item read — not the
// open list — or the closed item is invisible again.
func TestGitHubItemStateReadsAClosedItem(t *testing.T) {
	runner := &stateRunner{answers: map[string]string{
		"repos/dinhtrung/fleet-console/issues/438": `{"number":438,"state":"closed","title":"Old"}`,
	}}
	github := NewGitHub("dinhtrung/fleet-console", runner, &Budget{Max: 10})

	state, err := github.ItemState(context.Background(), 438)
	if err != nil {
		t.Fatalf("ItemState: %v", err)
	}
	if state != StateClosed {
		t.Fatalf("state = %q, want %q", state, StateClosed)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("calls = %v, want exactly one read", runner.calls)
	}
	if want := "gh api repos/dinhtrung/fleet-console/issues/438"; runner.calls[0] != want {
		t.Fatalf("read = %q, want %q", runner.calls[0], want)
	}
}

// TestGitHubItemStateReadsAnOpenItem keeps the other half honest: an open item is
// still open here, so the re-home arm cannot swallow the two-live-items refusal.
func TestGitHubItemStateReadsAnOpenItem(t *testing.T) {
	runner := &stateRunner{answers: map[string]string{
		"repos/dinhtrung/fleet-console/issues/438": `{"number":438,"state":"open"}`,
	}}
	github := NewGitHub("dinhtrung/fleet-console", runner, &Budget{Max: 10})

	state, err := github.ItemState(context.Background(), 438)
	if err != nil {
		t.Fatalf("ItemState: %v", err)
	}
	if state != StateOpen {
		t.Fatalf("state = %q, want %q", state, StateOpen)
	}
}

// TestItemStateNeverInventsAState: a number the forge does not have, and a state
// name nobody recognises, are both errors. Reading either as "closed" would
// re-home a branch onto the wrong item, and as "open" would refuse a legitimate
// merge, so neither is answered.
func TestItemStateNeverInventsAState(t *testing.T) {
	notFound := errors.New("exit status 1")
	for _, tc := range []struct {
		name    string
		gitlab  bool
		missing bool
		answer  string
	}{
		{name: "github: no such item", missing: true},
		{name: "github: an unrecognised state", answer: `{"number":438,"state":"merged"}`},
		{name: "gitlab: no such item", gitlab: true, missing: true},
		{name: "gitlab: an unrecognised state", answer: `{"iid":438,"state":"locked"}`, gitlab: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := "repos/dinhtrung/fleet-console/issues/438"
			runner := &stateRunner{}
			var target Forge
			if tc.gitlab {
				path = "projects/horisen%2Fmnp%2Fengine%2Ftraffic-simulator-v2/issues/438"
				target = NewGitLab("horisen/mnp/engine/traffic-simulator-v2", "", runner, &Budget{Max: 10})
			} else {
				target = NewGitHub("dinhtrung/fleet-console", runner, &Budget{Max: 10})
			}
			if tc.missing {
				runner.errs = map[string]error{path: notFound}
			} else {
				runner.answers = map[string]string{path: tc.answer}
			}

			state, err := target.ItemState(context.Background(), 438)
			if err == nil {
				t.Fatalf("ItemState = %q, nil; want an error: neither an absent item nor an unknown state is a state", state)
			}
			if state != "" {
				t.Fatalf("state = %q, want empty alongside the error", state)
			}
			if !strings.Contains(err.Error(), "438") {
				t.Fatalf("error = %q, want the item number named", err)
			}
		})
	}
}

// TestGitLabItemStateReadsAClosedItem is the same precondition on the GitLab
// adapter, which is the other spelling: `state=opened` in the list, `closed` here.
func TestGitLabItemStateReadsAClosedItem(t *testing.T) {
	const project = "horisen/mnp/engine/traffic-simulator-v2"
	runner := &stateRunner{answers: map[string]string{
		"projects/horisen%2Fmnp%2Fengine%2Ftraffic-simulator-v2/issues/438": `{"iid":438,"state":"closed"}`,
	}}
	gitlab := NewGitLab(project, "", runner, &Budget{Max: 10})

	state, err := gitlab.ItemState(context.Background(), 438)
	if err != nil {
		t.Fatalf("ItemState: %v", err)
	}
	if state != StateClosed {
		t.Fatalf("state = %q, want %q", state, StateClosed)
	}
	if want := "glab api projects/horisen%2Fmnp%2Fengine%2Ftraffic-simulator-v2/issues/438"; runner.calls[0] != want {
		t.Fatalf("read = %q, want %q", runner.calls[0], want)
	}
}

// TestGitLabItemStateTranslatesOpened: GitLab spells the open state "opened", and
// that is the one name the adapter maps. The app compares against "open", so a
// refusal that read "opened" as an unknown state would refuse every legitimate
// two-live-items merge on GitLab instead.
func TestGitLabItemStateTranslatesOpened(t *testing.T) {
	runner := &stateRunner{answers: map[string]string{
		"projects/horisen%2Fmnp%2Fengine%2Ftraffic-simulator-v2/issues/438": `{"iid":438,"state":"opened"}`,
	}}
	gitlab := NewGitLab("horisen/mnp/engine/traffic-simulator-v2", "", runner, &Budget{Max: 10})

	state, err := gitlab.ItemState(context.Background(), 438)
	if err != nil {
		t.Fatalf("ItemState: %v", err)
	}
	if state != StateOpen {
		t.Fatalf("state = %q, want %q", state, StateOpen)
	}
}
