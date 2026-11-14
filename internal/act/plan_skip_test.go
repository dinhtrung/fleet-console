package act

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/decide"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// planSkipTick runs one applied tick whose plan carries the given skips, with no
// open change requests or dispatches, so only the skip wiring is exercised.
func planSkipTick(t *testing.T, ctx context.Context, st *store.Store, project store.ProjectRow, skips []decide.Skip, crs []forge.ChangeRequest) TickReport {
	t.Helper()
	now := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	fake := forge.NewFake(forge.KindGitHub)
	fake.RequestsValue = crs
	report, err := RunTick(ctx, TickInput{
		Store: st, Project: project, Forge: fake, Rig: &queueStub{}, Git: &fakeGit{},
		Gate: openGate(), Apply: true, CRs: crs, Plan: decide.Plan{Skips: skips},
		Now: func() time.Time { return now }, WorktreeRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	return report
}

// TestPlanSkipPrintsOncePerItemReasonAndHead is change-set task 3: a ready item
// the plan did not dispatch says why in the journal — the line the incident's
// `dispatches=0 skipped=0` was missing — once per (item, reason, head), so a
// perpetual skip is not repeated every tick and a changed fact prints again.
func TestPlanSkipPrintsOncePerItemReasonAndHead(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	project := store.ProjectRow{ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", Enabled: true}
	const head = "aaaa1111111111111111111111111111111a"
	crs := []forge.ChangeRequest{{
		Number: 20, ClosesItem: 5, State: "open", HeadSHA: head, Body: "Closes #5",
	}}
	skip := decide.Skip{Number: 5, Reason: decide.ReasonNoFreeBuilder, Detail: "every admitted seat is taken"}

	first := planSkipTick(t, ctx, st, project, []decide.Skip{skip}, crs)
	if !hasSkip(first, "#5 no_free_builder — every admitted seat is taken") {
		t.Fatalf("first tick skipped = %v, want the plan-skip line", first.Skipped)
	}

	// Same fact, next tick: the ledger holds it, so the line is silence.
	second := planSkipTick(t, ctx, st, project, []decide.Skip{skip}, crs)
	if hasSkip(second, "no_free_builder") {
		t.Fatalf("second tick skipped = %v, want no repeat of the same skip", second.Skipped)
	}

	// A changed reason is a new fact.
	changed := planSkipTick(t, ctx, st, project,
		[]decide.Skip{{Number: 5, Reason: decide.ReasonLiveRow, Detail: "qitem-1"}}, crs)
	if !hasSkip(changed, "#5 live_row — qitem-1") {
		t.Fatalf("changed tick skipped = %v, want the new reason", changed.Skipped)
	}

	// A moved head is a new fact for the same reason.
	moved := []forge.ChangeRequest{{
		Number: 20, ClosesItem: 5, State: "open", HeadSHA: "bbbb2222222222222222222222222222222b", Body: "Closes #5",
	}}
	again := planSkipTick(t, ctx, st, project, []decide.Skip{skip}, moved)
	if !hasSkip(again, "#5 no_free_builder") {
		t.Fatalf("moved-head tick skipped = %v, want the skip against the new head", again.Skipped)
	}

	// The ledger key is the fixed vocabulary the dedupe and the audit share:
	// both heads are on the ledger, one event each.
	var details []string
	events, err := st.EventsForItem(ctx, 5, 50)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	for _, event := range events {
		if event.Kind == "plan.skip" {
			details = append(details, event.Detail)
		}
	}
	joined := strings.Join(details, "\n")
	for _, want := range []string{"head=" + head, "head=bbbb2222222222222222222222222222222b", "reason=no_free_builder"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("plan.skip details = %q, want %q", joined, want)
		}
	}
}
