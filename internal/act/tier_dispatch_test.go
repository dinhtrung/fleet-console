package act

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/decide"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/mirror"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// fleetRunner serves both reads the tier decision needs: the queue (`queue list`) and the roster
// (`ps --nodes`). The wire shapes are the ones the real decoders read, so the decode path runs too.
type fleetRunner struct {
	rows  []rig.QueueRow
	seats []rig.Seat
}

func (r fleetRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	if len(args) >= 2 && args[0] == "queue" && args[1] == "list" {
		wire := make([]map[string]any, 0, len(r.rows))
		for _, row := range r.rows {
			wire = append(wire, map[string]any{
				"qitemId":            row.ID,
				"sourceSession":      row.Source,
				"destinationSession": row.Destination,
				"state":              row.State,
				"blockedOn":          row.BlockedOn,
				"pickup":             map[string]string{"state": row.PickupState},
				"summary":            row.Summary,
				"body":               row.Body,
				"tsCreated":          row.CreatedAt,
			})
		}
		return json.Marshal(wire)
	}
	if len(args) >= 2 && args[0] == "ps" && args[1] == "--nodes" {
		wire := make([]map[string]any, 0, len(r.seats))
		for _, seat := range r.seats {
			wire = append(wire, map[string]any{
				"nodeId":               seat.NodeID,
				"rigId":                seat.RigID,
				"rigName":              seat.Rig,
				"canonicalSessionName": seat.Session,
				"logicalId":            seat.LogicalID,
				"lifecycleState":       seat.Lifecycle,
				"runtime":              seat.Runtime,
			})
		}
		return json.Marshal(map[string]any{"entries": wire})
	}
	return []byte("[]"), nil
}

// builderSeat is one running builder on a rig, in the shape the mirror keeps. LogicalID contains
// "build" because the mirror keeps only builder seats as dispatchable.
func builderSeat(session, rigName string) rig.Seat {
	return rig.Seat{
		NodeID: "node-" + session, RigID: "rig-" + rigName, Rig: rigName,
		Session: session, LogicalID: "dev.build-1." + strings.Split(session, "@")[0],
		Lifecycle: "running", Runtime: "omp",
	}
}

// tierProject is one project declaring two tiers: primary at tier 0, standby at tier 1.
func tierProject(pid int64, spill string) store.ProjectRow {
	return store.ProjectRow{
		ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r",
		Rig: "primary", Rigs: []string{"primary", "standby"},
		RigTiers: []store.RigTier{
			{Name: "primary", Tier: 0, Spill: "on-full"},
			{Name: "standby", Tier: 1, Spill: spill},
		},
		ConcurrencyCap: 1, Enabled: true,
	}
}

func tierProjectInput(spill string) store.ProjectInput {
	return store.ProjectInput{
		Key: "app", Name: "app", ForgeKind: "github", ForgeProject: "o/r",
		Rig: "primary", Rigs: []string{"primary", "standby"},
		RigTiers: []store.RigTier{
			{Name: "primary", Tier: 0, Spill: "on-full"},
			{Name: "standby", Tier: 1, Spill: spill},
		},
		ConcurrencyCap: 1, Enabled: true,
	}
}

// readableFleet is the roster the mirror keeps: a running builder on each rig, with tier 0's seat
// busy on another item so the plan has to spill. It is the "tier 0 full, tier 1 free" precondition,
// expressed as the facts the plan reads rather than as a flag.
func readableFleet(at time.Time) ([]rig.QueueRow, []rig.Seat) {
	busy := rig.QueueRow{
		ID: "qitem-busy", Source: "human@kernel", Destination: "dev-build-1@primary",
		State: "in-progress", PickupState: "working",
		Summary: "#9 other work", Body: "#9 other work", CreatedAt: at.Format(time.RFC3339),
	}
	return []rig.QueueRow{busy}, []rig.Seat{
		builderSeat("dev-build-1@primary", "primary"),
		builderSeat("dev-build-2@standby", "standby"),
	}
}

// openTierRead performs the real mirror read and returns the plan the tick would act on.
func openTierRead(t *testing.T, ctx context.Context, st *store.Store, pid int64, fake forge.Forge, project store.ProjectRow, at time.Time) decide.Plan {
	t.Helper()
	rows, seats := readableFleet(at)
	m := &mirror.Mirror{
		Store: st,
		Rig:   rig.NewAdapter(fleetRunner{rows: rows, seats: seats}),
		Now:   func() time.Time { return at },
	}
	read, err := m.Tick(ctx, []mirror.Project{{
		ID: pid, Key: "app", Forge: fake, Rigs: []string{"primary", "standby"},
	}})
	if err != nil {
		t.Fatalf("mirror tick: %v", err)
	}
	dispatches, err := st.DispatchesForProject(ctx, pid)
	if err != nil {
		t.Fatalf("dispatches: %v", err)
	}
	verdicts, err := st.VerdictsForProject(ctx, pid)
	if err != nil {
		t.Fatalf("verdicts: %v", err)
	}
	snapshot := mirror.BuildSnapshot(project, read.Projects["app"], dispatches, verdicts)
	return decide.PlanTick(snapshot, at)
}

// Tasks 3.1 and 3.2 end to end: two rigs with tiers, tier 0 full at the cap, tier 1 free. The real
// mirror reads the roster, the real plan fills by tier, and the real tick writes one row on the
// tier-1 seat with the worktree layout unchanged.
func TestTierDispatchEndToEnd(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	st := newStore(t)
	pid, err := st.CreateProject(ctx, tierProjectInput("on-full"))
	if err != nil {
		t.Fatalf("project: %v", err)
	}

	fake := forge.NewFake(forge.KindGitHub)
	fake.ItemsValue = []forge.Item{{
		Number: 7, Kind: forge.KindTask, State: forge.StateOpen, Title: "Pool store", Body: "body",
	}}

	project := tierProject(pid, "on-full")
	plan := openTierRead(t, ctx, st, pid, fake, project, at)

	// The plan chose tier 1's seat, and only that one.
	if len(plan.Intents) != 1 {
		t.Fatalf("intents = %+v, want exactly one: tier 0 is full and tier 1 is free", plan.Intents)
	}
	if seat := plan.Intents[0].Seat; seat != "dev-build-2@standby" {
		t.Fatalf("plan seat = %q, want the tier-1 standby seat", seat)
	}

	root := t.TempDir()
	git := &fakeGit{}
	queue := &queueStub{
		item:    &rig.QueueItem{ID: "qitem-tier", State: "pending"},
		created: &rig.CreateRequest{},
	}
	report, err := RunTick(ctx, TickInput{
		Store: st, Project: project, Forge: fake, Rig: queue, Git: git,
		Gate: openGate(), Apply: true,
		Items: fake.ItemsValue, Plan: plan, WorktreeRoot: root,
		Now: func() time.Time { return at },
	})
	if err != nil {
		t.Fatalf("tick: %v", err)
	}

	// Task 3.2: one dispatch, one row, on the tier-1 seat.
	if len(report.Dispatches) != 1 {
		t.Fatalf("dispatches = %+v, want one", report.Dispatches)
	}
	if queue.createCalls != 1 {
		t.Fatalf("queue create calls = %d, want one", queue.createCalls)
	}
	// Task 3.1: the row request's destination is <seat>@<rig> for the chosen tier's seat. This is
	// what the fake rig recorded, not a value the test computed.
	if got := queue.created.Destination; got != "dev-build-2@standby" {
		t.Fatalf("row destination = %q, want the tier-1 seat's <seat>@<rig>", got)
	}
	if !strings.Contains(queue.created.Destination, "@standby") {
		t.Fatalf("row destination %q does not name the tier-1 rig", queue.created.Destination)
	}

	// Task 3.1: the worktree layout is unchanged — wt-<key>-<n>, with no per-rig directory.
	wantWorktree := filepath.Join(root, "wt-app-7")
	gotWorktree := report.Dispatches[0].Worktree
	if gotWorktree != wantWorktree {
		t.Fatalf("worktree = %q, want %q", gotWorktree, wantWorktree)
	}
	if len(git.worktreePaths) != 1 || git.worktreePaths[0] != wantWorktree {
		t.Fatalf("git asked for worktrees %v, want exactly %q", git.worktreePaths, wantWorktree)
	}
	if strings.Contains(gotWorktree, "standby") || strings.Contains(gotWorktree, "primary") {
		t.Fatalf("worktree %q carries a rig name; the layout must not be per-rig", gotWorktree)
	}
	if got := report.Dispatches[0].Branch; got != "feature/7-pool-store" {
		t.Fatalf("branch = %q", got)
	}
}

// Task 3.2's journal line: a tier that refuses the dispatch names the tier and the trigger, and the
// plan's skip list is where the tick prints it.
func TestTierDispatchRefusalIsJournaled(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	st := newStore(t)
	pid, err := st.CreateProject(ctx, tierProjectInput("on-unavailable"))
	if err != nil {
		t.Fatalf("project: %v", err)
	}

	fake := forge.NewFake(forge.KindGitHub)
	fake.ItemsValue = []forge.Item{{
		Number: 7, Kind: forge.KindTask, State: forge.StateOpen, Title: "Pool store", Body: "body",
	}}

	project := tierProject(pid, "on-unavailable")
	plan := openTierRead(t, ctx, st, pid, fake, project, at)

	// Tier 0 is running (so on-unavailable is not satisfied) and its seat is busy, so nothing may
	// dispatch and the standby must stay cold.
	if len(plan.Intents) != 0 {
		t.Fatalf("intents = %+v, want none: the standby is on-unavailable and tier 0 is running", plan.Intents)
	}

	var found bool
	for _, skip := range plan.Skips {
		if skip.Reason != decide.ReasonNoFreeBuilder {
			continue
		}
		found = true
		if !strings.Contains(skip.Detail, "tier 1") || !strings.Contains(skip.Detail, "spill=on-unavailable") {
			t.Fatalf("skip detail = %q, want the tier and the trigger named", skip.Detail)
		}
	}
	if !found {
		t.Fatalf("skips = %+v, want one %s", plan.Skips, decide.ReasonNoFreeBuilder)
	}

	// The tick writes nothing for it, and the skip is what the operator reads in the journal.
	report, err := RunTick(ctx, TickInput{
		Store: st, Project: project, Forge: fake, Rig: &queueStub{}, Git: &fakeGit{},
		Gate: openGate(), Apply: true,
		Items: fake.ItemsValue, Plan: plan, WorktreeRoot: t.TempDir(),
		Now: func() time.Time { return at },
	})
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Dispatches) != 0 {
		t.Fatalf("dispatches = %+v, want none", report.Dispatches)
	}
}
