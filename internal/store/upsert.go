package store

import (
	"context"
	"database/sql"
	"fmt"
)

// UpsertResult is what a config apply did to one project.
type UpsertResult string

const (
	UpsertCreated   UpsertResult = "created"
	UpsertUpdated   UpsertResult = "updated"
	UpsertUnchanged UpsertResult = "unchanged"
)

// UpsertInput is one project and its repo from config.yaml.
type UpsertInput struct {
	Project      ProjectInput
	LabelsJSON   string
	RepoKey      string
	RepoPath     string
	WorktreeRoot string
	SyncOnHold   bool
}

// UpsertProject creates or refreshes a project and its repo. onhold is DB-owned:
// it is only written when SyncOnHold is set. Removal from config never deletes.
func (s *Store) UpsertProject(ctx context.Context, in UpsertInput) (UpsertResult, int64, error) {
	if in.Project.Key == "" {
		return "", 0, fmt.Errorf("store: upsert project: key is required")
	}
	if in.Project.DefaultBranch == "" {
		in.Project.DefaultBranch = "main"
	}
	if in.Project.RigTemplate == "" {
		in.Project.RigTemplate = "default"
	}
	if in.LabelsJSON == "" {
		in.LabelsJSON = "{}"
	}
	existing, err := s.projectByKey(ctx, in.Project.Key)
	if err != nil {
		return "", 0, err
	}
	changed := false
	rigs := normalizeRigs(in.Project.Rig, in.Project.Rigs)
	if existing == nil {
		id, err := s.createProjectWithLabels(ctx, in.Project, in.LabelsJSON)
		if err != nil {
			return "", 0, err
		}
		if _, err := s.upsertRepo(ctx, id, in); err != nil {
			return "", 0, err
		}
		return UpsertCreated, id, nil
	}
	onhold := existing.OnHold
	if in.SyncOnHold {
		onhold = in.Project.OnHold
		changed = changed || onhold != existing.OnHold
	}
	if existing.Name != in.Project.Name ||
		existing.ForgeKind != in.Project.ForgeKind ||
		existing.ForgeProject != in.Project.ForgeProject ||
		existing.DefaultBranch != in.Project.DefaultBranch ||
		!sameRigs(existing.Rigs, rigs) ||
		existing.RigTemplate != in.Project.RigTemplate ||
		existing.ConcurrencyCap != in.Project.ConcurrencyCap ||
		existing.GateCmd != in.Project.GateCmd ||
		existing.LabelsJSON != in.LabelsJSON ||
		!existing.Enabled {
		changed = true
	}
	repoChanged, err := s.upsertRepo(ctx, existing.ID, in)
	if err != nil {
		return "", 0, err
	}
	changed = changed || repoChanged
	if !changed {
		return UpsertUnchanged, existing.ID, nil
	}
	firstRig := ""
	if len(rigs) > 0 {
		firstRig = rigs[0]
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE project SET name = ?, forge_kind = ?, forge_project = ?, default_branch = ?,
		       rig = ?, rigs_json = ?, rig_template = ?, concurrency_cap = ?, enabled = 1, onhold = ?, gate_cmd = ?, labels_json = ?
		WHERE id = ?`,
		in.Project.Name, in.Project.ForgeKind, in.Project.ForgeProject, in.Project.DefaultBranch,
		firstRig, encodeRigs(rigs), in.Project.RigTemplate, in.Project.ConcurrencyCap, boolInt(onhold),
		nullString(in.Project.GateCmd), in.LabelsJSON, existing.ID)
	if err != nil {
		return "", 0, fmt.Errorf("store: upsert project: %w", err)
	}
	return UpsertUpdated, existing.ID, nil
}

func (s *Store) projectByKey(ctx context.Context, key string) (*ProjectRow, error) {
	var row ProjectRow
	var enabled, onhold int
	var rigsJSON string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, key, name, forge_kind, forge_project, default_branch, rig, rigs_json, rig_template,
		       concurrency_cap, enabled, onhold, COALESCE(gate_cmd, ''), labels_json
		FROM project WHERE key = ?`, key).
		Scan(&row.ID, &row.Key, &row.Name, &row.ForgeKind, &row.ForgeProject, &row.DefaultBranch,
			&row.Rig, &rigsJSON, &row.RigTemplate, &row.ConcurrencyCap, &enabled, &onhold, &row.GateCmd, &row.LabelsJSON)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: project %s: %w", key, err)
	}
	row.Rigs = decodeRigs(rigsJSON, row.Rig)
	row.Enabled, row.OnHold = enabled != 0, onhold != 0
	return &row, nil
}

func (s *Store) createProjectWithLabels(ctx context.Context, in ProjectInput, labelsJSON string) (int64, error) {
	if in.DefaultBranch == "" {
		in.DefaultBranch = "main"
	}
	if in.RigTemplate == "" {
		in.RigTemplate = "default"
	}
	rigs := normalizeRigs(in.Rig, in.Rigs)
	firstRig := ""
	if len(rigs) > 0 {
		firstRig = rigs[0]
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO project (key, name, forge_kind, forge_project, default_branch, rig, rigs_json, rig_template,
		                     concurrency_cap, enabled, onhold, gate_cmd, labels_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.Key, in.Name, in.ForgeKind, in.ForgeProject, in.DefaultBranch, firstRig, encodeRigs(rigs), in.RigTemplate,
		in.ConcurrencyCap, boolInt(true), boolInt(in.OnHold), nullString(in.GateCmd), labelsJSON)
	if err != nil {
		return 0, fmt.Errorf("store: create project: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: create project: %w", err)
	}
	return id, nil
}

// upsertRepo inserts or updates the project's repo; it reports whether it changed.
func (s *Store) upsertRepo(ctx context.Context, projectID int64, in UpsertInput) (bool, error) {
	key := in.RepoKey
	if key == "" {
		key = "main"
	}
	var path, worktree string
	err := s.db.QueryRowContext(ctx, `SELECT path, worktree_root FROM repo WHERE project_id = ? AND key = ?`,
		projectID, key).Scan(&path, &worktree)
	if err == sql.ErrNoRows {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO repo (project_id, key, path, worktree_root) VALUES (?, ?, ?, ?)`,
			projectID, key, in.RepoPath, in.WorktreeRoot); err != nil {
			return false, fmt.Errorf("store: insert repo: %w", err)
		}
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("store: repo: %w", err)
	}
	if path == in.RepoPath && worktree == in.WorktreeRoot {
		return false, nil
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE repo SET path = ?, worktree_root = ? WHERE project_id = ? AND key = ?`,
		in.RepoPath, in.WorktreeRoot, projectID, key); err != nil {
		return false, fmt.Errorf("store: update repo: %w", err)
	}
	return true, nil
}
