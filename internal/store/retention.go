package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// RetentionCounts is one prune run's numbers (#395): how many rows were older
// than the window, how many went, and how many were kept because live work
// still owns them.
type RetentionCounts struct {
	Scanned  int
	Deleted  int
	KeptLive int
}

// PruneEvents deletes ledger rows older than cutoff in batches, never touching
// evidence for live work. Two guards, both by shape because the event table has
// no project column:
//
//   - a row whose subject is `#<n>` is kept while any project has a live
//     dispatch for item n (no ended_at) — the subject does not say which
//     project, so the conservative reading is the only safe one;
//   - a row whose subject is `project:<key>` is kept while that project's last
//     queue read holds an unresolved row.
//
// Everything else is judged by age. apply false counts and writes nothing.
func (s *Store) PruneEvents(ctx context.Context, cutoff time.Time, batch int, apply bool) (RetentionCounts, error) {
	if batch <= 0 {
		batch = 500
	}
	liveItems, err := s.liveDispatchItems(ctx)
	if err != nil {
		return RetentionCounts{}, err
	}
	guarded, err := s.guardedProjects(ctx)
	if err != nil {
		return RetentionCounts{}, err
	}

	var counts RetentionCounts
	cursor := int64(0)
	window := cutoff.UTC().Format(time.RFC3339Nano)
	for {
		rows, err := s.db.QueryContext(ctx, `
			SELECT id, subject FROM event WHERE at < ? AND id > ? ORDER BY id LIMIT ?`,
			window, cursor, batch)
		if err != nil {
			return counts, fmt.Errorf("store: prune events: %w", err)
		}
		type candidate struct {
			id      int64
			subject string
		}
		var page []candidate
		for rows.Next() {
			var row candidate
			if err := rows.Scan(&row.id, &row.subject); err != nil {
				rows.Close()
				return counts, fmt.Errorf("store: prune events: %w", err)
			}
			page = append(page, row)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return counts, fmt.Errorf("store: prune events: %w", err)
		}
		rows.Close()
		if len(page) == 0 {
			break
		}

		var deleteIDs []any
		for _, row := range page {
			counts.Scanned++
			cursor = row.id
			if keepForLiveWork(row.subject, liveItems, guarded) {
				counts.KeptLive++
				continue
			}
			if apply {
				deleteIDs = append(deleteIDs, row.id)
			}
		}
		if apply && len(deleteIDs) > 0 {
			placeholders := strings.TrimSuffix(strings.Repeat("?,", len(deleteIDs)), ",")
			args := make([]any, 0, len(deleteIDs))
			args = append(args, deleteIDs...)
			res, err := s.db.ExecContext(ctx, `DELETE FROM event WHERE id IN (`+placeholders+`)`, args...)
			if err != nil {
				return counts, fmt.Errorf("store: prune events: %w", err)
			}
			removed, _ := res.RowsAffected()
			counts.Deleted += int(removed)
		} else if !apply {
			counts.Deleted += len(page) - counts.KeptLive
		}
	}
	return counts, nil
}

// keepForLiveWork is the ledger's retention guard: evidence for work that is
// still moving outlives the window, whatever its age.
func keepForLiveWork(subject string, liveItems map[int]bool, guardedProjects map[string]bool) bool {
	if number, ok := strings.CutPrefix(subject, "#"); ok {
		if item, err := atoiSafe(number); err == nil && liveItems[item] {
			return true
		}
		return false
	}
	if key, ok := strings.CutPrefix(subject, "project:"); ok {
		return guardedProjects[strings.TrimSpace(key)]
	}
	return false
}

func atoiSafe(value string) (int, error) {
	number := 0
	if value == "" {
		return 0, fmt.Errorf("empty")
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not a number")
		}
		number = number*10 + int(r-'0')
	}
	return number, nil
}

// liveDispatchItems is every item number with a dispatch that has not ended.
func (s *Store) liveDispatchItems(ctx context.Context) (map[int]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT item_number FROM dispatch WHERE ended_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("store: prune events: %w", err)
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var item int
		if err := rows.Scan(&item); err != nil {
			return nil, fmt.Errorf("store: prune events: %w", err)
		}
		out[item] = true
	}
	return out, rows.Err()
}

// guardedProjects is every project whose last queue read holds a row that is
// not finished. A missing or unreadable snapshot guards nothing: no evidence is
// not the same as no work, but the window still has to be able to move.
func (s *Store) guardedProjects(ctx context.Context) (map[string]bool, error) {
	out := map[string]bool{}
	projects, err := s.Projects(ctx)
	if err != nil {
		return nil, err
	}
	for _, project := range projects {
		payload, _, err := s.GetSnapshot(ctx, project.ID, "rig_queue")
		if err != nil || len(payload) == 0 {
			continue
		}
		var rows []queueSnapshotRow
		if err := json.Unmarshal(payload, &rows); err != nil {
			continue
		}
		for _, row := range rows {
			if !queueResolved(row) {
				out[project.Key] = true
				break
			}
		}
	}
	return out, nil
}
