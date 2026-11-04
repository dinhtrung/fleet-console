package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// NewDispatch is one dispatch attempt to record.
type NewDispatch struct {
	ProjectID  int64
	ItemNumber int
	Attempt    int
	Seat       string
	Branch     string
	Worktree   string
	BaseSHA    string
	QItemID    string
	BriefPath  string
	State      string
	CreatedAt  time.Time
}

// CreateDispatch inserts one dispatch attempt and returns its id.
func (s *Store) CreateDispatch(ctx context.Context, d NewDispatch) (int64, error) {
	created := d.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	state := d.State
	if state == "" {
		state = "active"
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO dispatch (project_id, item_number, attempt, seat, branch, worktree, base_sha,
		                      qitem_id, brief_path, state, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ProjectID, d.ItemNumber, d.Attempt, d.Seat, d.Branch, d.Worktree, d.BaseSHA,
		d.QItemID, d.BriefPath, state, created.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("store: create dispatch: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: create dispatch: %w", err)
	}
	return id, nil
}

// EndDispatch marks a dispatch attempt ended.
func (s *Store) EndDispatch(ctx context.Context, id int64, reason string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE dispatch SET ended_at = ?, end_reason = ? WHERE id = ? AND ended_at IS NULL`,
		time.Now().UTC().Format(time.RFC3339Nano), reason, id)
	if err != nil {
		return fmt.Errorf("store: end dispatch: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("store: dispatch %d is not open", id)
	}
	return nil
}

// Dispatch reads one dispatch by id.
func (s *Store) Dispatch(ctx context.Context, id int64) (*DispatchRow, error) {
	var d DispatchRow
	var ended sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, project_id, item_number, attempt, seat, branch, worktree, base_sha,
		       COALESCE(qitem_id, ''), COALESCE(brief_path, ''), created_at, ended_at, COALESCE(end_reason, ''), state
		FROM dispatch WHERE id = ?`, id).
		Scan(&d.ID, &d.ProjectID, &d.ItemNumber, &d.Attempt, &d.Seat, &d.Branch, &d.Worktree,
			&d.BaseSHA, &d.QItemID, &d.BriefPath, &d.CreatedAt, &ended, &d.EndReason, &d.State)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: dispatch %d: %w", id, err)
	}
	if ended.Valid {
		at := ended.String
		d.EndedAt = &at
	}
	return &d, nil
}

// NewVerdict is one reviewer verdict to record.
//
// DispatchID is 0 for an adopted change request: the app never dispatched the
// item, so there is no row to point at (design D2 — no synthetic dispatch).
// ProjectID and ItemNumber name the item either way, and both are required: item
// numbers are unique only within a project.
type NewVerdict struct {
	DispatchID int64
	ProjectID  int64
	ItemNumber int
	SHA        string
	Decision   string
	Source     string
	At         time.Time
}

// AddVerdict inserts a verdict. A zero DispatchID is stored as NULL: a verdict
// with no dispatch is adoption's normal case, not a missing value.
func (s *Store) AddVerdict(ctx context.Context, v NewVerdict) (int64, error) {
	at := v.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	var dispatchID any
	if v.DispatchID != 0 {
		dispatchID = v.DispatchID
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO verdict (dispatch_id, project_id, item_number, sha, decision, source, at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		dispatchID, v.ProjectID, v.ItemNumber, v.SHA, v.Decision, v.Source,
		at.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("store: add verdict: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: add verdict: %w", err)
	}
	return id, nil
}

// PendingDispatches returns un-stamped two-phase dispatches for a project.
func (s *Store) PendingDispatches(ctx context.Context, projectID int64) ([]DispatchRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, project_id, item_number, attempt, seat, branch, worktree, base_sha,
		       COALESCE(qitem_id, ''), COALESCE(brief_path, ''), created_at, ended_at, COALESCE(end_reason, ''), state
		FROM dispatch WHERE project_id = ? AND state = 'pending' AND COALESCE(qitem_id, '') = ''
		ORDER BY id`, projectID)
	if err != nil {
		return nil, fmt.Errorf("store: pending dispatches: %w", err)
	}
	defer rows.Close()
	var out []DispatchRow
	for rows.Next() {
		var d DispatchRow
		var ended sql.NullString
		if err := rows.Scan(&d.ID, &d.ProjectID, &d.ItemNumber, &d.Attempt, &d.Seat, &d.Branch, &d.Worktree,
			&d.BaseSHA, &d.QItemID, &d.BriefPath, &d.CreatedAt, &ended, &d.EndReason, &d.State); err != nil {
			return nil, fmt.Errorf("store: pending dispatches: %w", err)
		}
		if ended.Valid {
			at := ended.String
			d.EndedAt = &at
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// StampDispatch completes a two-phase dispatch with its queue row.
func (s *Store) StampDispatch(ctx context.Context, id int64, qitemID string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE dispatch SET qitem_id = ?, state = 'active' WHERE id = ? AND state = 'pending'`, qitemID, id)
	if err != nil {
		return fmt.Errorf("store: stamp dispatch: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("store: dispatch %d is not pending", id)
	}
	return nil
}

// AbandonDispatch closes a pending dispatch that has no queue row.
func (s *Store) AbandonDispatch(ctx context.Context, id int64, reason string) error {
	if reason == "" {
		reason = "abandoned"
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE dispatch SET state = 'abandoned', ended_at = ?, end_reason = ? WHERE id = ? AND state = 'pending' AND ended_at IS NULL`,
		time.Now().UTC().Format(time.RFC3339Nano), reason, id)
	if err != nil {
		return fmt.Errorf("store: abandon dispatch: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("store: dispatch %d is not pending", id)
	}
	return nil
}
