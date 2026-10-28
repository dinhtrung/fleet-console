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
	CreatedAt  time.Time
}

// CreateDispatch inserts one dispatch attempt and returns its id.
func (s *Store) CreateDispatch(ctx context.Context, d NewDispatch) (int64, error) {
	created := d.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO dispatch (project_id, item_number, attempt, seat, branch, worktree, base_sha,
		                      qitem_id, brief_path, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ProjectID, d.ItemNumber, d.Attempt, d.Seat, d.Branch, d.Worktree, d.BaseSHA,
		d.QItemID, d.BriefPath, created.UTC().Format(time.RFC3339Nano))
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
		       COALESCE(qitem_id, ''), COALESCE(brief_path, ''), created_at, ended_at, COALESCE(end_reason, '')
		FROM dispatch WHERE id = ?`, id).
		Scan(&d.ID, &d.ProjectID, &d.ItemNumber, &d.Attempt, &d.Seat, &d.Branch, &d.Worktree,
			&d.BaseSHA, &d.QItemID, &d.BriefPath, &d.CreatedAt, &ended, &d.EndReason)
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
type NewVerdict struct {
	DispatchID int64
	SHA        string
	Decision   string
	Source     string
	At         time.Time
}

// AddVerdict inserts a verdict.
func (s *Store) AddVerdict(ctx context.Context, v NewVerdict) (int64, error) {
	at := v.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO verdict (dispatch_id, sha, decision, source, at) VALUES (?, ?, ?, ?, ?)`,
		v.DispatchID, v.SHA, v.Decision, v.Source, at.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("store: add verdict: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: add verdict: %w", err)
	}
	return id, nil
}
