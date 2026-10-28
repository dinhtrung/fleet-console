package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// DispatcherState is the old dispatcher's JSON state. Only the keys the cutover
// imports are typed; unknown keys are ignored.
type DispatcherState struct {
	Dispatched map[string]DispatchRecord `json:"dispatched"`
	Attempts   map[string]int            `json:"attempts"`
	Nudges     map[string]any            `json:"nudges"`
	Closers    map[string]any            `json:"closers"`
}

// DispatchRecord is one old dispatcher dispatch.
type DispatchRecord struct {
	At       float64 `json:"at"`
	Attempt  int     `json:"attempt"`
	Base     string  `json:"base"`
	Branch   string  `json:"branch"`
	Brief    string  `json:"brief"`
	QItemID  string  `json:"qitem"`
	Seat     string  `json:"seat"`
	Worktree string  `json:"worktree"`
}

// ParseDispatcherState decodes the old dispatcher's state file.
func ParseDispatcherState(data []byte) (*DispatcherState, error) {
	var state DispatcherState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("import: decode dispatcher state: %w", err)
	}
	if state.Dispatched == nil {
		state.Dispatched = map[string]DispatchRecord{}
	}
	return &state, nil
}

// ProjectByKey finds a project row by its registry key.
func (s *Store) ProjectByKey(ctx context.Context, key string) (*ProjectRow, error) {
	projects, err := s.Projects(ctx)
	if err != nil {
		return nil, err
	}
	for index := range projects {
		if projects[index].Key == key {
			return &projects[index], nil
		}
	}
	return nil, nil
}

// ImportDispatches copies the old dispatcher's per-item records into the dispatch
// table. Existing (project, item, attempt) rows are left alone; the count is the
// number inserted.
func (s *Store) ImportDispatches(ctx context.Context, projectID int64, state *DispatcherState) (int, error) {
	if state == nil {
		return 0, fmt.Errorf("import: state is required")
	}
	inserted := 0
	for raw, record := range state.Dispatched {
		number, err := strconv.Atoi(raw)
		if err != nil || number <= 0 {
			continue
		}
		attempt := record.Attempt
		if attempt <= 0 {
			attempt = 1
		}
		var existing int
		if err := s.db.QueryRowContext(ctx,
			`SELECT count(*) FROM dispatch WHERE project_id = ? AND item_number = ? AND attempt = ?`,
			projectID, number, attempt).Scan(&existing); err != nil {
			return inserted, fmt.Errorf("import: check dispatch %d: %w", number, err)
		}
		if existing > 0 {
			continue
		}
		created := time.Unix(int64(record.At), 0).UTC()
		if _, err := s.CreateDispatch(ctx, NewDispatch{
			ProjectID: projectID, ItemNumber: number, Attempt: attempt, Seat: record.Seat,
			Branch: record.Branch, Worktree: record.Worktree, BaseSHA: record.Base,
			QItemID: record.QItemID, BriefPath: record.Brief, CreatedAt: created,
		}); err != nil {
			return inserted, err
		}
		inserted++
	}
	return inserted, nil
}
