package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// A v1 database upgrades to the current schema and backfills the rig list from
// the legacy column. This is the real upgrade path: the deployment database is
// at user_version 1, so v1 -> current is what runs at the next service start.
func TestMigrateFromV1BackfillsRigs(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v1.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, schemaV1); err != nil {
		t.Fatalf("apply schemaV1: %v", err)
	}
	for key, rig := range map[string]string{"a": `ri"g`, "b": "plain", "c": ""} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO project (key, name, forge_kind, forge_project, rig, rig_template) VALUES (?, ?, 'github', 'o/r', ?, 't')`,
			key, key, rig); err != nil {
			t.Fatalf("insert %s: %v", key, err)
		}
	}
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = 1"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	var version, invalid int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM project WHERE json_valid(rigs_json) = 0`).Scan(&invalid); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion || invalid != 0 {
		t.Fatalf("user_version = %d, invalid rigs_json rows = %d", version, invalid)
	}

	rows, err := s.Projects(ctx)
	if err != nil {
		t.Fatalf("projects: %v", err)
	}
	got := make(map[string][]string, len(rows))
	for _, row := range rows {
		got[row.Key] = row.Rigs
	}
	// The backfill must escape as JSON: a name holding a quote survives intact.
	if len(got["a"]) != 1 || got["a"][0] != `ri"g` {
		t.Fatalf("a rigs = %q, want the quoted name intact", got["a"])
	}
	if len(got["b"]) != 1 || got["b"][0] != "plain" {
		t.Fatalf("b rigs = %q, want [plain]", got["b"])
	}
	if len(got["c"]) != 0 {
		t.Fatalf("c rigs = %q, want none for an empty legacy column", got["c"])
	}
}
