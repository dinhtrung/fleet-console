package mirror

import (
	"testing"

	"github.com/dinhtrung/fleet-console/internal/rig"
)

// TestFilterRowsKeepsHumanObligations pins the second reason a decision row never
// reached the snapshot: both live projects configure one rig, so a filter keyed
// only on the destination rig drops every @kernel row — including the blocked
// rows whose blocker is the operator.
func TestFilterRowsKeepsHumanObligations(t *testing.T) {
	rows := []rig.QueueRow{
		{ID: "q1", State: "in-progress", Destination: "dev-build-1@fleet-console"},
		{ID: "q2", State: "blocked", Destination: "operator-agent@kernel", BlockedOn: "operator-human@kernel"},
		{ID: "q3", State: "blocked", Destination: "advisor-lead@kernel", BlockedOn: "operator-human@kernel"},
		{ID: "q4", State: "in-progress", Destination: "dev-build-android-1@thuocla-android"},
	}

	kept := filterRows(rows, []string{"fleet-console"})
	got := make(map[string]bool, len(kept))
	for _, row := range kept {
		got[row.ID] = true
	}

	if !got["q1"] {
		t.Fatal("the project's own rig row was dropped")
	}
	for _, id := range []string{"q2", "q3"} {
		if !got[id] {
			t.Fatalf("%s was dropped: a row a human owes an answer for is the project's business", id)
		}
	}
	if got["q4"] {
		t.Fatal("another project's rig row was kept")
	}
	if len(kept) != 3 {
		t.Fatalf("kept %d rows, want 3 (%v)", len(kept), got)
	}
}
