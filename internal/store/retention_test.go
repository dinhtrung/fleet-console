package store

import (
	"context"
	"fmt"
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

// appendOldEvents inserts n rows past the window in one transaction. Calling
// AppendEvent per row would be a transaction each, and these tests need
// thousands of rows to reach a second batch.
func appendOldEvents(t *testing.T, st *Store, n int, at time.Time) {
	t.Helper()
	tx, err := st.db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.Prepare(`INSERT INTO event (at, actor, kind, subject, detail) VALUES (?, 'act', 'test.event', ?, 'd')`)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer func() { _ = stmt.Close() }()
	stamp := at.UTC().Format(time.RFC3339Nano)
	for i := 0; i < n; i++ {
		if _, err := stmt.Exec(stamp, fmt.Sprintf("project:bulk-%d", i)); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
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

// TestPruneEventsKeepsTheSummarySubject (#430): the housekeeping summary writes
// `project <key>` while the board's audit writes `project:<key>`. The guard reads
// both through one shape list, so a project with live work keeps its summary.
func TestPruneEventsKeepsTheSummarySubject(t *testing.T) {
	ctx := context.Background()
	st := upsertStore(t)
	_, pid, err := st.UpsertProject(ctx, baseUpsert())
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	now := time.Now().UTC()
	if err := st.PutSnapshot(ctx, pid, "rig_queue", []map[string]string{
		{"id": "qitem-1", "state": "pending", "pickup_state": "unclaimed"},
	}, now); err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	summary := appendEvent(t, st, "project app")     // guarded project: kept
	unrelated := appendEvent(t, st, "project other") // no live work: deleted
	backdateEvent(t, st, summary, now.AddDate(0, 0, -100))
	backdateEvent(t, st, unrelated, now.AddDate(0, 0, -100))

	counts, err := st.PruneEvents(ctx, now.AddDate(0, 0, -90), 500, true)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if counts.Scanned != 2 || counts.Deleted != 1 || counts.KeptLive != 1 {
		t.Fatalf("counts = %+v, want scanned 2, deleted 1, kept 1", counts)
	}
	subjects := eventSubjects(t, st)
	if len(subjects) != 1 || subjects[0] != "project app" {
		t.Fatalf("subjects = %v, want the summary kept", subjects)
	}
}

// TestPruneEventsDryRunCountsEveryBatch (#466): the dry run discounted every
// page after the first by the live rows an earlier page had kept, because it
// subtracted the running total instead of the page's own. A run over four pages
// reported the second, third and fourth as if page one's two live rows were on
// them too. The count has to be the number of rows that would go.
func TestPruneEventsDryRunCountsEveryBatch(t *testing.T) {
	ctx := context.Background()
	st := upsertStore(t)
	_, pid, err := st.UpsertProject(ctx, baseUpsert())
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	now := time.Now().UTC()
	old := now.AddDate(0, 0, -100)

	// Two live rows, on their own item, older than the window — written *first* so
	// they land on the first page. The bug only bites when a kept row is on an
	// earlier page than the rows after it: the running total it subtracted was
	// page one's, applied to pages two onward.
	if _, err := st.CreateDispatch(ctx, NewDispatch{
		ProjectID: pid, ItemNumber: 5, Attempt: 1, Seat: "seat@rig",
		Branch: "feature/5-x", Worktree: "wt", BaseSHA: "base", CreatedAt: now,
	}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	for i := 0; i < 2; i++ {
		backdateEvent(t, st, appendEvent(t, st, "#5"), old)
	}
	appendOldEvents(t, st, 1200, old)

	counts, err := st.PruneEvents(ctx, now.AddDate(0, 0, -90), 500, false)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if counts.Scanned != 1202 || counts.Deleted != 1200 || counts.KeptLive != 2 {
		t.Fatalf("counts = %+v, want 1202 scanned, 1200 deleted, 2 kept live", counts)
	}
	if subjects := eventSubjects(t, st); len(subjects) == 0 {
		t.Fatal("a dry run deleted rows")
	}
}

// TestPruneEventsDryRunAtTheBatchBoundary (#466): a full page is not the end of
// the run. Exactly one batch's worth, and one row past it, both report every row
// — the page that comes back empty is what ends the walk, so an off-by-one at
// the boundary cannot come back unnoticed.
func TestPruneEventsDryRunAtTheBatchBoundary(t *testing.T) {
	for _, rows := range []int{499, 500, 501} {
		t.Run(fmt.Sprintf("%d rows", rows), func(t *testing.T) {
			ctx := context.Background()
			st := upsertStore(t)
			now := time.Now().UTC()
			appendOldEvents(t, st, rows, now.AddDate(0, 0, -100))

			counts, err := st.PruneEvents(ctx, now.AddDate(0, 0, -90), 500, false)
			if err != nil {
				t.Fatalf("prune: %v", err)
			}
			if counts.Scanned != rows || counts.Deleted != rows || counts.KeptLive != 0 {
				t.Fatalf("counts = %+v, want %d scanned and deleted", counts, rows)
			}
		})
	}
}

// TestPruneEventsApplyDeletesWhatTheDryRunCounted (#466): the dry run exists to
// be trusted before the apply runs. The two paths must agree row for row across
// batches, and the apply must not start deleting what the guard kept.
func TestPruneEventsApplyDeletesWhatTheDryRunCounted(t *testing.T) {
	ctx := context.Background()
	st := upsertStore(t)
	_, pid, err := st.UpsertProject(ctx, baseUpsert())
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	now := time.Now().UTC()
	old := now.AddDate(0, 0, -100)
	if _, err := st.CreateDispatch(ctx, NewDispatch{
		ProjectID: pid, ItemNumber: 5, Attempt: 1, Seat: "seat@rig",
		Branch: "feature/5-x", Worktree: "wt", BaseSHA: "base", CreatedAt: now,
	}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	for i := 0; i < 2; i++ {
		backdateEvent(t, st, appendEvent(t, st, "#5"), old)
	}
	appendOldEvents(t, st, 1200, old)

	dry, err := st.PruneEvents(ctx, now.AddDate(0, 0, -90), 500, false)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	applied, err := st.PruneEvents(ctx, now.AddDate(0, 0, -90), 500, true)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if applied.Deleted != dry.Deleted || applied.KeptLive != dry.KeptLive {
		t.Fatalf("apply = %+v, dry run = %+v: the two paths disagree", applied, dry)
	}
	if applied.Deleted != 1200 || applied.KeptLive != 2 || applied.Scanned != 1202 {
		t.Fatalf("apply = %+v, want 1202 scanned, 1200 deleted, 2 kept", applied)
	}
	subjects := eventSubjects(t, st)
	if len(subjects) != 2 || subjects[0] != "#5" || subjects[1] != "#5" {
		t.Fatalf("subjects = %v, want only the two live rows", subjects)
	}
	if applied.Deleted != 1200 || applied.KeptLive != 2 || len(subjects) != 2 {
		t.Fatalf("apply = %+v, subjects = %v", applied, subjects)
	}
}
