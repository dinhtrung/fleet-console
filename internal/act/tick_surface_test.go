package act

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/decide"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// TestTickNamesAPlanSkip (#402, #403 requirement 3): a plan skip that leaves a
// ready item undispatched reaches the tick's skip lines, and both the line and
// the ledger row are once per (item, reason, head) — "no intent, no skip" is
// what let three items sit, and a line repeated every tick is the noise the
// dedupe exists to stop.
func TestTickNamesAPlanSkip(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 13, 17, 0, 0, 0, time.UTC)
	st := newStore(t)
	pid := newProject(t, st)
	fake := forge.NewFake(forge.KindGitHub)
	queue := &queueStub{item: &rig.QueueItem{ID: "qitem-1", State: "pending"}}
	const firstHead = "aaaa1111111111111111111111111111111a"
	const movedHead = "bbbb2222222222222222222222222222222b"
	openCR := forge.ChangeRequest{Number: 20, ClosesItem: 5, State: "open", HeadSHA: firstHead, Body: "Closes #5"}
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
		CRs: []forge.ChangeRequest{openCR},
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

	// The same fact on the next tick is silence on both surfaces.
	second, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	if hasSkip(second, "#5 dispatch not taken") {
		t.Fatalf("tick 2 skipped = %v, want no repeat while reason and head are unchanged", second.Skipped)
	}
	page, err = st.Events(ctx, store.EventFilter{Kind: "dispatch.skip", Limit: 10})
	if err != nil || len(page.Events) != 1 {
		t.Fatalf("dispatch.skip after two ticks = %v (%v), want still one", page.Events, err)
	}

	// A moved head is a new fact for the same item and reason: the line comes
	// back, and so does one ledger row.
	movedCR := openCR
	movedCR.HeadSHA = movedHead
	in.CRs = []forge.ChangeRequest{movedCR}
	moved, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick 3: %v", err)
	}
	if !hasSkip(moved, "#5 dispatch not taken: no_free_builder") {
		t.Fatalf("tick 3 skipped = %v, want the skip named again at the moved head", moved.Skipped)
	}
	page, err = st.Events(ctx, store.EventFilter{Kind: "dispatch.skip", Limit: 10})
	if err != nil || len(page.Events) != 2 {
		t.Fatalf("dispatch.skip at the moved head = %v (%v), want two", page.Events, err)
	}
	var details []string
	for _, event := range page.Events {
		details = append(details, event.Detail)
	}
	joined := strings.Join(details, "\n")
	for _, want := range []string{"head=" + firstHead, "head=" + movedHead, "reason=no_free_builder;"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("dispatch.skip details = %q, want %q", joined, want)
		}
	}
}
