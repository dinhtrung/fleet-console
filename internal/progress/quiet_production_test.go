package progress

import (
	"testing"

	"github.com/dinhtrung/fleet-console/internal/decide"
	"github.com/dinhtrung/fleet-console/internal/rig"
)

// productionDecisionRow is the shape the live board serves: blocked, an agent
// destination, and a human blocker. Captured from /api/decisions on 2026-11-04,
// where both open decisions looked exactly like this.
func productionDecisionRow(id, destination, blockedOn, summary string) rig.QueueRow {
	return rig.QueueRow{
		ID:          id,
		State:       "blocked",
		Destination: destination,
		BlockedOn:   blockedOn,
		Summary:     summary,
		Body:        summary,
	}
}

// TestQuietIsFalseForABlockedOnHumanRow is the scenario the reject named: "A
// waiting decision is never quiet". The row's destination is an agent seat, so a
// predicate reading only Destination calls this board quiet while the board
// itself is serving the decision to the operator.
func TestQuietIsFalseForABlockedOnHumanRow(t *testing.T) {
	read := quietRead()
	projectRead := read.Projects["fleet-console"]
	projectRead.Rows = []rig.QueueRow{
		productionDecisionRow("qitem-1", "operator-agent@kernel", "operator-human@kernel",
			"Release gate: do not tag android-v1.2.0 until the smoke test passes"),
	}
	read.Projects["fleet-console"] = projectRead

	snapshot := Build(read, nil, testProject(), testConfig(), Roster{Plan: &decide.Plan{}})
	if snapshot.Counts.OpenDecisions != 1 {
		t.Fatalf("OpenDecisions = %d, want 1 for a blocked-on-human row", snapshot.Counts.OpenDecisions)
	}
	if snapshot.Quiet() {
		t.Fatal("Quiet() = true while the board holds a decision waiting on the operator")
	}
}

// TestQuietIgnoresABlockedRowWaitingOnAnAgent is the control: a blocked row whose
// blocker is another agent is not a human obligation, so it must not keep the
// notice alive.
func TestQuietIgnoresABlockedRowWaitingOnAnAgent(t *testing.T) {
	read := quietRead()
	projectRead := read.Projects["fleet-console"]
	projectRead.Rows = []rig.QueueRow{
		productionDecisionRow("qitem-2", "dev-build-1@fleet-console", "dev-build-2@fleet-console",
			"waiting on another builder"),
	}
	read.Projects["fleet-console"] = projectRead

	snapshot := Build(read, nil, testProject(), testConfig(), Roster{Plan: &decide.Plan{}})
	if snapshot.Counts.OpenDecisions != 0 {
		t.Fatalf("OpenDecisions = %d, want 0 for an agent blocker", snapshot.Counts.OpenDecisions)
	}
	if !snapshot.Quiet() {
		t.Fatalf("Quiet() = false with no human obligation; counts = %+v", snapshot.Counts)
	}
}

// TestQuietIsFalseForTheLiveBoardShape replays the two rows the live board served
// on 2026-11-04 through the decoder and the predicate, with no stubbing between
// them. It is the probe the reject asked for, kept as a regression: the rows are
// `blocked`, their destinations are @kernel seats, and their blocker is the
// operator, so a quiet snapshot here would page nobody while the board waits.
func TestQuietIsFalseForTheLiveBoardShape(t *testing.T) {
	const wire = `[
	  {"qitemId":"qitem-20261104012716-36bbd84f","sourceSession":"dev-review@thuocla-android",
	   "destinationSession":"advisor-lead@kernel","state":"blocked","blockedOn":"operator-human@kernel",
	   "pickup":{"state":"working"},"summary":"Release gate: do not tag android-v1.2.0 until the smoke test passes",
	   "tsCreated":"2026-11-04T01:27:16Z"},
	  {"qitemId":"qitem-20261104014517-6e808968","sourceSession":"dev-review@thuocla-android",
	   "destinationSession":"operator-agent@kernel","state":"blocked","blockedOn":"operator-human@kernel",
	   "pickup":{"state":"working"},"summary":"Row S3 (Room bridge, tasks 3.1-3.5) needs a builder",
	   "tsCreated":"2026-11-04T01:45:17Z"}
	]`

	rows, err := rig.ParseQueueList([]byte(wire), nil)
	if err != nil {
		t.Fatalf("decode the recorded board rows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("decoded %d rows, want 2", len(rows))
	}
	for _, row := range rows {
		if row.BlockedOn != "operator-human@kernel" {
			t.Fatalf("row %s BlockedOn = %q, want the human blocker decoded", row.ID, row.BlockedOn)
		}
	}

	read := quietRead()
	projectRead := read.Projects["fleet-console"]
	projectRead.Rows = rows
	read.Projects["fleet-console"] = projectRead

	snapshot := Build(read, nil, testProject(), testConfig(), Roster{Plan: &decide.Plan{}})
	if snapshot.Counts.OpenDecisions != 2 {
		t.Fatalf("OpenDecisions = %d, want 2", snapshot.Counts.OpenDecisions)
	}
	if snapshot.Quiet() {
		t.Fatal("Quiet() = true for the live board shape, which serves two open decisions")
	}
}
