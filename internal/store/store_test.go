package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenCreatesMigratesAndReopens(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "fleet-console.db")

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version != schemaVersion {
		t.Fatalf("user_version = %d, want %d", version, schemaVersion)
	}

	var journal string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if journal != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journal)
	}

	var busy int
	if err := s.db.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busy); err != nil {
		t.Fatalf("read busy_timeout: %v", err)
	}
	if busy != busyTimeoutMS {
		t.Fatalf("busy_timeout = %d, want %d", busy, busyTimeoutMS)
	}

	for _, table := range []string{"project", "repo", "item", "dispatch", "verdict", "event", "snapshot", "mirror_meta"} {
		var name string
		if err := s.db.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name); err != nil {
			t.Fatalf("table %s: %v", table, err)
		}
	}
	for _, table := range []string{"queue_row", "decision", "lease", "task"} {
		var name string
		err := s.db.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		if err == nil {
			t.Fatalf("rig-owned table %s must not exist in the app database", table)
		}
	}

	id, err := s.AppendEvent(ctx, Event{Actor: "test", Kind: "dispatch", Subject: "#2"})
	if err != nil {
		t.Fatalf("append event: %v", err)
	}
	if id <= 0 {
		t.Fatalf("event id = %d, want positive", id)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	var events int
	if err := reopened.db.QueryRowContext(ctx, "SELECT count(*) FROM event").Scan(&events); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if events != 1 {
		t.Fatalf("events after reopen = %d, want 1", events)
	}
	if err := reopened.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read user_version after reopen: %v", err)
	}
	if version != schemaVersion {
		t.Fatalf("user_version after reopen = %d, want %d", version, schemaVersion)
	}
}

func TestAppendEventRequiresActorKindSubject(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "fleet-console.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	if _, err := s.AppendEvent(ctx, Event{Kind: "dispatch", Subject: "#2"}); err == nil {
		t.Fatal("append event without actor: want error")
	}
}

// insertEvent writes one event with a chosen `at`, which AppendEvent cannot do:
// it always stamps time.Now. A filter test needs instant control.
func insertEvent(t *testing.T, s *Store, at, actor, kind, subject string) int64 {
	t.Helper()
	res, err := s.db.ExecContext(context.Background(), `
		INSERT INTO event (at, actor, kind, subject, detail, exit_code, stdout_hash)
		VALUES (?, ?, ?, ?, '', NULL, NULL)`, at, actor, kind, subject)
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("insert event id: %v", err)
	}
	return id
}

func openStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "fleet-console.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// The keyset walks the log newest first with no OFFSET: five rows, a page of
// two, and the cursor empty exactly on the short last page.
func TestEventsPagesByKeyset(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	for i := 1; i <= 5; i++ {
		insertEvent(t, s, "2026-11-05T10:00:0"+string(rune('0'+i))+"Z", "act", "dispatch", "#"+string(rune('0'+i)))
	}

	page, err := s.Events(ctx, EventFilter{Limit: 2})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if got := eventIDs(page.Events); !equalInts(got, []int64{5, 4}) {
		t.Fatalf("page 1 ids = %v, want [5 4]", got)
	}
	if page.NextBefore != 4 {
		t.Fatalf("page 1 cursor = %d, want 4", page.NextBefore)
	}

	page, err = s.Events(ctx, EventFilter{Limit: 2, Before: page.NextBefore})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if got := eventIDs(page.Events); !equalInts(got, []int64{3, 2}) {
		t.Fatalf("page 2 ids = %v, want [3 2]", got)
	}
	if page.NextBefore != 2 {
		t.Fatalf("page 2 cursor = %d, want 2", page.NextBefore)
	}

	page, err = s.Events(ctx, EventFilter{Limit: 2, Before: page.NextBefore})
	if err != nil {
		t.Fatalf("page 3: %v", err)
	}
	if got := eventIDs(page.Events); !equalInts(got, []int64{1}) {
		t.Fatalf("page 3 ids = %v, want [1]", got)
	}
	if page.NextBefore != 0 {
		t.Fatalf("page 3 cursor = %d, want empty (0)", page.NextBefore)
	}
	if page, err = s.Events(ctx, EventFilter{Limit: 2, Before: 1}); err != nil || len(page.Events) != 0 {
		t.Fatalf("page past the end = %v, %v; want empty", eventIDs(page.Events), err)
	}
}

// A row appended between pages must not shift the window: the second page is
// read by id, not by position.
func TestEventsCursorIsStableUnderWrites(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	for i := 1; i <= 4; i++ {
		insertEvent(t, s, "2026-11-05T10:00:0"+string(rune('0'+i))+"Z", "act", "dispatch", "#"+string(rune('0'+i)))
	}
	first, err := s.Events(ctx, EventFilter{Limit: 2})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	insertEvent(t, s, "2026-11-05T11:00:00Z", "act", "dispatch", "#99")

	second, err := s.Events(ctx, EventFilter{Limit: 2, Before: first.NextBefore})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if got := eventIDs(second.Events); !equalInts(got, []int64{2, 1}) {
		t.Fatalf("page 2 after an insert = %v, want [2 1] (the new row is not re-shown)", got)
	}
}

func TestEventsFiltersByActorKindAndSubjectPrefix(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	insertEvent(t, s, "2026-11-05T10:00:01Z", "act", "dispatch", "fleet-console")
	insertEvent(t, s, "2026-11-05T10:00:02Z", "act", "merge", "fleet-console")
	insertEvent(t, s, "2026-11-05T10:00:03Z", "human", "verdict", "fleet-console#167")
	insertEvent(t, s, "2026-11-05T10:00:04Z", "alert", "telegram.sent", "qitem-1")

	page, err := s.Events(ctx, EventFilter{Actor: "act"})
	if err != nil {
		t.Fatalf("actor filter: %v", err)
	}
	if got := eventIDs(page.Events); !equalInts(got, []int64{2, 1}) {
		t.Fatalf("actor=act ids = %v, want [2 1]", got)
	}

	page, err = s.Events(ctx, EventFilter{SubjectPrefix: "fleet-console"})
	if err != nil {
		t.Fatalf("subject filter: %v", err)
	}
	if got := eventIDs(page.Events); !equalInts(got, []int64{3, 2, 1}) {
		t.Fatalf(`subject prefix "fleet-console" ids = %v, want [3 2 1]`, got)
	}

	page, err = s.Events(ctx, EventFilter{Kind: "merge"})
	if err != nil {
		t.Fatalf("kind filter: %v", err)
	}
	if got := eventIDs(page.Events); !equalInts(got, []int64{2}) {
		t.Fatalf("kind=merge ids = %v, want [2]", got)
	}

	// The prefix is a literal: a wildcard in the value must not widen it.
	page, err = s.Events(ctx, EventFilter{SubjectPrefix: "fleet%"})
	if err != nil {
		t.Fatalf("literal wildcard filter: %v", err)
	}
	if len(page.Events) != 0 {
		t.Fatalf(`subject prefix "fleet%%" ids = %v, want empty (%% is a literal)`, eventIDs(page.Events))
	}
}

// Since/Until must compare instants. Text order would miss "...23.5Z" when the
// bound is "...23Z", because as strings "23Z" sorts after "23.5Z".
func TestEventsFilterTimesCompareAsInstants(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	insertEvent(t, s, "2026-11-05T10:00:23Z", "act", "dispatch", "#1")
	insertEvent(t, s, "2026-11-05T10:00:23.5Z", "act", "dispatch", "#2")
	insertEvent(t, s, "2026-11-05T10:00:24Z", "act", "dispatch", "#3")

	page, err := s.Events(ctx, EventFilter{Since: "2026-11-05T10:00:23.4Z"})
	if err != nil {
		t.Fatalf("since: %v", err)
	}
	if got := eventIDs(page.Events); !equalInts(got, []int64{3, 2}) {
		t.Fatalf("since 23.4 ids = %v, want [3 2] (.5 is after .4, text order disagrees)", got)
	}

	page, err = s.Events(ctx, EventFilter{Until: "2026-11-05T10:00:23.5Z"})
	if err != nil {
		t.Fatalf("until: %v", err)
	}
	if got := eventIDs(page.Events); !equalInts(got, []int64{2, 1}) {
		t.Fatalf("until 23.5 ids = %v, want [2 1]", got)
	}

	page, err = s.Events(ctx, EventFilter{
		Since: "2026-11-05T10:00:23.4Z", Until: "2026-11-05T10:00:23.6Z",
	})
	if err != nil {
		t.Fatalf("window: %v", err)
	}
	if got := eventIDs(page.Events); !equalInts(got, []int64{2}) {
		t.Fatalf("window 23.4..23.6 ids = %v, want [2]", got)
	}
}

func eventIDs(events []EventRow) []int64 {
	out := make([]int64, 0, len(events))
	for _, e := range events {
		out = append(out, e.ID)
	}
	return out
}

func equalInts(got, want []int64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
