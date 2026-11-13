package act

import (
	"context"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/decide"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// TestTickNamesAPlanSkip (#402): a plan skip that leaves a ready item
// undispatched reaches the tick's skip lines, and the ledger records it once
// per (item, reason) — "no intent, no skip" is what let three items sit.
func TestTickNamesAPlanSkip(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 13, 17, 0, 0, 0, time.UTC)
	st := newStore(t)
	pid := newProject(t, st)
	fake := forge.NewFake(forge.KindGitHub)
	queue := &queueStub{item: &rig.QueueItem{ID: "qitem-1", State: "pending"}}
	in := TickInput{
		Store: st,
		Project: store.ProjectRow{
			ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", Enabled: true,
		},
		Forge: fake, Rig: queue, Git: &fakeGit{}, Gate: openGate(), Apply: true,
		Plan: decide.Plan{Status: decide.StatusNothing, Skips: []decide.Skip{
			{Number: 5, Reason: decide.ReasonNoFreeBuilder},
			{Number: 6, Reason: decide.ReasonAlreadyDispatched},
		}},
		Now: func() time.Time { return now },
	}

	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if !hasSkip(report, "#5 dispatch not taken: no_free_builder") {
		t.Fatalf("skipped = %v, want the refusal named", report.Skipped)
	}
	if hasSkip(report, "#6 dispatch not taken") {
		t.Fatalf("skipped = %v, want an in-flight item kept quiet", report.Skipped)
	}
	page, err := st.Events(ctx, store.EventFilter{Kind: "dispatch.skip", Limit: 10})
	if err != nil || len(page.Events) != 1 {
		t.Fatalf("dispatch.skip = %v (%v), want one", page.Events, err)
	}
	if page.Events[0].Subject != "#5" {
		t.Fatalf("event subject = %q, want #5", page.Events[0].Subject)
	}

	// A second tick repeats the line but not the ledger row.
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	page, err = st.Events(ctx, store.EventFilter{Kind: "dispatch.skip", Limit: 10})
	if err != nil || len(page.Events) != 1 {
		t.Fatalf("dispatch.skip after two ticks = %v (%v), want still one", page.Events, err)
	}
}
