package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
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
	RigTiers       []RigTier
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
		p.RigTiers = decodeRigTiers(rigsJSON, p.Rig)
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

// VerdictsForItem returns the verdicts recorded for one item of one project,
// newest first. An adopted change request has no dispatch, so a merge that runs
// without one reads the verdict by item.
//
// The project scope is required, not decoration: item_number is unique only
// within a project (UNIQUE(project_id, number)), and two projects number their
// items independently. The row carries its own project_id for that reason.
func (s *Store) VerdictsForItem(ctx context.Context, projectID int64, item int) ([]VerdictRow, error) {
	return s.verdictRows(ctx, `
		SELECT id, COALESCE(dispatch_id, 0), item_number, sha, decision, source, at FROM verdict
		WHERE project_id = ? AND item_number = ? ORDER BY id DESC`, projectID, item)
}

func (s *Store) verdictRows(ctx context.Context, query string, args ...any) ([]VerdictRow, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
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

// EventFilter narrows an audit read. Every field is optional: a zero value
// means "no filter". Since and Until are compared as instants, not as text:
// `at` is written RFC3339Nano, whose text order disagrees with instant order
// when the fractional seconds differ in length ("...23Z" vs "...23.5Z").
// Before is the keyset cursor — only rows with a smaller id are returned, so a
// page stays stable while the writer appends.
type EventFilter struct {
	Actor         string
	Kind          string
	SubjectPrefix string
	Since         string
	Until         string
	Before        int64
	Limit         int
}

// EventPage is one page of the audit log and the cursor for the next one.
// NextBefore is the smallest id on a full page and 0 on the last page — the key
// is always present, so "no further page" is a value the caller reads, not a
// field it has to notice is missing.
type EventPage struct {
	Events     []EventRow `json:"events"`
	NextBefore int64      `json:"next_before"`
}

// Events reads the audit log newest first, paged by keyset on id DESC.
func (s *Store) Events(ctx context.Context, filter EventFilter) (EventPage, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	query := `SELECT id, at, actor, kind, subject, detail, exit_code, COALESCE(stdout_hash, '') FROM event`
	var conditions []string
	var args []any
	if filter.Before > 0 {
		conditions = append(conditions, "id < ?")
		args = append(args, filter.Before)
	}
	if filter.Actor != "" {
		conditions = append(conditions, "actor = ?")
		args = append(args, filter.Actor)
	}
	if filter.Kind != "" {
		conditions = append(conditions, "kind = ?")
		args = append(args, filter.Kind)
	}
	if filter.SubjectPrefix != "" {
		conditions = append(conditions, `subject LIKE ? ESCAPE '\'`)
		args = append(args, escapeLike(filter.SubjectPrefix)+"%")
	}
	if filter.Since != "" {
		conditions = append(conditions, "julianday(at) >= julianday(?)")
		args = append(args, filter.Since)
	}
	if filter.Until != "" {
		conditions = append(conditions, "julianday(at) <= julianday(?)")
		args = append(args, filter.Until)
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return EventPage{}, fmt.Errorf("store: events: %w", err)
	}
	defer rows.Close()
	events, err := scanEvents(rows)
	if err != nil {
		return EventPage{}, err
	}
	if events == nil {
		// An empty page is an empty list, never JSON null: the screen renders
		// "no events match" from a list, not from a missing field.
		events = []EventRow{}
	}
	page := EventPage{Events: events}
	if len(events) == limit {
		page.NextBefore = events[len(events)-1].ID
	}
	return page, nil
}

// escapeLike makes a value safe as a LIKE prefix: the escape character itself
// first, then the two wildcards.
func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "%", `\%`)
	return strings.ReplaceAll(value, "_", `\_`)
}

// RecentEvents returns the newest audit rows. It is the unfiltered first page.
func (s *Store) RecentEvents(ctx context.Context, limit int) ([]EventRow, error) {
	page, err := s.Events(ctx, EventFilter{Limit: limit})
	if err != nil {
		return nil, err
	}
	return page.Events, nil
}

// EventByID returns one audit row, or nil when no row has that id.
func (s *Store) EventByID(ctx context.Context, id int64) (*EventRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, at, actor, kind, subject, detail, exit_code, COALESCE(stdout_hash, '')
		FROM event WHERE id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("store: event %d: %w", id, err)
	}
	defer rows.Close()
	events, err := scanEvents(rows)
	if err != nil || len(events) == 0 {
		return nil, err
	}
	return &events[0], nil
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
