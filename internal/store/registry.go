package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// SetProjectEnabled enables or disables a project, reporting whether the row
// changed. Disabling leaves every row intact; only DeleteProject removes them.
func (s *Store) SetProjectEnabled(ctx context.Context, projectID int64, enabled bool) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE project SET enabled = ? WHERE id = ?`, boolInt(enabled), projectID)
	if err != nil {
		return false, fmt.Errorf("store: set project enabled: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: set project enabled: %w", err)
	}
	return n > 0, nil
}

// SetProjectOnHold pauses or resumes dispatch for one project, reporting
// whether the row changed. onhold is the pause flag, never enabled: every
// upsert sets enabled = 1 unconditionally (upsert.go), so the boot pass and
// --apply-config would re-enable a project paused through enabled. onhold is
// DB-owned — UpsertProject only overwrites it when the caller passes
// SyncOnHold — so a pause survives both.
func (s *Store) SetProjectOnHold(ctx context.Context, projectID int64, onhold bool) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE project SET onhold = ? WHERE id = ?`, boolInt(onhold), projectID)
	if err != nil {
		return false, fmt.Errorf("store: set project onhold: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: set project onhold: %w", err)
	}
	return n > 0, nil
}

// LiveDispatch returns the first open dispatch of a project, if any.
func (s *Store) LiveDispatch(ctx context.Context, projectID int64) (int64, bool, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM dispatch WHERE project_id = ? AND ended_at IS NULL ORDER BY id LIMIT 1`, projectID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("store: live dispatch: %w", err)
	}
	return id, true, nil
}

// DeleteProject removes a project and the app-owned rows for it in one
// transaction: verdicts, dispatches, items, plan reasons, snapshots, mirror
// metadata and repos. Events are the audit trail and are kept.
//
// Every per-project family belongs in this list, and a new one is not finished
// until it is here: project.id has no AUTOINCREMENT, so SQLite reuses a deleted
// project's rowid, and a family left behind is inherited by the next project
// that lands on the same id.
func (s *Store) DeleteProject(ctx context.Context, projectID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: delete project: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	statements := []string{
		`DELETE FROM verdict WHERE dispatch_id IN (SELECT id FROM dispatch WHERE project_id = ?)`,
		`DELETE FROM dispatch WHERE project_id = ?`,
		`DELETE FROM item WHERE project_id = ?`,
		`DELETE FROM plan_reason WHERE project_id = ?`,
		`DELETE FROM snapshot WHERE project_id = ?`,
		`DELETE FROM mirror_meta WHERE project_id = ?`,
		`DELETE FROM repo WHERE project_id = ?`,
		`DELETE FROM project WHERE id = ?`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement, projectID); err != nil {
			return fmt.Errorf("store: delete project %d: %w", projectID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: delete project %d: %w", projectID, err)
	}
	return nil
}
