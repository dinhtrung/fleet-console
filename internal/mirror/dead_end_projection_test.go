package mirror

import (
	"testing"

	"github.com/dinhtrung/fleet-console/internal/git"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// TestBuildSnapshotCarriesTheDeadEndFacts projects the three facts the
// dead-end rule reads (#526): the branch head origin reported, the head an
// ended attempt stopped at, and the resolution of its queue row. Each comes
// from a different read — git, the dispatch table, the queue — and a dropped
// field would silently restore the re-dispatch loop.
func TestBuildSnapshotCarriesTheDeadEndFacts(t *testing.T) {
	project := store.ProjectRow{ID: 1, Key: "app", ForgeKind: "github", Enabled: true}
	result := ProjectResult{
		Branches: []git.Branch{{Name: "feature/5-five", Item: 5, Head: "aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111"}},
		Rows: []rig.QueueRow{{
			ID: "qitem-1", State: "done", PickupState: "terminal", Destination: "build@rig",
			Resolution: "blocked: needs a deployed instance", CreatedAt: "2026-11-18T04:00:00Z",
		}},
	}
	dispatches := []store.DispatchRow{{
		ID: 1, ProjectID: 1, ItemNumber: 5, Attempt: 1, Seat: "build@rig",
		Branch: "feature/5-five", QItemID: "qitem-1", CreatedAt: "2026-11-18T03:00:00Z",
		EndReason: "superseded", EndHead: "aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111",
	}}

	snapshot := BuildSnapshot(project, result, dispatches, nil, nil, nil)

	if len(snapshot.Branches) != 1 || snapshot.Branches[0].Head == "" {
		t.Fatalf("branches = %+v, want origin's head on the branch", snapshot.Branches)
	}
	if len(snapshot.Dispatches) != 1 || snapshot.Dispatches[0].EndHead == "" {
		t.Fatalf("dispatches = %+v, want the head the attempt stopped at", snapshot.Dispatches)
	}
	if len(snapshot.Rows) != 1 || snapshot.Rows[0].Resolution == "" {
		t.Fatalf("rows = %+v, want the row's resolution", snapshot.Rows)
	}
}
