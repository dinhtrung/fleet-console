package act

import (
	"context"
	"fmt"
)

// EpicRollup is one epic the tick closed because a merge finished its last
// sub-issue. It is on the report so a caller can say what happened without
// re-reading the audit table.
type EpicRollup struct {
	Epic     int
	Children int
}

// rollupAfterMerge applies the rollup rule to a merged item and, when it says
// close, comments on and closes the parent epic.
//
// The courtesy rule from the issue shapes every failure path here: the merge has
// already landed, so nothing in this function may report it as failed. A forge
// error becomes a skip line — `#<epic> rollup: <error>` — and the next tick
// retries, because the epic is still open.
//
// closed carries the epics this tick has already closed, so two merged items
// that share a parent produce one close rather than two.
func (a *Actions) rollupAfterMerge(
	ctx context.Context,
	in TickInput,
	report *TickReport,
	closed map[int]bool,
	item int,
	merged *MergeResult,
) {
	parentOf := a.parentFor(ctx, in, item)
	decision, err := Rollup(parentOf, in.Items)
	if err != nil {
		report.Skipped = append(report.Skipped, fmt.Sprintf("#%d rollup: %v", item, err))
		return
	}
	if !decision.ShouldClose() {
		// Which skips are journaled is the spec's call, not a preference: a parent
		// that is not an epic records nothing ("the parent is left alone"), because
		// most merged items have no parent at all and a line per merge would bury
		// the ones that matter. The three that do record are the epic-shaped ones.
		if journalsSkip(decision) {
			report.Skipped = append(report.Skipped, fmt.Sprintf("#%d rollup: %s", decision.Epic, decision.Reason))
		}
		return
	}
	if closed[decision.Epic] {
		return
	}

	if err := a.Forge.Comment(ctx, decision.Epic, epicComment(item, merged, decision)); err != nil {
		// A permission refusal raises the operator question (#260): attempt 0 —
		// the epic has no dispatch — and the journal line names the permission.
		if notePermission(in, report, a.RefusePermission(ctx, decision.Epic, epicTitle(in, decision.Epic), "comment", err, 0, merged.ChangeRequest, "")) {
			return
		}
		report.Skipped = append(report.Skipped, fmt.Sprintf("#%d rollup: comment: %v", decision.Epic, err))
		return
	}
	if err := a.Forge.CloseItem(ctx, decision.Epic); err != nil {
		if notePermission(in, report, a.RefusePermission(ctx, decision.Epic, epicTitle(in, decision.Epic), "close", err, 0, merged.ChangeRequest, "")) {
			return
		}
		report.Skipped = append(report.Skipped, fmt.Sprintf("#%d rollup: close: %v", decision.Epic, err))
		return
	}
	if _, err := a.event(ctx, "act", "epic.closed", fmt.Sprintf("#%d", decision.Epic),
		fmt.Sprintf("%d sub-issues", decision.Children)); err != nil {
		report.Skipped = append(report.Skipped, fmt.Sprintf("#%d rollup: event: %v", decision.Epic, err))
		return
	}
	closed[decision.Epic] = true
	report.Epics = append(report.Epics, EpicRollup{Epic: decision.Epic, Children: decision.Children})
}

// epicComment is the note left on the epic. It names the item that finished it
// and the change request that carried it, so an operator reading the epic a week
// later does not have to open the audit log to know why it closed.
func epicComment(item int, merged *MergeResult, decision Decision) string {
	request := ""
	if merged != nil && merged.ChangeRequest != 0 {
		request = fmt.Sprintf(" (change request #%d)", merged.ChangeRequest)
	}
	return fmt.Sprintf(
		"#%d merged%s, which was the last open sub-issue: every sub-issue of this epic is closed. Closing it.",
		item, request,
	)
}

// parentFor is the merged item's parent pointer.
//
// The snapshot is the first source, and normally the only one. The store is the
// fallback for the case the guard exists for: a forge read that returned the
// epic but not its sub-issues — the merged item is then absent from the snapshot
// too, and without the cached parent the tick would report "no parent" and never
// reach the guard. A store read is not a forge read, so the tick's request count
// is unchanged.
func (a *Actions) parentFor(ctx context.Context, in TickInput, number int) *int {
	for _, item := range in.Items {
		if item.Number == number {
			return item.Parent
		}
	}
	if a.Store == nil {
		return nil
	}
	row, err := a.Store.Item(ctx, in.Project.ID, number)
	if err != nil || row == nil {
		return nil
	}
	return row.ParentNumber
}

// epicTitle is the epic's title from the snapshot, empty when the snapshot has
// none — the raise then fills the title from the store (#260).
func epicTitle(in TickInput, epic int) string {
	if item := itemFor(in.Items, epic); item != nil {
		return item.Title
	}
	return ""
}

// journalsSkip reports whether a skip is worth a line in the tick's journal. The
// spec names three — still open, no sub-issues in the snapshot, already closed —
// and says a non-epic parent is left alone with nothing recorded. "No parent" is
// the fourth silent case for the same reason: it is the ordinary merged-item
// path, not a rollup that was considered and declined.
func journalsSkip(decision Decision) bool {
	switch decision.Reason {
	case "no parent", "parent not an epic":
		return false
	default:
		return true
	}
}
