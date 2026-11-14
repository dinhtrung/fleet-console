package act

import (
	"context"
	"sort"
	"time"

	"github.com/dinhtrung/fleet-console/internal/decide"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// recordPlanReasons keeps this plan's per-item reason in the store so the board
// can answer "why is this item not moving" after the tick that produced it
// (#428, design D4). #411 named the skip on the journal, which is right for
// somebody reading logs and useless for somebody reading the board — and a
// restart erases the journal line anyway.
//
// The rows are the plan's whole answer for the project: every item the plan left
// undispatched, with no history. The store replaces the project's table with
// them, so an item that this tick dispatched, or that left the forge, loses its
// row without the tick having to name it — the plan does not skip either one, and
// the table is the last plan, not a log.
//
// A plan that did not run writes nothing. A stale forge read planned nothing
// (decide returns StatusStale before any rule runs), and the same rule that
// keeps a stale read from vouching for demand keeps it from clearing the board:
// a failed read proves nothing about the item. A disabled or on-hold project is
// likewise not a tick source, so its last answer stands rather than being
// replaced by an empty one.
//
// The write is best-effort, like the ledger rows the tick writes elsewhere: this
// column is a display, and a store failure must not fail a tick that has already
// dispatched. The failure is reported as a skip line so it is still visible.
func recordPlanReasons(ctx context.Context, actions *Actions, in TickInput, at time.Time) error {
	if in.Plan.Status == decide.StatusStale || !in.Project.Enabled || in.Project.OnHold {
		return nil
	}
	return actions.Store.ReplacePlanReasons(ctx, in.Project.ID, planReasonRows(in.CRs, in.Plan, at))
}

// planReasonRows is the table's content for one plan: one row per skipped item,
// ordered by item number so a test and a human read the same order twice. A
// duplicate skip for one item is collapsed — the last one wins — because the
// table holds one row per item by construction, and two answers for one item is
// a plan bug rather than two facts.
//
// The head is the item's open change request, the same head the dispatch.skip
// ledger key carries (skipHead), so a re-review that moves the head leaves a
// different record rather than a stale one.
func planReasonRows(crs []forge.ChangeRequest, plan decide.Plan, at time.Time) []store.PlanReason {
	rows := make([]store.PlanReason, 0, len(plan.Skips))
	index := make(map[int]int, len(plan.Skips))
	for _, skip := range plan.Skips {
		row := store.PlanReason{
			ItemNumber: skip.Number,
			Reason:     string(skip.Reason),
			Detail:     skip.Detail,
			Head:       skipHead(crs, skip.Number),
			TickAt:     at,
		}
		if position, ok := index[skip.Number]; ok {
			rows[position] = row
			continue
		}
		index[skip.Number] = len(rows)
		rows = append(rows, row)
	}
	sort.Slice(rows, func(a, b int) bool { return rows[a].ItemNumber < rows[b].ItemNumber })
	return rows
}
