package store

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
)

// QuestionRow is one operator question the ledger still has waiting (#377):
// a note the merge gate could not read as a verdict, read back from the
// `decision.verdict_question` event that raised it.
type QuestionRow struct {
	EventID       int64
	At            string
	Project       string
	Item          int
	ChangeRequest int
	NoteID        string
	Head          string
	Destination   string
	QItemID       string
	// RowState is the queue row's state when the project's rig_queue snapshot
	// held it, and "" when the snapshot had no answer (no read yet, a bounded
	// list, or a row the rig dropped). The list keeps a question whose row is
	// unknown: the ledger's word is the only fact then.
	RowState string
}

// questionText is the fixed text RaiseVerdictQuestion writes:
//
//	<project> #<item>: note <note> on change request #<cr> cannot be read as a
//	verdict (<reason>); asked <destination>; note=<note>; head=<head>; row <qid>
//
// The reason is free prose, so it is matched loosely; everything after it is
// the structured tail the dedupe key already relies on. A detail this does not
// match is skipped, never guessed at.
var questionText = regexp.MustCompile(`^([a-z0-9-]+) #(\d+): note (\S+) on change request #(\d+) cannot be read as a verdict \(.*\); asked (\S+); note=\S+; head=([^;\s]+);+ row (\S+)$`)

// queueSnapshotRow is the part of one `rig queue list` row the join reads.
type queueSnapshotRow struct {
	ID     string `json:"id"`
	State  string `json:"state"`
	Pickup string `json:"pickup_state"`
}

// queueResolved reports whether the queue says a row is finished with it. A
// done or closed row, or a terminal pickup, is resolved; a pending, blocked or
// in-progress row still waits on its destination.
func queueResolved(row queueSnapshotRow) bool {
	return row.State == "done" || row.State == "closed" || row.Pickup == "terminal"
}

// UnansweredQuestions reads one project's `decision.verdict_question` events
// oldest first and joins each with the project's last `rig_queue` snapshot:
// a row the queue says is finished disappears, and a row the snapshot does not
// hold stays. It is one read of the ledger and one of the snapshot — there is
// no polling here; the board calls it when it renders.
func (s *Store) UnansweredQuestions(ctx context.Context, projectID int64, projectKey string) ([]QuestionRow, error) {
	// The event table has no project column: the project is the detail's first
	// token, so the LIKE narrows the scan to the project's own rows. Keys are
	// [a-z0-9-] (config.ValidateProject), so the only wildcard is the one
	// appended here.
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, at, detail FROM event
		WHERE kind = 'decision.verdict_question' AND detail LIKE ?
		ORDER BY id ASC`, projectKey+" #%")
	if err != nil {
		return nil, fmt.Errorf("store: unanswered questions: %w", err)
	}
	// Read every row before asking for the snapshot: the store runs on one
	// connection (the single-writer guard), so a second query while this cursor
	// is open would wait on itself.
	var ledger []QuestionRow
	for rows.Next() {
		var id int64
		var at, detail string
		if err := rows.Scan(&id, &at, &detail); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: unanswered questions: %w", err)
		}
		match := questionText.FindStringSubmatch(detail)
		if match == nil || match[1] != projectKey {
			continue
		}
		item, _ := strconv.Atoi(match[2])
		changeRequest, _ := strconv.Atoi(match[4])
		ledger = append(ledger, QuestionRow{
			EventID: id, At: at, Project: match[1], Item: item, ChangeRequest: changeRequest,
			NoteID: match[3], Destination: match[5], Head: match[6], QItemID: match[7],
		})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("store: unanswered questions: %w", err)
	}
	rows.Close()

	states := s.queueStates(ctx, projectID)
	out := []QuestionRow{}
	for _, question := range ledger {
		if row, ok := states[question.QItemID]; ok {
			question.RowState = row.State
			if queueResolved(row) {
				continue
			}
		}
		out = append(out, question)
	}
	return out, nil
}

// queueStates reads the project's last rig_queue snapshot, keyed by qitem id.
// A missing snapshot is an empty map, not an error: the questions are still
// readable without it, they just carry no row state.
func (s *Store) queueStates(ctx context.Context, projectID int64) map[string]queueSnapshotRow {
	payload, _, err := s.GetSnapshot(ctx, projectID, "rig_queue")
	if err != nil || len(payload) == 0 {
		return map[string]queueSnapshotRow{}
	}
	var rows []queueSnapshotRow
	if err := json.Unmarshal(payload, &rows); err != nil {
		return map[string]queueSnapshotRow{}
	}
	states := make(map[string]queueSnapshotRow, len(rows))
	for _, row := range rows {
		if row.ID != "" {
			states[row.ID] = row
		}
	}
	return states
}
