package act

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/store"
)

// TestRetentionDisabledWritesNothing (#395): a window of zero is retention off,
// not a run — no prune and no event.
func TestRetentionDisabledWritesNothing(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	old, err := st.AppendEvent(ctx, store.Event{Actor: "act", Kind: "test.event", Subject: "project:a", Detail: "d"})
	if err != nil {
		t.Fatalf("event: %v", err)
	}
	_ = old

	report, err := RetainEvents(ctx, RetentionInput{Store: st, KeepDays: 0, Apply: true})
	if err != nil {
		t.Fatalf("retain: %v", err)
	}
	if report.EventID != 0 || report.Deleted != 0 {
		t.Fatalf("report = %+v, want nothing done and nothing recorded", report)
	}
	page, err := st.Events(ctx, store.EventFilter{Kind: EventHousekeepingRetention, Limit: 5})
	if err != nil || len(page.Events) != 0 {
		t.Fatalf("retention events = %v (%v), want none", page.Events, err)
	}
}

// TestRetentionRecordsTheRun (#395): one housekeeping.retention event per run
// with the window, the counts and the dry-run marker — the audit's own answer.
func TestRetentionRecordsTheRun(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	if _, err := st.AppendEvent(ctx, store.Event{Actor: "act", Kind: "test.event", Subject: "project:a", Detail: "d"}); err != nil {
		t.Fatalf("event: %v", err)
	}

	report, err := RetainEvents(ctx, RetentionInput{
		Store: st, KeepDays: 90, Apply: false, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("retain: %v", err)
	}
	if report.EventID == 0 || report.Window != now.AddDate(0, 0, -90) {
		t.Fatalf("report = %+v, want the window and an event id", report)
	}
	page, err := st.Events(ctx, store.EventFilter{Kind: EventHousekeepingRetention, Limit: 5})
	if err != nil || len(page.Events) != 1 {
		t.Fatalf("retention events = %v (%v), want one", page.Events, err)
	}
	detail := page.Events[0].Detail
	for _, want := range []string{"window=90d", "scanned=", "deleted=", "kept_live=", "dry_run=true"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("detail = %q, want %q", detail, want)
		}
	}
}
