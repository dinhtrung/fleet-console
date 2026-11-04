package store

import (
	"context"
	"database/sql"
	"fmt"
)

// ProjectRow is one project registry row.
type ProjectRow struct {
	ID             int64
	Key            string
	Name           string
	ForgeKind      string
	ForgeProject   string
	DefaultBranch  string
	Rig            string
	Rigs           []string
	RigTemplate    string
	ConcurrencyCap int
	Enabled        bool
	OnHold         bool
	GateCmd        string
	LabelsJSON     string
}

// ItemRow is one mirrored forge item.
type ItemRow struct {
	ID            int64
	ProjectID     int64
	Number        int
	Kind          string
	State         string
	Title         string
	Labels        string
	ParentNumber  *int
	BlockedByJSON string
	URL           string
	UpdatedAt     string
}

// DispatchRow is one dispatch attempt.
type DispatchRow struct {
	ID         int64   `json:"id"`
	ProjectID  int64   `json:"project_id"`
	ItemNumber int     `json:"item_number"`
	Attempt    int     `json:"attempt"`
	Seat       string  `json:"seat"`
	Branch     string  `json:"branch"`
	Worktree   string  `json:"worktree"`
	BaseSHA    string  `json:"base_sha"`
	QItemID    string  `json:"qitem_id"`
	BriefPath  string  `json:"brief_path"`
	CreatedAt  string  `json:"created_at"`
	EndedAt    *string `json:"ended_at,omitempty"`
	EndReason  string  `json:"end_reason"`
	State      string  `json:"state"`
}

// VerdictRow is one reviewer verdict. DispatchID is 0 when the verdict belongs
// to an adopted change request, which has no dispatch row.
type VerdictRow struct {
	ID         int64  `json:"id"`
	DispatchID int64  `json:"dispatch_id"`
	ItemNumber int    `json:"item_number"`
	SHA        string `json:"sha"`
	Decision   string `json:"decision"`
	Source     string `json:"source"`
	At         string `json:"at"`
}

// EventRow is one audit row.
type EventRow struct {
	ID         int64  `json:"id"`
	At         string `json:"at"`
	Actor      string `json:"actor"`
	Kind       string `json:"kind"`
	Subject    string `json:"subject"`
	Detail     string `json:"detail"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	StdoutHash string `json:"stdout_hash"`
}

// Projects returns the project registry ordered by key.
func (s *Store) Projects(ctx context.Context) ([]ProjectRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, key, name, forge_kind, forge_project, default_branch, rig, rigs_json, rig_template,
		       concurrency_cap, enabled, onhold, COALESCE(gate_cmd, ''), labels_json
		FROM project ORDER BY key`)
	if err != nil {
		return nil, fmt.Errorf("store: projects: %w", err)
	}
	defer rows.Close()
	var out []ProjectRow
	for rows.Next() {
		var p ProjectRow
		var enabled, onhold int
		var rigsJSON string
		if err := rows.Scan(&p.ID, &p.Key, &p.Name, &p.ForgeKind, &p.ForgeProject, &p.DefaultBranch, &p.Rig,
			&rigsJSON, &p.RigTemplate, &p.ConcurrencyCap, &enabled, &onhold, &p.GateCmd, &p.LabelsJSON); err != nil {
			return nil, fmt.Errorf("store: projects: %w", err)
		}
		p.Rigs = decodeRigs(rigsJSON, p.Rig)
		p.Enabled, p.OnHold = enabled != 0, onhold != 0
		out = append(out, p)
	}
	return out, rows.Err()
}

// Items returns every mirrored item ordered by project and number.
func (s *Store) Items(ctx context.Context) ([]ItemRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, project_id, number, kind, state, title, labels,
		       parent_number, blocked_by_json, url, updated_at
		FROM item ORDER BY project_id, number`)
	if err != nil {
		return nil, fmt.Errorf("store: items: %w", err)
	}
	defer rows.Close()
	var out []ItemRow
	for rows.Next() {
		var item ItemRow
		var parent sql.NullInt64
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.Number, &item.Kind, &item.State, &item.Title,
			&item.Labels, &parent, &item.BlockedByJSON, &item.URL, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("store: items: %w", err)
		}
		if parent.Valid {
			n := int(parent.Int64)
			item.ParentNumber = &n
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// ItemCount returns how many items are cached for a project. The mirror uses it
// to scale the project deadline before the forge read (base + per-item).
func (s *Store) ItemCount(ctx context.Context, projectID int64) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM item WHERE project_id = ?`, projectID).Scan(&count); err != nil {
		return 0, fmt.Errorf("store: item count: %w", err)
	}
	return count, nil
}

// Item finds one item by project and number.
func (s *Store) Item(ctx context.Context, projectID int64, number int) (*ItemRow, error) {
	var item ItemRow
	var parent sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT id, project_id, number, kind, state, title, labels,
		       parent_number, blocked_by_json, url, updated_at
		FROM item WHERE project_id = ? AND number = ?`, projectID, number).
		Scan(&item.ID, &item.ProjectID, &item.Number, &item.Kind, &item.State, &item.Title,
			&item.Labels, &parent, &item.BlockedByJSON, &item.URL, &item.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: item %d: %w", number, err)
	}
	if parent.Valid {
		n := int(parent.Int64)
		item.ParentNumber = &n
	}
	return &item, nil
}

// Dispatches returns every dispatch for one item, newest attempt first.
func (s *Store) Dispatches(ctx context.Context, projectID int64, number int) ([]DispatchRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, project_id, item_number, attempt, seat, branch, worktree, base_sha,
		       COALESCE(qitem_id, ''), COALESCE(brief_path, ''), created_at, ended_at, COALESCE(end_reason, ''), state
		FROM dispatch WHERE project_id = ? AND item_number = ? ORDER BY attempt DESC`, projectID, number)
	if err != nil {
		return nil, fmt.Errorf("store: dispatches: %w", err)
	}
	defer rows.Close()
	var out []DispatchRow
	for rows.Next() {
		var d DispatchRow
		var ended sql.NullString
		if err := rows.Scan(&d.ID, &d.ProjectID, &d.ItemNumber, &d.Attempt, &d.Seat, &d.Branch, &d.Worktree,
			&d.BaseSHA, &d.QItemID, &d.BriefPath, &d.CreatedAt, &ended, &d.EndReason, &d.State); err != nil {
			return nil, fmt.Errorf("store: dispatches: %w", err)
		}
		if ended.Valid {
			at := ended.String
			d.EndedAt = &at
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Verdicts returns the verdicts for one dispatch, newest first. A verdict
// recorded for an adopted change request has no dispatch and is not returned
// here; VerdictsForItem reads those.
func (s *Store) Verdicts(ctx context.Context, dispatchID int64) ([]VerdictRow, error) {
	return s.verdictRows(ctx, `
		SELECT id, COALESCE(dispatch_id, 0), item_number, sha, decision, source, at FROM verdict
		WHERE dispatch_id = ? ORDER BY id DESC`, dispatchID)
}

// VerdictsForItem returns the verdicts recorded for one item, newest first,
// whatever row they came from. An adopted change request has no dispatch, so a
// merge that runs without one has to read the verdict by item.
func (s *Store) VerdictsForItem(ctx context.Context, item int) ([]VerdictRow, error) {
	return s.verdictRows(ctx, `
		SELECT id, COALESCE(dispatch_id, 0), item_number, sha, decision, source, at FROM verdict
		WHERE item_number = ? ORDER BY id DESC`, item)
}

func (s *Store) verdictRows(ctx context.Context, query string, arg any) ([]VerdictRow, error) {
	rows, err := s.db.QueryContext(ctx, query, arg)
	if err != nil {
		return nil, fmt.Errorf("store: verdicts: %w", err)
	}
	defer rows.Close()
	var out []VerdictRow
	for rows.Next() {
		var v VerdictRow
		if err := rows.Scan(&v.ID, &v.DispatchID, &v.ItemNumber, &v.SHA, &v.Decision, &v.Source, &v.At); err != nil {
			return nil, fmt.Errorf("store: verdicts: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// EventsForItem returns the audit trail that names one item number.
func (s *Store) EventsForItem(ctx context.Context, number, limit int) ([]EventRow, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, at, actor, kind, subject, detail, exit_code, COALESCE(stdout_hash, '')
		FROM event WHERE subject LIKE ? OR detail LIKE ?
		ORDER BY id DESC LIMIT ?`, "%#"+fmt.Sprint(number)+"%", "%#"+fmt.Sprint(number)+"%", limit)
	if err != nil {
		return nil, fmt.Errorf("store: events for item: %w", err)
	}
	defer rows.Close()
	return scanEvents(rows)
}

// RecentEvents returns the newest audit rows.
func (s *Store) RecentEvents(ctx context.Context, limit int) ([]EventRow, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, at, actor, kind, subject, detail, exit_code, COALESCE(stdout_hash, '')
		FROM event ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: events: %w", err)
	}
	defer rows.Close()
	return scanEvents(rows)
}

func scanEvents(rows *sql.Rows) ([]EventRow, error) {
	var out []EventRow
	for rows.Next() {
		var e EventRow
		var exit sql.NullInt64
		if err := rows.Scan(&e.ID, &e.At, &e.Actor, &e.Kind, &e.Subject, &e.Detail, &exit, &e.StdoutHash); err != nil {
			return nil, fmt.Errorf("store: events: %w", err)
		}
		if exit.Valid {
			n := int(exit.Int64)
			e.ExitCode = &n
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// EventByKindAndSubject returns the newest event with this kind and subject.
func (s *Store) EventByKindAndSubject(ctx context.Context, kind, subject string) (*EventRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, at, actor, kind, subject, detail, exit_code, COALESCE(stdout_hash, '')
		FROM event WHERE kind = ? AND subject = ? ORDER BY id DESC LIMIT 1`, kind, subject)
	if err != nil {
		return nil, fmt.Errorf("store: event %s/%s: %w", kind, subject, err)
	}
	defer rows.Close()
	events, err := scanEvents(rows)
	if err != nil || len(events) == 0 {
		return nil, err
	}
	return &events[0], nil
}

// SetProjectOnHold pauses or resumes dispatch for one project.
func (s *Store) SetProjectOnHold(ctx context.Context, projectID int64, onhold bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE project SET onhold = ? WHERE id = ?`, boolInt(onhold), projectID)
	if err != nil {
		return fmt.Errorf("store: set project onhold: %w", err)
	}
	return nil
}

// HasEvent reports whether an event of kind for subject already carries the text.
// It is the once-per-key ledger for intents that must not repeat every tick.
func (s *Store) HasEvent(ctx context.Context, kind, subject, contains string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM event WHERE kind = ? AND subject = ? AND instr(detail, ?) > 0 LIMIT 1`,
		kind, subject, contains).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("store: event lookup: %w", err)
	}
	return true, nil
}
