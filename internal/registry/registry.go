// Package registry is the one path from a config project to the store: the
// file is the source of truth, the database is its cache. `--apply-config`,
// the boot pass and the board's create route all call Apply, so a project the
// board writes is a project the next boot reads back unchanged.
package registry

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dinhtrung/fleet-console/internal/config"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// Applied is one project's outcome in an apply run.
type Applied struct {
	Key    string
	Result store.UpsertResult
}

// Apply creates or refreshes every project and, when disableAbsent is set,
// disables the projects the file no longer names. Removal never deletes:
// `--prune` is the only one-directional path, and it is an explicit command.
func Apply(ctx context.Context, db *store.Store, projects []config.Project, syncOnHold, disableAbsent bool) ([]Applied, error) {
	applied := make([]Applied, 0, len(projects))
	for _, project := range projects {
		result, err := ApplyOne(ctx, db, project, syncOnHold)
		if err != nil {
			return applied, err
		}
		applied = append(applied, Applied{Key: project.Key, Result: result})
	}
	if !disableAbsent {
		return applied, nil
	}
	keep := make(map[string]bool, len(projects))
	for _, project := range projects {
		keep[project.Key] = true
	}
	rows, err := db.Projects(ctx)
	if err != nil {
		return applied, err
	}
	for _, row := range rows {
		if keep[row.Key] || !row.Enabled {
			continue
		}
		changed, err := db.SetProjectEnabled(ctx, row.ID, false)
		if err != nil {
			return applied, err
		}
		if changed {
			applied = append(applied, Applied{Key: row.Key, Result: store.UpsertResult("disabled")})
		}
	}
	return applied, nil
}

// ApplyOne writes one project and its repo to the store. onhold is DB-owned: it
// is only copied when SyncOnHold is set.
func ApplyOne(ctx context.Context, db *store.Store, project config.Project, syncOnHold bool) (store.UpsertResult, error) {
	name := project.Name
	if name == "" {
		name = project.Key
	}
	labels := []byte("{}")
	if project.Labels != nil {
		encoded, err := json.Marshal(project.Labels)
		if err != nil {
			return "", fmt.Errorf("config: project %s labels: %w", project.Key, err)
		}
		labels = encoded
	}
	onhold := project.OnHold != nil && *project.OnHold
	result, _, err := db.UpsertProject(ctx, store.UpsertInput{
		Project: store.ProjectInput{
			Key: project.Key, Name: name, ForgeKind: project.Forge, ForgeProject: project.Repo,
			DefaultBranch: project.DefaultBranch, Rig: project.Rig, Rigs: project.Rigs, RigTemplate: project.RigTemplate,
			ConcurrencyCap: project.ConcurrencyCap, Enabled: true, OnHold: onhold, GateCmd: project.GateCmd,
		},
		LabelsJSON: string(labels), RepoKey: "main",
		RepoPath: project.Paths.Main, WorktreeRoot: project.Paths.Worktrees,
		SyncOnHold: syncOnHold,
	})
	return result, err
}

// Prune deletes every project absent from the config. It refuses before
// deleting anything when one of them has a live dispatch.
func Prune(ctx context.Context, db *store.Store, projects []config.Project) ([]string, error) {
	keep := make(map[string]bool, len(projects))
	for _, project := range projects {
		keep[project.Key] = true
	}
	rows, err := db.Projects(ctx)
	if err != nil {
		return nil, err
	}
	var absent []store.ProjectRow
	for _, row := range rows {
		if !keep[row.Key] {
			absent = append(absent, row)
		}
	}
	for _, row := range absent {
		id, live, err := db.LiveDispatch(ctx, row.ID)
		if err != nil {
			return nil, err
		}
		if live {
			return nil, fmt.Errorf("prune: project %s has a live dispatch %d", row.Key, id)
		}
	}
	pruned := make([]string, 0, len(absent))
	for _, row := range absent {
		if err := db.DeleteProject(ctx, row.ID); err != nil {
			return pruned, err
		}
		pruned = append(pruned, row.Key)
	}
	return pruned, nil
}
