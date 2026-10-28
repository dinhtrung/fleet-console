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
