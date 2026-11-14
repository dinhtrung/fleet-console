package store

import (
	"context"
	"fmt"
	"time"
)

// PlanReason is one item's last plan reason: the plan left the item
// undispatched, and this is why (#428, design D4). It is one row per item, not
// one per tick — a skip that repeats overwrites the row and carries the later
// time, and a skip that stops (the item dispatched) deletes it. No history is
// kept, so an operator reading the board sees what the last plan thought, which
// is the question the board is actually asked.
//
// Head is the item's open change request at plan time — the same head the
// dispatch.skip ledger key uses (act.skipHead) — so a re-review that moves the
// head is visibly a different reason from the one recorded against the old one.
// Empty is "the item had no open change request", not "unknown".
type PlanReason struct {
	ProjectID  int64
	ItemNumber int
	Reason     string
	Detail     string
	Head       string
	TickAt     time.Time
}

// ReplacePlanReasons rewrites one project's plan reasons in a transaction: the
// rows given are the whole answer for this project, and any other row for it is
// gone. The caller passes the plan's skips minus the items it dispatched, so a
// dispatched item's row disappears in the same tick, and an item that left the
// forge disappears with it (it is no longer in any plan).
//
// Replace rather than upsert-then-prune because the table is a snapshot of one
// plan, the same shape ReplaceItems uses for the mirror: the alternative keeps a
// row for an item nothing plans any more, which the board would show as a live
// reason for work that is gone.
//
// The insert runs WHERE EXISTS on the project row, like ReplaceItems, so a
// project deleted between the tick's read and this write leaves no orphan rows.
func (s *Store) ReplacePlanReasons(ctx context.Context, projectID int64, reasons []PlanReason) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: replace plan reasons: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM plan_reason WHERE project_id = ?", projectID); err != nil {
		return fmt.Errorf("store: replace plan reasons: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO plan_reason (project_id, item_number, reason, detail, head, tick_at)
		SELECT ?, ?, ?, ?, ?, ?
		WHERE EXISTS (SELECT 1 FROM project WHERE id = ?)`)
	if err != nil {
		return fmt.Errorf("store: replace plan reasons: %w", err)
	}
	defer stmt.Close()
	for _, reason := range reasons {
		tickAt := reason.TickAt
		if tickAt.IsZero() {
			tickAt = time.Now().UTC()
		}
		if _, err := stmt.ExecContext(ctx, projectID, reason.ItemNumber, reason.Reason,
			reason.Detail, reason.Head, tickAt.UTC().Format(time.RFC3339Nano), projectID); err != nil {
			return fmt.Errorf("store: replace plan reason %d: %w", reason.ItemNumber, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: replace plan reasons: %w", err)
	}
	return nil
}

// PlanReasons reads one project's plan reasons, ordered by item number so a
// surface renders them in the order the board lists the items.
func (s *Store) PlanReasons(ctx context.Context, projectID int64) ([]PlanReason, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT project_id, item_number, reason, detail, head, tick_at
		FROM plan_reason WHERE project_id = ? ORDER BY item_number`, projectID)
	if err != nil {
		return nil, fmt.Errorf("store: plan reasons: %w", err)
	}
	defer rows.Close()
	var out []PlanReason
	for rows.Next() {
		var reason PlanReason
		var tickAt string
		if err := rows.Scan(&reason.ProjectID, &reason.ItemNumber, &reason.Reason,
			&reason.Detail, &reason.Head, &tickAt); err != nil {
			return nil, fmt.Errorf("store: plan reasons: %w", err)
		}
		if at, err := time.Parse(time.RFC3339Nano, tickAt); err == nil {
			reason.TickAt = at
		}
		out = append(out, reason)
	}
	return out, rows.Err()
}
