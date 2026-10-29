package store

import (
	"context"
	"fmt"
)

// schemaVersion is the number of migrations in migrations. A database at this
// user_version is current.
const schemaVersion = 3

// migrations are applied in order; migrations[i] moves user_version from i to
// i+1. Never edit an applied migration; append a new one.
var migrations = []string{schemaV1, schemaV2, schemaV3}

// schemaV1 is design §6 verbatim. Do not add the queue_row, decision, lease or
// task tables here: that state belongs to the rig.
const schemaV1 = `
CREATE TABLE project (
  id INTEGER PRIMARY KEY, key TEXT UNIQUE NOT NULL, name TEXT NOT NULL,
  forge_kind TEXT NOT NULL,                -- github | gitlab
  forge_project TEXT NOT NULL,             -- "dinhtrung/fleet-console"
  default_branch TEXT NOT NULL DEFAULT 'main',
  rig TEXT NOT NULL, rig_template TEXT NOT NULL,
  concurrency_cap INTEGER NOT NULL DEFAULT 2,
  enabled INTEGER NOT NULL DEFAULT 1, onhold INTEGER NOT NULL DEFAULT 0,
  gate_cmd TEXT, labels_json TEXT NOT NULL DEFAULT '{}'
);
CREATE TABLE repo (
  id INTEGER PRIMARY KEY, project_id INTEGER NOT NULL REFERENCES project(id),
  key TEXT NOT NULL, path TEXT NOT NULL, worktree_root TEXT NOT NULL,
  UNIQUE(project_id, key)
);
CREATE TABLE item (                        -- forge mirror, one row per item
  id INTEGER PRIMARY KEY, project_id INTEGER NOT NULL, number INTEGER NOT NULL,
  kind TEXT NOT NULL, state TEXT NOT NULL, title TEXT NOT NULL,
  labels TEXT NOT NULL DEFAULT '', parent_number INTEGER,
  blocked_by_json TEXT NOT NULL DEFAULT '[]', url TEXT NOT NULL,
  updated_at TEXT NOT NULL, UNIQUE(project_id, number)
);
CREATE TABLE dispatch (
  id INTEGER PRIMARY KEY, project_id INTEGER NOT NULL, item_number INTEGER NOT NULL,
  attempt INTEGER NOT NULL, seat TEXT NOT NULL, branch TEXT NOT NULL,
  worktree TEXT NOT NULL, base_sha TEXT NOT NULL, qitem_id TEXT,
  brief_path TEXT, created_at TEXT NOT NULL, ended_at TEXT,
  end_reason TEXT,                         -- merged | rejected | stalled | superseded | abandoned
  UNIQUE(project_id, item_number, attempt)
);
CREATE TABLE verdict (
  id INTEGER PRIMARY KEY, dispatch_id INTEGER NOT NULL REFERENCES dispatch(id),
  sha TEXT NOT NULL, decision TEXT NOT NULL,  -- accept | reject
  source TEXT NOT NULL, at TEXT NOT NULL
);
CREATE TABLE event (
  id INTEGER PRIMARY KEY, at TEXT NOT NULL, actor TEXT NOT NULL, kind TEXT NOT NULL,
  subject TEXT NOT NULL, detail TEXT NOT NULL, exit_code INTEGER, stdout_hash TEXT
);
CREATE TABLE snapshot (                    -- cache of rig reads, never a decision source
  project_id INTEGER NOT NULL, kind TEXT NOT NULL, payload_json TEXT NOT NULL,
  synced_at TEXT NOT NULL, PRIMARY KEY (project_id, kind)
);
CREATE TABLE mirror_meta (
  project_id INTEGER PRIMARY KEY, last_forge_at TEXT, last_rig_at TEXT, etag TEXT
);
`

// schemaV2 gives a dispatch a state. Rows that predate the two-phase dispatch
// are active: they were written together with their queue row.
const schemaV2 = `
ALTER TABLE dispatch ADD COLUMN state TEXT NOT NULL DEFAULT 'active';
`

// schemaV3 adds the ordered rig list. The legacy rig column keeps the first
// name, and existing rows are backfilled from it. json_array escapes the name as
// JSON; string concatenation would only double quotes, which is SQL-literal
// escaping and leaves rigs_json invalid for a name containing a quote.
const schemaV3 = `
ALTER TABLE project ADD COLUMN rigs_json TEXT NOT NULL DEFAULT '[]';
UPDATE project SET rigs_json = json_array(rig) WHERE rig <> '';
`

func (s *Store) migrate(ctx context.Context) error {
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("store: read schema version: %w", err)
	}
	if version > schemaVersion {
		return fmt.Errorf("store: database schema version %d is newer than this binary supports (%d)", version, schemaVersion)
	}
	for next := version; next < schemaVersion; next++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("store: migrate to %d: %w", next+1, err)
		}
		if _, err := tx.ExecContext(ctx, migrations[next]); err != nil {
			tx.Rollback()
			return fmt.Errorf("store: migrate to %d: %w", next+1, err)
		}
		// PRAGMA does not take a bind parameter, and next+1 is a local integer.
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", next+1)); err != nil {
			tx.Rollback()
			return fmt.Errorf("store: record schema version %d: %w", next+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("store: migrate to %d: %w", next+1, err)
		}
	}
	return nil
}
