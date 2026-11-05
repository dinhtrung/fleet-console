package housekeeping

import "testing"

// One case per keep condition plus the removable case, and a precedence case:
// when several conditions hold, the first named wins so the reason is stable.
func TestSelectKeepConditions(t *testing.T) {
	tests := []struct {
		name       string
		candidate  Candidate
		wantRemove bool
		wantReason Reason
	}{
		{
			name:       "a shipped item's clean worktree is removed",
			candidate:  Candidate{Item: 7, Path: "/wt/wt-app-7", Branch: "feature/7-done"},
			wantRemove: true,
		},
		{
			name:       "an open change request keeps it",
			candidate:  Candidate{Item: 8, Path: "/wt/wt-app-8", Branch: "feature/8-open", HasOpenCR: true},
			wantReason: ReasonOpenCR,
		},
		{
			name:       "a live dispatch row keeps it",
			candidate:  Candidate{Item: 9, Path: "/wt/wt-app-9", LiveDispatch: true},
			wantReason: ReasonLiveDispatch,
		},
		{
			name:       "a live queue row keeps it",
			candidate:  Candidate{Item: 10, Path: "/wt/wt-app-10", LiveRow: true},
			wantReason: ReasonLiveRow,
		},
		{
			name:       "a dirty tree keeps it whatever the item's state",
			candidate:  Candidate{Item: 11, Path: "/wt/wt-app-11", Dirty: true},
			wantReason: ReasonDirty,
		},
		{
			name: "the first condition named wins when several hold",
			candidate: Candidate{
				Item: 12, Path: "/wt/wt-app-12", Branch: "b",
				HasOpenCR: true, LiveDispatch: true, LiveRow: true, Dirty: true,
			},
			wantReason: ReasonOpenCR,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decisions := Select([]Candidate{test.candidate})
			if len(decisions) != 1 {
				t.Fatalf("decisions = %d, want 1", len(decisions))
			}
			got := decisions[0]
			if got.Remove != test.wantRemove {
				t.Fatalf("Remove = %v, want %v (reason %q)", got.Remove, test.wantRemove, got.Reason)
			}
			if got.Reason != test.wantReason {
				t.Fatalf("Reason = %q, want %q", got.Reason, test.wantReason)
			}
			if test.wantReason == ReasonOpenCR && got.Detail != test.candidate.Branch {
				t.Fatalf("Detail = %q, want the branch %q", got.Detail, test.candidate.Branch)
			}
		})
	}
}

// Select is pure and order-preserving: the same input yields the same decisions.
func TestSelectIsOrderPreserving(t *testing.T) {
	in := []Candidate{
		{Item: 1, Path: "/wt/wt-app-1"},
		{Item: 2, Path: "/wt/wt-app-2", Dirty: true},
		{Item: 3, Path: "/wt/wt-app-3"},
	}
	decisions := Select(in)
	if len(decisions) != 3 {
		t.Fatalf("decisions = %d, want 3", len(decisions))
	}
	for index, decision := range decisions {
		if decision.Candidate.Item != in[index].Item {
			t.Fatalf("decision %d is item %d, want %d (order changed)", index, decision.Candidate.Item, in[index].Item)
		}
	}
	if !decisions[0].Remove || decisions[1].Remove || !decisions[2].Remove {
		t.Fatalf("remove pattern = %v %v %v, want true false true",
			decisions[0].Remove, decisions[1].Remove, decisions[2].Remove)
	}
}
