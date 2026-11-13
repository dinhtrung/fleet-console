package forge

import (
	"context"
	"strings"
	"testing"
)

// TestMergeUnknown pins the three-state split (epic #341, design D1): a word
// the forge has not answered with — empty, GitHub's unknown, GitLab's
// checking — is waiting, never a refusal; a computed word is what it says.
// MergeConflict is untouched: only the two rebaseable words are conflicts.
func TestMergeUnknown(t *testing.T) {
	cases := []struct {
		name     string
		state    string
		unknown  bool
		conflict bool
	}{
		{"no word", "", true, false},
		{"github computing", "unknown", true, false},
		{"gitlab computing", "checking", true, false},
		{"case-insensitive", "UNKnown", true, false},
		{"github conflict", "dirty", false, true},
		{"gitlab conflict", "cannot_be_merged", false, true},
		{"a clean word", "clean", false, false},
		{"a blocked word", "blocked", false, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cr := ChangeRequest{MergeState: tc.state}
			if got := cr.MergeUnknown(); got != tc.unknown {
				t.Fatalf("MergeUnknown = %v, want %v", got, tc.unknown)
			}
			if got := cr.MergeConflict(); got != tc.conflict {
				t.Fatalf("MergeConflict = %v, want %v", got, tc.conflict)
			}
		})
	}
}

// TestGitHubMergeStateComesFromTheComputingRead pins the refetch contract
// (design D1): when the list page leaves mergeable null, the single-PR read
// is the response that computed mergeability, and its mergeable_state — not
// the list page's `unknown` — is the word the change request carries.
func TestGitHubMergeStateComesFromTheComputingRead(t *testing.T) {
	runner := githubFixtures()
	runner.exact["gh api repos/dinhtrung/fleet-console/pulls?state=all&per_page=100&page=1"] = "github/pulls-unknown.json"
	runner.exact["gh api repos/dinhtrung/fleet-console/pulls/14"] = "github/pull-14-computed.json"
	runner.exact["gh api repos/dinhtrung/fleet-console/pulls/14/files?per_page=100"] = "github/pull-files.json"
	runner.exact["gh api repos/dinhtrung/fleet-console/commits/a21e0626300f54f2c062bbb8026fe67bbdf47c3d/check-runs"] = "github/check-runs.json"
	runner.exact["gh api repos/dinhtrung/fleet-console/commits/a21e0626300f54f2c062bbb8026fe67bbdf47c3d/status"] = "github/commit-status.json"

	adapter := NewGitHub("dinhtrung/fleet-console", runner, &Budget{Max: 100})
	requests, err := adapter.ChangeRequests(context.Background())
	if err != nil {
		t.Fatalf("ChangeRequests: %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(requests))
	}
	cr := requests[0]
	if cr.Mergeable {
		t.Fatal("mergeable = true, want the computed false")
	}
	if cr.MergeState != "dirty" {
		t.Fatalf("merge_state = %q, want the refetch's dirty, not the list page's unknown", cr.MergeState)
	}
	if !cr.MergeConflict() || cr.MergeUnknown() {
		t.Fatalf("conflict=%v unknown=%v, want the computed word to read as a conflict", cr.MergeConflict(), cr.MergeUnknown())
	}
	// The refetch happened: one single-PR read for the null list page.
	refetch := 0
	for _, call := range runner.calls {
		if strings.HasSuffix(call, "/pulls/14") {
			refetch++
		}
	}
	if refetch != 1 {
		t.Fatalf("single-PR refetches = %d (%v), want 1", refetch, runner.calls)
	}
}
