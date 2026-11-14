package store

import (
	"context"
	"fmt"
)

// schemaVersion is the number of migrations in migrations. A database at this
// user_version is current.
const schemaVersion = 6

// migrations are applied in order; migrations[i] moves user_version from i to
// i+1. Never edit an applied migration; append a new one.
var migrations = []string{schemaV1, schemaV2, schemaV3, schemaV4, schemaV5, schemaV6}

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

// schemaV4 lets a verdict belong to an item rather than a dispatch. Adoption
// merges a change request the app never dispatched, so its verdict has no
// dispatch to point at (design D2: no synthetic dispatch row). SQLite cannot
// drop a NOT NULL or a foreign key in place, so the table is rebuilt.
//
// project_id is stored rather than resolved through a join: item_number is unique
// only within a project, and resolving the project through the item mirror would
// depend on that cache holding the row — the mirror keeps open items, so the
// linking row disappears exactly when a merged item closes. Both columns are
// backfilled from the dispatch each existing verdict already points at.
const schemaV4 = `
CREATE TABLE verdict_new (
  id INTEGER PRIMARY KEY, dispatch_id INTEGER REFERENCES dispatch(id),
  project_id INTEGER NOT NULL,
  item_number INTEGER NOT NULL DEFAULT 0,
  sha TEXT NOT NULL, decision TEXT NOT NULL,  -- accept | reject
  source TEXT NOT NULL, at TEXT NOT NULL
);
INSERT INTO verdict_new (id, dispatch_id, project_id, item_number, sha, decision, source, at)
  SELECT v.id, v.dispatch_id, COALESCE(d.project_id, 0), COALESCE(d.item_number, 0),
         v.sha, v.decision, v.source, v.at
  FROM verdict v LEFT JOIN dispatch d ON d.id = v.dispatch_id;
DROP TABLE verdict;
ALTER TABLE verdict_new RENAME TO verdict;
`

// schemaV5 gives the project its verdict relay (#375). The relay was file-only:
// a project created on the board writes a row and can carry a block in the
// file, but nothing in the database could hold the pair, so the running tick —
// which reads rows — always answered ("", "") for a row the file did not name.
// Both columns default empty, which is off; the row is authoritative for the
// resolver and the file block is the fallback.
const schemaV5 = `
ALTER TABLE project ADD COLUMN relay_mode TEXT NOT NULL DEFAULT '';
ALTER TABLE project ADD COLUMN relay_seat TEXT NOT NULL DEFAULT '';
`

// schemaV6 keeps the plan's per-item reason (#428, design D4). Before it, the
// reason a ready item was not dispatched lived in one tick's memory: #411 named
// it on the journal, and an operator reading the board — or a restart later —
// saw nothing. One row per item, keyed with its project because item numbers are
// unique only within a project, replaced wholesale per tick by the plan.
//
// head is the item's open change request at plan time, the same head the
// dispatch.skip ledger key uses: a re-review that moves the head makes the old
// reason a different fact rather than a stale one.
const schemaV6 = `
CREATE TABLE plan_reason (
  project_id INTEGER NOT NULL,
  item_number INTEGER NOT NULL,
  reason TEXT NOT NULL,
  detail TEXT NOT NULL DEFAULT '',
  head TEXT NOT NULL DEFAULT '',
  tick_at TEXT NOT NULL,
  PRIMARY KEY (project_id, item_number)
);
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
