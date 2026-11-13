package store

import (
	"context"
	"testing"
	"time"
)

func backdateEvent(t *testing.T, st *Store, id int64, at time.Time) {
	t.Helper()
	if _, err := st.db.ExecContext(context.Background(),
		`UPDATE event SET at = ? WHERE id = ?`, at.UTC().Format(time.RFC3339Nano), id); err != nil {
		t.Fatalf("backdate: %v", err)
	}
}

func appendEvent(t *testing.T, st *Store, subject string) int64 {
	t.Helper()
	id, err := st.AppendEvent(context.Background(), Event{Actor: "act", Kind: "test.event", Subject: subject, Detail: "d"})
	if err != nil {
		t.Fatalf("event: %v", err)
	}
	return id
}

func eventSubjects(t *testing.T, st *Store) []string {
	t.Helper()
	page, err := st.Events(context.Background(), EventFilter{Limit: 100})
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	out := make([]string, 0, len(page.Events))
	for _, event := range page.Events {
		out = append(out, event.Subject)
	}
	return out
}

// TestPruneEventsByWindow (#395): rows older than the cutoff go, newer rows
// stay, and the counts say so.
func TestPruneEventsByWindow(t *testing.T) {
	ctx := context.Background()
	st := upsertStore(t)
	now := time.Now().UTC()
	old := appendEvent(t, st, "project:a")
	backdateEvent(t, st, old, now.AddDate(0, 0, -100))
	fresh := appendEvent(t, st, "project:b")

	counts, err := st.PruneEvents(ctx, now.AddDate(0, 0, -90), 500, true)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if counts.Scanned != 1 || counts.Deleted != 1 || counts.KeptLive != 0 {
		t.Fatalf("counts = %+v, want one scanned and deleted", counts)
	}
	subjects := eventSubjects(t, st)
	if len(subjects) != 1 || subjects[0] != "project:b" {
		t.Fatalf("subjects = %v, want only the fresh row (%d kept)", subjects, fresh)
	}
}

// TestPruneEventsKeepsLiveWork (#395): an item with a live dispatch and a
// project whose queue has an unresolved row keep their rows whatever their age;
// the rest of the window still moves.
func TestPruneEventsKeepsLiveWork(t *testing.T) {
	ctx := context.Background()
	st := upsertStore(t)
	_, pid, err := st.UpsertProject(ctx, baseUpsert())
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	now := time.Now().UTC()
	if _, err := st.CreateDispatch(ctx, NewDispatch{
		ProjectID: pid, ItemNumber: 5, Attempt: 1, Seat: "seat@rig",
		Branch: "feature/5-x", Worktree: "wt", BaseSHA: "base", CreatedAt: now,
	}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := st.PutSnapshot(ctx, pid, "rig_queue", []map[string]string{
		{"id": "qitem-1", "state": "pending", "pickup_state": "unclaimed"},
	}, now); err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	liveItem := appendEvent(t, st, "#5")                // live dispatch: kept
	guardedProject := appendEvent(t, st, "project:app") // unresolved queue row: kept
	ordinary := appendEvent(t, st, "#9")                // nothing live: deleted
	for _, id := range []int64{liveItem, guardedProject, ordinary} {
		backdateEvent(t, st, id, now.AddDate(0, 0, -100))
	}

	counts, err := st.PruneEvents(ctx, now.AddDate(0, 0, -90), 500, true)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if counts.Scanned != 3 || counts.Deleted != 1 || counts.KeptLive != 2 {
		t.Fatalf("counts = %+v, want scanned 3, deleted 1, kept 2", counts)
	}
	subjects := eventSubjects(t, st)
	if len(subjects) != 2 {
		t.Fatalf("subjects = %v, want the live item and the guarded project", subjects)
	}
}

// TestPruneEventsDryRun (#395): apply false counts what would go and writes
// nothing.
func TestPruneEventsDryRun(t *testing.T) {
	ctx := context.Background()
	st := upsertStore(t)
	now := time.Now().UTC()
	id := appendEvent(t, st, "project:a")
	backdateEvent(t, st, id, now.AddDate(0, 0, -100))

	counts, err := st.PruneEvents(ctx, now.AddDate(0, 0, -90), 500, false)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if counts.Scanned != 1 || counts.Deleted != 1 {
		t.Fatalf("counts = %+v, want the dry run to count the delete", counts)
	}
	if subjects := eventSubjects(t, st); len(subjects) != 1 {
		t.Fatalf("subjects = %v, want the row still present after a dry run", subjects)
	}
}

// TestPruneEventsKeepsUnresolvedQueueItem (#395): a question's ledger row is
// the durable backstop for its queue row. With the dispatch long ended and only
// the queue row still pending, the decision.* event names the item and the
// qitem; the prune must keep it, or the board stops showing the question and the
// reconcile can never close the row.
func TestPruneEventsKeepsUnresolvedQueueItem(t *testing.T) {
	ctx := context.Background()
	st := upsertStore(t)
	_, pid, err := st.UpsertProject(ctx, baseUpsert())
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	now := time.Now().UTC()

	// The raise shape repair.go writes: subject #<item>, detail ending "; row <qitem>".
	raised, err := st.AppendEvent(ctx, Event{
		Actor: "act", Kind: "decision.merge_repair", Subject: "#7",
		Detail: "app #7: change request #20 conflicts (dirty); asked human@kernel; to rebase; row qitem-7",
	})
	if err != nil {
		t.Fatalf("event: %v", err)
	}
	backdateEvent(t, st, raised, now.AddDate(0, 0, -100))

	// No dispatch at all — the work is open only through its queue row.
	if err := st.PutSnapshot(ctx, pid, "rig_queue", []map[string]string{
		{"id": "qitem-7", "state": "pending", "pickup_state": "unclaimed"},
	}, now); err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	counts, err := st.PruneEvents(ctx, now.AddDate(0, 0, -90), 500, true)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if counts.Scanned != 1 || counts.KeptLive != 1 || counts.Deleted != 0 {
		t.Fatalf("counts = %+v, want the one row scanned and kept", counts)
	}
	if subjects := eventSubjects(t, st); len(subjects) != 1 || subjects[0] != "#7" {
		t.Fatalf("subjects = %v, want #7 kept for its open queue row", subjects)
	}
}
