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

func failedStartInput(t *testing.T, running map[string]bool) (TickInput, *queueStub) {
	t.Helper()
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	st := newStore(t)
	pid := newProject(t, st)
	queue := &queueStub{item: &rig.QueueItem{ID: "qitem-1", State: "pending"}, created: &rig.CreateRequest{}}
	return TickInput{
		Store: st,
		Project: store.ProjectRow{
			ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig-a", ConcurrencyCap: 2, Enabled: true,
		},
		Forge: forge.NewFake(forge.KindGitHub), Rig: queue, Git: &fakeGit{}, Gate: openGate(), Apply: true,
		Items: []forge.Item{{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Five"}},
		Plan: decide.Plan{Status: decide.StatusPlanned, Intents: []decide.Intent{
			{Kind: decide.IntentDispatch, Item: 5, Seat: "builder@rig-a"},
		}},
		LifecycleFailed: map[string]string{"rig-a": "no rig spec at /x"},
		RunningSeats:    running,
		WorktreeRoot:    t.TempDir(), Now: func() time.Time { return now },
	}, queue
}

// TestFailedStartKeepsDispatchingToRunningSeats (#431): a rig whose start failed
// still has agents that are up — the failure is reported once, and the work is
// dispatched to the seats that exist instead of blanking the whole project.
func TestFailedStartKeepsDispatchingToRunningSeats(t *testing.T) {
	ctx := context.Background()
	in, queue := failedStartInput(t, map[string]bool{"builder@rig-a": true})

	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Dispatches) != 1 {
		t.Fatalf("dispatches = %+v, want the running seat to take item 5", report.Dispatches)
	}
	if queue.createCalls != 1 {
		t.Fatalf("queue creates = %d, want the dispatch row's queue item", queue.createCalls)
	}
	if !hasSkip(report, "rig rig-a up: no rig spec at /x") {
		t.Fatalf("skipped = %v, want the failure still named", report.Skipped)
	}
}

// TestFailedStartDoesNotDispatchToADeadSeat (#431): the same failure with no
// running seat is a refusal, not a dispatch to an agent that is not there.
func TestFailedStartDoesNotDispatchToADeadSeat(t *testing.T) {
	ctx := context.Background()
	in, queue := failedStartInput(t, nil)

	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Dispatches) != 0 || queue.createCalls != 0 {
		t.Fatalf("dispatches = %+v creates = %d, want none without a running seat", report.Dispatches, queue.createCalls)
	}
	if !hasSkip(report, "rig rig-a up: no rig spec at /x") {
		t.Fatalf("skipped = %v, want the failure named", report.Skipped)
	}
}
