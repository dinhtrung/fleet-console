package act

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

func attentionSeat(seat, question string) []rig.Seat {
	return []rig.Seat{{
		Session: seat, Rig: "rig-a", Lifecycle: rig.LifecycleRunning,
		Attention: []rig.SeatAttention{{Seat: seat, Rig: "rig-a", Kind: rig.AttentionKindAttention, Question: question}},
	}}
}

func attentionEvents(t *testing.T, st *store.Store, kind string) []store.EventRow {
	t.Helper()
	page, err := st.Events(context.Background(), store.EventFilter{Kind: kind, Limit: 20})
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	return page.Events
}

func newAttention(t *testing.T, st *store.Store, now time.Time) (*AttentionCoordinator, *[]AttentionNotice) {
	t.Helper()
	notices := []AttentionNotice{}
	return &AttentionCoordinator{
		Store: st, Now: func() time.Time { return now },
		Notify: func(notice AttentionNotice) { notices = append(notices, notice) },
	}, &notices
}

// TestAttentionRaisedOncePerQuestion (#427): five ticks reading the same seat and
// question write one obligation and one notice, never five.
func TestAttentionRaisedOncePerQuestion(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	coordinator, notices := newAttention(t, st, now)
	seats := attentionSeat("dev-review@rig-a", "Approve the merge?")

	for tick := 1; tick <= 5; tick++ {
		if err := coordinator.Observe(ctx, seats, true); err != nil {
			t.Fatalf("tick %d: %v", tick, err)
		}
	}
	if raised := attentionEvents(t, st, EventAttentionRaised); len(raised) != 1 {
		t.Fatalf("raised = %+v, want one obligation over five ticks", raised)
	}
	if len(*notices) != 1 || (*notices)[0].Resolved {
		t.Fatalf("notices = %+v, want one raise", *notices)
	}
	if (*notices)[0].Question != "Approve the merge?" {
		t.Fatalf("notice = %+v, want the question carried", (*notices)[0])
	}
}

// TestAttentionResolvedByAnObservedRead (#427): the first successful read that
// no longer reports the question writes one resolution and clears the notice.
func TestAttentionResolvedByAnObservedRead(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	coordinator, notices := newAttention(t, st, now)
	seats := attentionSeat("dev-review@rig-a", "Approve the merge?")

	if err := coordinator.Observe(ctx, seats, true); err != nil {
		t.Fatalf("raise: %v", err)
	}
	if err := coordinator.Observe(ctx, nil, true); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := coordinator.Observe(ctx, nil, true); err != nil {
		t.Fatalf("repeat: %v", err)
	}
	if resolved := attentionEvents(t, st, EventAttentionResolved); len(resolved) != 1 {
		t.Fatalf("resolved = %+v, want one resolution", resolved)
	}
	if len(*notices) != 2 || !(*notices)[1].Resolved {
		t.Fatalf("notices = %+v, want a raise and one resolve", *notices)
	}
	if !strings.Contains((*notices)[1].Seat, "dev-review") {
		t.Fatalf("resolved notice = %+v, want the seat named", (*notices)[1])
	}
}

// TestAttentionFailedReadProvesNothing (#427): while a question stands, an
// unreadable read raises nothing and resolves nothing.
func TestAttentionFailedReadProvesNothing(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	coordinator, notices := newAttention(t, st, now)

	if err := coordinator.Observe(ctx, attentionSeat("dev-review@rig-a", "Approve?"), false); err != nil {
		t.Fatalf("failed read: %v", err)
	}
	if raised := attentionEvents(t, st, EventAttentionRaised); len(raised) != 0 {
		t.Fatalf("raised = %+v, want nothing from a read that proves nothing", raised)
	}

	// Raise it, then fail the read: the obligation stands untouched.
	if err := coordinator.Observe(ctx, attentionSeat("dev-review@rig-a", "Approve?"), true); err != nil {
		t.Fatalf("raise: %v", err)
	}
	if err := coordinator.Observe(ctx, nil, false); err != nil {
		t.Fatalf("failed read: %v", err)
	}
	if resolved := attentionEvents(t, st, EventAttentionResolved); len(resolved) != 0 {
		t.Fatalf("resolved = %+v, want nothing resolved by a failed read", resolved)
	}
	if len(*notices) != 1 {
		t.Fatalf("notices = %+v, want only the raise", *notices)
	}
}

// TestAttentionChangedQuestionIsANewObligation (#427): a different question on
// the same seat is a new obligation — the old one resolves and the new one
// raises, because the fingerprint is the question's hash.
func TestAttentionChangedQuestionIsANewObligation(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	coordinator, _ := newAttention(t, st, now)

	if err := coordinator.Observe(ctx, attentionSeat("dev-review@rig-a", "Approve?"), true); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := coordinator.Observe(ctx, attentionSeat("dev-review@rig-a", "Allow the seat to start?"), true); err != nil {
		t.Fatalf("second: %v", err)
	}
	if raised := attentionEvents(t, st, EventAttentionRaised); len(raised) != 2 {
		t.Fatalf("raised = %+v, want two obligations", raised)
	}
	if resolved := attentionEvents(t, st, EventAttentionResolved); len(resolved) != 1 {
		t.Fatalf("resolved = %+v, want the old question resolved", resolved)
	}
}
