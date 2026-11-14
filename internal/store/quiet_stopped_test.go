package store

import (
	"context"
	"path/filepath"
	"testing"
)

// TestQuietStoppedRigsReadsTheNewestLifecycleEvent: the app's own stop is
// provenance for a stopped rig, and the newest event wins — a start after a stop
// means the rig is not ours to revive any more (#403).
func TestQuietStoppedRigsReadsTheNewestLifecycleEvent(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "fleet.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer st.Close()

	event := func(kind, subject, detail string) {
		t.Helper()
		if _, err := st.AppendEvent(ctx, Event{Actor: "act", Kind: kind, Subject: subject, Detail: detail}); err != nil {
			t.Fatalf("event %s %s: %v", kind, subject, err)
		}
	}

	event("rig.started", "quiet-then-started", "ta")
	event("rig.stopped", "quiet-then-started", "quiet 12m0s")
	event("rig.started", "quiet-then-started", "2 ready item(s)")
	event("rig.started", "never-stopped", "daemon down; 1 ready item(s)")
	event("rig.started", "stopped-by-app", "ta")
	event("rig.stopped", "stopped-by-app", "quiet 11m0s")

	// An unrelated kind must not stand in for a lifecycle event.
	event("decision.stale", "other", "unrelated")

	got, err := st.QuietStoppedRigs(ctx)
	if err != nil {
		t.Fatalf("quiet stopped rigs: %v", err)
	}
	if len(got) != 1 || !got["stopped-by-app"] {
		t.Fatalf("quiet stopped = %+v, want only stopped-by-app", got)
	}
}
