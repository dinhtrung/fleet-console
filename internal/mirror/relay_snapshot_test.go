package mirror

import (
	"context"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/decide"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// relayProject creates the project a relay test plans against: one mirrored
// item, one open change request whose head the reviewer row names, and a
// project row that either names a relay mode or does not.
func relayProject(t *testing.T, st *store.Store, relayMode, relaySeat string) store.ProjectRow {
	t.Helper()
	ctx := context.Background()
	if _, _, err := st.UpsertProject(ctx, store.UpsertInput{
		Project: store.ProjectInput{
			Key: "app", Name: "app", ForgeKind: "github", ForgeProject: "o/r",
			Rig: "rig", ConcurrencyCap: 2, Enabled: true,
			RelayMode: relayMode, RelaySeat: relaySeat,
		},
		SyncRelay: true,
	}); err != nil {
		t.Fatalf("project: %v", err)
	}
	projects, err := st.Projects(ctx)
	if err != nil {
		t.Fatalf("projects: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("projects = %d, want 1", len(projects))
	}
	return projects[0]
}

// relayRead is the tick's reads for that project: the item, the open change
// request and the reviewer row that ended without writing a verdict.
func relayRead(head string) ProjectResult {
	return ProjectResult{
		Items: []forge.Item{{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Five"}},
		CRs: []forge.ChangeRequest{{
			Number: 20, ClosesItem: 5, State: "open", HeadSHA: head,
		}},
		Rows: []rig.QueueRow{{
			ID: "q-1", State: "done", Destination: "dev-review@rig",
			Body: "review #5 change request #20 head " + head,
		}},
	}
}

// TestRelayPlansFromARealSnapshot is #397's acceptance test: RelayMode reaches
// the decide input through BuildSnapshot, and a project whose reviewer row
// ended without a verdict plans the relay — from a snapshot built by
// BuildSnapshot, not by hand.
func TestRelayPlansFromARealSnapshot(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	const head = "aaaa1111111111111111111111111111111a"
	project := relayProject(t, st, "scribe", "scribe@rig")

	relayed, err := st.RelayedHeads(ctx, project.ID)
	if err != nil {
		t.Fatalf("relayed heads: %v", err)
	}
	snapshot := BuildSnapshot(project, relayRead(head), nil, nil, nil, relayed)
	if snapshot.Project.RelayMode != "scribe" {
		t.Fatalf("snapshot relay mode = %q, want the project row's scribe", snapshot.Project.RelayMode)
	}
	plan := decide.PlanTick(snapshot, planNow)
	relays := planRelays(t, plan)
	if len(relays) != 1 {
		t.Fatalf("relays = %d, want 1 from a real snapshot", len(relays))
	}
	if relays[0].Item != 5 || relays[0].ChangeRequest != 20 || relays[0].HeadSHA != head {
		t.Fatalf("relay = %+v, want item 5 cr 20 head %s", relays[0], head)
	}
}

// TestRelayOffWithoutModeFromARealSnapshot is the other half: the same project
// with no mode plans no relay, and the decision arms are unchanged.
func TestRelayOffWithoutModeFromARealSnapshot(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	const head = "aaaa1111111111111111111111111111111a"
	project := relayProject(t, st, "", "")

	relayed, err := st.RelayedHeads(ctx, project.ID)
	if err != nil {
		t.Fatalf("relayed heads: %v", err)
	}
	snapshot := BuildSnapshot(project, relayRead(head), nil, nil, nil, relayed)
	if snapshot.Project.RelayMode != "" {
		t.Fatalf("snapshot relay mode = %q, want empty", snapshot.Project.RelayMode)
	}
	plan := decide.PlanTick(snapshot, planNow)
	if relays := planRelays(t, plan); len(relays) != 0 {
		t.Fatalf("relays = %+v, want none: the project never opted in", relays)
	}
}

// TestRelayedHeadsComeFromTheLedger: the second tick over one head plans no
// second relay, because the ledger's verdict.relayed event reaches the plan
// through the snapshot's fold — not through plan memory.
func TestRelayedHeadsComeFromTheLedger(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	const head = "aaaa1111111111111111111111111111111a"
	project := relayProject(t, st, "scribe", "scribe@rig")
	if err := st.ReplaceItems(ctx, project.ID, []store.ItemRecord{{
		Number: 5, Kind: "task", State: "open", Title: "Five", BlockedByJSON: "[]",
	}}); err != nil {
		t.Fatalf("items: %v", err)
	}
	// The act layer's own write, in its exact vocabulary (`head=<sha>; row <id>`).
	if _, err := st.AppendEvent(ctx, store.Event{
		Actor: "act", Kind: "verdict.relayed", Subject: "#5", Detail: "head=" + head + "; row q-9",
	}); err != nil {
		t.Fatalf("event: %v", err)
	}

	relayed, err := st.RelayedHeads(ctx, project.ID)
	if err != nil {
		t.Fatalf("relayed heads: %v", err)
	}
	if len(relayed) != 1 || relayed[0].Item != 5 || relayed[0].Head != head {
		t.Fatalf("relayed heads = %+v, want #5 at %s", relayed, head)
	}
	snapshot := BuildSnapshot(project, relayRead(head), nil, nil, nil, relayed)
	if relays := planRelays(t, decide.PlanTick(snapshot, planNow)); len(relays) != 0 {
		t.Fatalf("relays = %+v, want none: the ledger already holds this head", relays)
	}
}

var planNow = time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)

// planRelays filters the plan's relay intents.
func planRelays(t *testing.T, plan decide.Plan) []decide.Intent {
	t.Helper()
	var out []decide.Intent
	for _, intent := range plan.Intents {
		if intent.Kind == decide.IntentRelayVerdict {
			out = append(out, intent)
		}
	}
	return out
}
