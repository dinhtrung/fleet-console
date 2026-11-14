package mirror

import (
	"testing"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/git"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// TestBuildSnapshotOpenCRIncludesADraft is the half of the one-task-per-epic
// rule's "A draft change request" scenario that no decide-level test can reach
// (#467).
//
// The decide snapshot's Branch carries HasOpenCR/HasMergedCR and no draft flag,
// so `decide` holds an epic on whatever the projection says. The projection is
// where a draft could silently stop counting: a builder's dispatch ends when the
// pull request opens, and a draft is exactly the state a review starts in — if
// this skipped drafts, the epic would free its holder and start the next sibling
// against an unmerged branch, which is the conflict the rule exists to prevent.
func TestBuildSnapshotOpenCRIncludesADraft(t *testing.T) {
	project := store.ProjectRow{ID: 1, Key: "app", ForgeKind: "github", Enabled: true}
	result := ProjectResult{
		Items: []forge.Item{{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Five"}},
		// The branch list is what BuildSnapshot iterates; a branch named for the
		// item is how the mirror associates a change request with it.
		Branches: []git.Branch{{Name: "feature/5-five", Item: 5}},
		CRs: []forge.ChangeRequest{{
			Number: 9, ClosesItem: 5, State: "open", Draft: true, HeadSHA: "aaaa111",
		}},
	}

	snapshot := BuildSnapshot(project, result, nil, nil, nil, nil)
	if len(snapshot.Branches) != 1 {
		t.Fatalf("branches = %+v, want the item's branch", snapshot.Branches)
	}
	branch := snapshot.Branches[0]
	if !branch.HasOpenCR {
		t.Fatalf("HasOpenCR = false for an open draft change request, want true: a draft is what a review starts in, and the epic must keep holding")
	}
	if branch.HasMergedCR {
		t.Fatalf("HasMergedCR = true for an unmerged draft, want false")
	}
}
