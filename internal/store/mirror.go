package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// ItemRecord is one normalized forge item written by the mirror.
type ItemRecord struct {
	Number        int
	Kind          string
	State         string
	Title         string
	Labels        string
	Parent        *int
	BlockedByJSON string
	URL           string
	UpdatedAt     time.Time
}

// ReplaceItems rewrites one project's mirrored items in a transaction. A failed
// forge read never reaches here, so cached rows survive a stale tick.
func (s *Store) ReplaceItems(ctx context.Context, projectID int64, records []ItemRecord) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: replace items: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM item WHERE project_id = ?", projectID); err != nil {
		return fmt.Errorf("store: replace items: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO item (project_id, number, kind, state, title, labels, parent_number,
		                  blocked_by_json, url, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("store: replace items: %w", err)
	}
	defer stmt.Close()
	for _, record := range records {
		var parent any
		if record.Parent != nil {
			parent = *record.Parent
		}
		if _, err := stmt.ExecContext(ctx, projectID, record.Number, record.Kind, record.State,
			record.Title, record.Labels, parent, record.BlockedByJSON, record.URL,
			record.UpdatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("store: replace item %d: %w", record.Number, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: replace items: %w", err)
	}
	return nil
}

// PutSnapshot caches one read under (project, kind).
func (s *Store) PutSnapshot(ctx context.Context, projectID int64, kind string, payload any, at time.Time) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("store: snapshot %s: %w", kind, err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO snapshot (project_id, kind, payload_json, synced_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(project_id, kind) DO UPDATE SET payload_json = excluded.payload_json,
		                                            synced_at = excluded.synced_at`,
		projectID, kind, string(raw), at.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("store: snapshot %s: %w", kind, err)
	}
	return nil
}

// SetForgeStamp records the last successful forge sync.
func (s *Store) SetForgeStamp(ctx context.Context, projectID int64, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO mirror_meta (project_id, last_forge_at) VALUES (?, ?)
		ON CONFLICT(project_id) DO UPDATE SET last_forge_at = excluded.last_forge_at`,
		projectID, at.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("store: forge stamp: %w", err)
	}
	return nil
}

// SetRigStamp records the last successful rig sync.
func (s *Store) SetRigStamp(ctx context.Context, projectID int64, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO mirror_meta (project_id, last_rig_at) VALUES (?, ?)
		ON CONFLICT(project_id) DO UPDATE SET last_rig_at = excluded.last_rig_at`,
		projectID, at.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("store: rig stamp: %w", err)
	}
	return nil
}

// MirrorStatus is the stamp/stale block the board reads.
type MirrorStatus struct {
	LastForgeAt    *time.Time `json:"last_forge_at,omitempty"`
	LastRigAt      *time.Time `json:"last_rig_at,omitempty"`
	ForgeStale     bool       `json:"forge_stale"`
	ForgeForbidden bool       `json:"forge_forbidden"`
	RigStale       bool       `json:"rig_stale"`
	ForgeError     string     `json:"forge_error,omitempty"`
	RigError       string     `json:"rig_error,omitempty"`
	BranchError    string     `json:"branch_error,omitempty"`
}

type statusPayload struct {
	ForgeStale     bool   `json:"forge_stale"`
	ForgeForbidden bool   `json:"forge_forbidden"`
	RigStale       bool   `json:"rig_stale"`
	ForgeError     string `json:"forge_error,omitempty"`
	RigError       string `json:"rig_error,omitempty"`
	BranchError    string `json:"branch_error,omitempty"`
}

// MirrorStatus reads the last successful stamps and the last tick's stale block.
func (s *Store) MirrorStatus(ctx context.Context, projectID int64) (MirrorStatus, error) {
	var status MirrorStatus
	var forgeAt, rigAt sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT last_forge_at, last_rig_at FROM mirror_meta WHERE project_id = ?`, projectID).
		Scan(&forgeAt, &rigAt)
	if err != nil && err != sql.ErrNoRows {
		return status, fmt.Errorf("store: mirror status: %w", err)
	}
	if forgeAt.Valid {
		if at, err := time.Parse(time.RFC3339Nano, forgeAt.String); err == nil {
			status.LastForgeAt = &at
		}
	}
	if rigAt.Valid {
		if at, err := time.Parse(time.RFC3339Nano, rigAt.String); err == nil {
			status.LastRigAt = &at
		}
	}
	var payload string
	if err := s.db.QueryRowContext(ctx, `SELECT payload_json FROM snapshot WHERE project_id = ? AND kind = 'status'`, projectID).
		Scan(&payload); err == nil {
		var block statusPayload
		if json.Unmarshal([]byte(payload), &block) == nil {
			status.ForgeStale, status.RigStale = block.ForgeStale, block.RigStale
			status.ForgeForbidden = block.ForgeForbidden
			status.ForgeError, status.RigError, status.BranchError = block.ForgeError, block.RigError, block.BranchError
		}
	} else if err != sql.ErrNoRows {
		return status, fmt.Errorf("store: mirror status: %w", err)
	}
	return status, nil
}

// RepoPath returns the first repo path registered for a project.
func (s *Store) RepoPath(ctx context.Context, projectID int64) (string, error) {
	var path string
	err := s.db.QueryRowContext(ctx, `SELECT path FROM repo WHERE project_id = ? ORDER BY id LIMIT 1`, projectID).Scan(&path)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("store: repo path: %w", err)
	}
	return path, nil
}

// DispatchesForProject returns every dispatch attempt for a project, newest first.
func (s *Store) DispatchesForProject(ctx context.Context, projectID int64) ([]DispatchRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, project_id, item_number, attempt, seat, branch, worktree, base_sha,
		       COALESCE(qitem_id, ''), COALESCE(brief_path, ''), created_at, ended_at, COALESCE(end_reason, '')
		FROM dispatch WHERE project_id = ? ORDER BY item_number, attempt DESC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("store: dispatches: %w", err)
	}
	defer rows.Close()
	var out []DispatchRow
	for rows.Next() {
		var d DispatchRow
		var ended sql.NullString
		if err := rows.Scan(&d.ID, &d.ProjectID, &d.ItemNumber, &d.Attempt, &d.Seat, &d.Branch, &d.Worktree,
			&d.BaseSHA, &d.QItemID, &d.BriefPath, &d.CreatedAt, &ended, &d.EndReason); err != nil {
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

// VerdictForItem is one verdict joined to its item.
type VerdictForItem struct {
	Item     int
	SHA      string
	Decision string
}

// VerdictsForProject returns every verdict for a project.
func (s *Store) VerdictsForProject(ctx context.Context, projectID int64) ([]VerdictForItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.item_number, v.sha, v.decision
		FROM verdict v JOIN dispatch d ON d.id = v.dispatch_id
		WHERE d.project_id = ? ORDER BY v.id DESC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("store: verdicts: %w", err)
	}
	defer rows.Close()
	var out []VerdictForItem
	for rows.Next() {
		var v VerdictForItem
		if err := rows.Scan(&v.Item, &v.SHA, &v.Decision); err != nil {
			return nil, fmt.Errorf("store: verdicts: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ProjectInput registers one project.
type ProjectInput struct {
	Key            string
	Name           string
	ForgeKind      string
	ForgeProject   string
	DefaultBranch  string
	Rig            string
	RigTemplate    string
	ConcurrencyCap int
	Enabled        bool
	OnHold         bool
	GateCmd        string
}

// CreateProject inserts a project row and returns its id.
func (s *Store) CreateProject(ctx context.Context, in ProjectInput) (int64, error) {
	if in.DefaultBranch == "" {
		in.DefaultBranch = "main"
	}
	if in.RigTemplate == "" {
		in.RigTemplate = "rig.yaml.tmpl"
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO project (key, name, forge_kind, forge_project, default_branch, rig, rig_template,
		                     concurrency_cap, enabled, onhold, gate_cmd)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.Key, in.Name, in.ForgeKind, in.ForgeProject, in.DefaultBranch, in.Rig, in.RigTemplate,
		in.ConcurrencyCap, boolInt(in.Enabled), boolInt(in.OnHold), nullString(in.GateCmd))
	if err != nil {
		return 0, fmt.Errorf("store: create project: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: create project: %w", err)
	}
	return id, nil
}

// RepoInput registers one repo for a project.
type RepoInput struct {
	Key          string
	Path         string
	WorktreeRoot string
}

// CreateRepo inserts a repo row.
func (s *Store) CreateRepo(ctx context.Context, projectID int64, in RepoInput) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO repo (project_id, key, path, worktree_root) VALUES (?, ?, ?, ?)`,
		projectID, in.Key, in.Path, in.WorktreeRoot)
	if err != nil {
		return fmt.Errorf("store: create repo: %w", err)
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// GetSnapshot reads one cached payload and its sync time.
func (s *Store) GetSnapshot(ctx context.Context, projectID int64, kind string) ([]byte, time.Time, error) {
	var payload, at string
	err := s.db.QueryRowContext(ctx, `SELECT payload_json, synced_at FROM snapshot WHERE project_id = ? AND kind = ?`, projectID, kind).
		Scan(&payload, &at)
	if err == sql.ErrNoRows {
		return nil, time.Time{}, nil
	}
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("store: snapshot %s: %w", kind, err)
	}
	parsed, _ := time.Parse(time.RFC3339Nano, at)
	return []byte(payload), parsed, nil
}
