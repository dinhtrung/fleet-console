package act

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/decide"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/mirror"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// TestEndToEndHandDispatchedItemMerges is task 3.1: the whole pipeline in the
// order the server runs it — the real mirror reads the rig and the forge, the
// real plan decides, then the real tick executes — for the reported failure.
//
// The item's queue row was created by hand (`rig queue create`), so the store
// holds no dispatch row for it. Before adoption the watchdog skipped the change
// request with "closes no dispatch" and the hand-dispatched item never merged.
// T1's unit test drives RunTick with a hand-built input; this drives the same
// path the server drives, so the mirror's own row filtering and the plan's own
// judgment are in the test.
func TestEndToEndHandDispatchedItemMerges(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	st := newStore(t)
	pid := newProject(t, st)

	const head = "abc1234"
	fake := forge.NewFake(forge.KindGitHub)
	fake.ItemsValue = []forge.Item{{
		Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Five",
	}}
	fake.RequestsValue = []forge.ChangeRequest{{
		Number: 9, HeadSHA: head, Branch: "feature/5-hand-dispatch",
		Mergeable: true, Body: "Closes #5", DocsOnly: true,
	}}
	fake.NotesValue = []forge.Comment{{
		ID: "n1", Body: "ACCEPT " + head, CreatedAt: "2026-10-29T11:00:00Z",
	}}

	// The hand-made queue row: it names this project's rig so the mirror keeps it,
	// and there is deliberately no dispatch row behind it.
	handRow := rig.QueueRow{
		ID: "qitem-hand-5", Source: "human@kernel", Destination: "dev-build-1@rig",
		State: "in-progress", PickupState: "working",
		Summary: "#5 hand-dispatched", Body: "#5 hand-dispatched",
	}

	// 1. The real mirror reads the rig and the forge for this project.
	m := &mirror.Mirror{
		Store: st,
		Rig:   rig.NewAdapter(rowJSONRunner{rows: []rig.QueueRow{handRow}}),
		Now:   func() time.Time { return at },
	}
	read, err := m.Tick(ctx, []mirror.Project{{
		ID: pid, Key: "app", Forge: fake, Rigs: []string{"rig"},
	}})
	if err != nil {
		t.Fatalf("mirror tick: %v", err)
	}
	if got := len(read.Projects["app"].Rows); got != 1 {
		t.Fatalf("mirror kept %d rows, want the hand-made row", got)
	}

	// 2. The real plan, from the pre-tick state: no verdict row exists yet.
	dispatches, err := st.DispatchesForProject(ctx, pid)
	if err != nil {
		t.Fatalf("dispatches: %v", err)
	}
	verdicts, err := st.VerdictsForProject(ctx, pid)
	if err != nil {
		t.Fatalf("verdicts: %v", err)
	}
	if len(dispatches) != 0 {
		t.Fatalf("dispatch rows = %d, want none: the row was created by hand", len(dispatches))
	}
	project := store.ProjectRow{
		ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r",
		Rig: "rig", ConcurrencyCap: 2, Enabled: true,
	}
	snapshot := mirror.BuildSnapshot(project, read.Projects["app"], dispatches, verdicts, nil, nil)
	plan := decide.PlanTick(snapshot, at)

	// 3. The real tick, with that plan and that read.
	in := TickInput{
		Store: st, Project: project, Forge: fake, Rig: &queueStub{}, Git: &fakeGit{},
		Gate: openGate(), Apply: true,
		Items: read.Projects["app"].Items, CRs: read.Projects["app"].CRs, Rows: read.Projects["app"].Rows,
		Plan: plan, WorktreeRoot: t.TempDir(), Now: func() time.Time { return at },
	}
	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}

	// The merge landed at the verdict's sha.
	if len(report.Merges) != 1 || !report.Merges[0].Merged {
		t.Fatalf("merges = %+v, want one; skipped = %v", report.Merges, report.Skipped)
	}
	if report.Merges[0].SHA != head {
		t.Fatalf("merged sha = %q, want %q", report.Merges[0].SHA, head)
	}
	if !hasWrite(fake.Writes, "merge", 9, head) {
		t.Fatalf("writes = %+v, want the forge merge at %s", fake.Writes, head)
	}

	// Task 3.2: the rendered skip list carries no "closes no dispatch" line. The
	// old code produced exactly "#5: change request #9 closes no dispatch", and
	// the tick is the only thing that renders this list.
	for _, skipped := range report.Skipped {
		if strings.Contains(skipped, "closes no dispatch") {
			t.Fatalf("old symptom is back in the report: %q", skipped)
		}
	}

	// The item closed and the marker cleared.
	closed := false
	for _, write := range fake.Writes {
		if write.Op == "close" && write.Number == 5 {
			closed = true
		}
	}
	if !closed {
		t.Fatalf("the item was never closed; writes = %+v", fake.Writes)
	}

	// Both events, adoption before merge (the ledger is newest-first).
	kinds := eventKindsOf(t, ctx, st)
	adoptedAt, mergeAt := indexOf(kinds, "change_request.adopted"), indexOf(kinds, "merge")
	if adoptedAt < 0 || mergeAt < 0 {
		t.Fatalf("events = %v, want change_request.adopted and merge", kinds)
	}
	if mergeAt > adoptedAt {
		t.Fatalf("merge recorded before adoption: %v", kinds)
	}

	// And the whole point of design D2: no dispatch row was invented.
	after, err := st.Dispatches(ctx, pid, 5)
	if err != nil {
		t.Fatalf("dispatches after: %v", err)
	}
	if len(after) != 0 {
		t.Fatalf("dispatch rows after the merge = %+v, want none", after)
	}
}

// rowJSONRunner serves QueueList from rows, so the real mirror can run without a
// rig binary. It answers only the read the mirror makes for the row filter; the
// forge is a fake, so no other rig call happens in this test.
type rowJSONRunner struct{ rows []rig.QueueRow }

func (r rowJSONRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	if len(args) >= 2 && args[0] == "queue" && args[1] == "list" {
		// The wire shape ParseQueueList reads, so the real decoder runs too.
		wire := make([]map[string]any, 0, len(r.rows))
		for _, row := range r.rows {
			wire = append(wire, map[string]any{
				"qitemId":            row.ID,
				"sourceSession":      row.Source,
				"destinationSession": row.Destination,
				"state":              row.State,
				"pickup":             map[string]string{"state": row.PickupState},
				"summary":            row.Summary,
				"body":               row.Body,
			})
		}
		return json.Marshal(wire)
	}
	return []byte("[]"), nil
}

func eventKindsOf(t *testing.T, ctx context.Context, st *store.Store) []string {
	t.Helper()
	events, err := st.RecentEvents(ctx, 100)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	return eventKinds(events)
}

func indexOf(values []string, want string) int {
	for index, value := range values {
		if value == want {
			return index
		}
	}
	return -1
}
