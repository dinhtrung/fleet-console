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
	guardedProjects, guardedItems, err := s.unresolvedGuards(ctx)
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
			if keepForLiveWork(row.subject, liveItems, guardedItems, guardedProjects) {
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
// still open outlives the window, whatever its age. An item row is kept by a
// live dispatch or by an unresolved queue row that names it; a project row is
// kept by any unresolved queue row in that project.
func keepForLiveWork(subject string, liveItems, guardedItems map[int]bool, guardedProjects map[string]bool) bool {
	if number, ok := strings.CutPrefix(subject, "#"); ok {
		item, err := atoiSafe(number)
		if err != nil {
			return false
		}
		return liveItems[item] || guardedItems[item]
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

// unresolvedGuards is the retention guard set: every project whose last queue
// read holds a row that is not finished, and every item named by such a row.
// The item half needs the inverse mapping the ledger itself carries — the three
// decision.* kinds that back a queue row all end their detail with
// `; row <qitem-id>` and carry the item in their subject — so evidence for an
// open question survives the window even when no dispatch is running.
//
// A missing or unreadable snapshot guards nothing: no evidence is not the same
// as no work, but the window still has to be able to move.
func (s *Store) unresolvedGuards(ctx context.Context) (map[string]bool, map[int]bool, error) {
	projects := map[string]bool{}
	items := map[int]bool{}
	byQItem, err := s.qitemItems(ctx)
	if err != nil {
		return nil, nil, err
	}
	all, err := s.Projects(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, project := range all {
		payload, _, err := s.GetSnapshot(ctx, project.ID, "rig_queue")
		if err != nil || len(payload) == 0 {
			continue
		}
		var rows []queueSnapshotRow
		if err := json.Unmarshal(payload, &rows); err != nil {
			continue
		}
		for _, row := range rows {
			if queueResolved(row) {
				continue
			}
			projects[project.Key] = true
			if item, ok := byQItem[row.ID]; ok {
				items[item] = true
			}
		}
	}
	return projects, items, nil
}

// qitemItems reads the qitem -> item mapping out of the ledger rows that back a
// queue row: their subject is `#<item>` and their detail ends with
// `; row <qitem-id>`.
func (s *Store) qitemItems(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT subject, detail FROM event
		WHERE kind IN ('decision.verdict_question', 'decision.merge_repair', 'decision.stale')`)
	if err != nil {
		return nil, fmt.Errorf("store: prune events: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var subject, detail string
		if err := rows.Scan(&subject, &detail); err != nil {
			return nil, fmt.Errorf("store: prune events: %w", err)
		}
		number, ok := strings.CutPrefix(subject, "#")
		if !ok {
			continue
		}
		item, err := atoiSafe(number)
		if err != nil {
			continue
		}
		if qitem := rowIDFromDetail(detail); qitem != "" {
			out[qitem] = item
		}
	}
	return out, rows.Err()
}

// rowIDFromDetail reads the qitem id the decision raises append as
// `; row <id>`. It takes the last marker: the reason prose can contain other
// semicolons, and the id is always the tail.
func rowIDFromDetail(detail string) string {
	const marker = "; row "
	at := strings.LastIndex(detail, marker)
	if at < 0 {
		return ""
	}
	id := strings.TrimSpace(detail[at+len(marker):])
	if id == "" || strings.ContainsAny(id, " \t\n") {
		return ""
	}
	return id
}
