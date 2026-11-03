package act

import (
	"context"
	"fmt"
	"strings"

	"github.com/dinhtrung/fleet-console/internal/forge"
)

// sweepEpics runs the epic sweep for one applied tick: every open epic the tick
// observed is re-read through the forge and handed to Sweep.
//
// The tick's own snapshot cannot be the evidence. It is built from a `state=open`
// list (internal/forge/github.go:163), so a closed child is absent from it and
// "no sub-issues" is indistinguishable from "all sub-issues closed" — the trap
// #122 names. Each open epic therefore costs one SubIssues read, which is the
// bound task 3.5 asks for: one page set per open epic per tick, no more.
//
// Order and idempotence: closed carries the epics this tick has already closed,
// shared with the merge loop, so an epic the rollup just closed is not closed a
// second time. Across ticks the dedupe is the epic's own state — a closed epic is
// not in the next tick's open list, so the sweep does not meet it again.
//
// Every failure is a skip line and the tick carries on: a read that errors leaves
// the epic open (closing on a short read would close an epic over work nobody
// looked at), and a comment or close that errors leaves it open too, so the next
// tick's sweep sees the same conditions and tries again. No dedupe table.
func (a *Actions) sweepEpics(ctx context.Context, in TickInput, report *TickReport, closed map[int]bool) {
	for index := range in.Items {
		epic := in.Items[index]
		if epic.Kind != forge.KindEpic {
			continue
		}
		if !strings.EqualFold(epic.State, forge.StateOpen) {
			continue
		}
		if closed[epic.Number] {
			continue
		}
		report.SweepReads++
		children, err := a.Forge.SubIssues(ctx, epic.Number)
		if err != nil {
			// A failed read is never "no open children": the epic stays open.
			report.Skipped = append(report.Skipped, fmt.Sprintf("#%d sweep: %v", epic.Number, err))
			continue
		}
		decision := Sweep(epic, children)
		if !decision.ShouldClose() {
			// Quiet marks the skips that are the ordinary state of an unfinished
			// epic. Only the considered declines reach the journal, so a sweep
			// meeting the same epic every tick does not bury the ticks that did
			// something.
			if !decision.Quiet {
				report.Skipped = append(report.Skipped, fmt.Sprintf("#%d sweep: %s", decision.Epic, decision.Reason))
			}
			continue
		}
		// Post the comment once per epic, not once per tick. The comment goes first
		// because the spec makes a failed comment leave the epic open for a retry,
		// exactly like a failed close; but that retry would otherwise post another
		// comment every tick the close keeps failing, unbounded. Reading the epic's
		// notes and skipping a sweep comment that is already there bounds it to one
		// while keeping the ordering the spec requires. The read is spent only for an
		// epic the sweep has decided to close, so it is not a per-open-epic cost.
		commented, err := a.sweepCommented(ctx, decision.Epic)
		if err != nil {
			// A read that failed cannot be read as "no comment": posting on a failed
			// read is how the accumulation starts. Leave the epic open and retry.
			report.Skipped = append(report.Skipped, fmt.Sprintf("#%d sweep: read notes: %v", decision.Epic, err))
			continue
		}
		if !commented {
			if err := a.Forge.Comment(ctx, decision.Epic, sweepComment(decision)); err != nil {
				report.Skipped = append(report.Skipped, fmt.Sprintf("#%d sweep: comment: %v", decision.Epic, err))
				continue
			}
		}
		if err := a.Forge.CloseItem(ctx, decision.Epic); err != nil {
			report.Skipped = append(report.Skipped, fmt.Sprintf("#%d sweep: close: %v", decision.Epic, err))
			continue
		}
		// The detail names the source: an operator querying epic.closed must be able
		// to tell a sweep close from a merge close without opening the comment.
		if _, err := a.event(ctx, "act", "epic.closed", fmt.Sprintf("#%d", decision.Epic),
			fmt.Sprintf("sweep: %d sub-issues", decision.Children)); err != nil {
			report.Skipped = append(report.Skipped, fmt.Sprintf("#%d sweep: event: %v", decision.Epic, err))
			continue
		}
		closed[decision.Epic] = true
		report.Epics = append(report.Epics, EpicRollup{Epic: decision.Epic, Children: decision.Children})
	}
}

// sweepComment is the note the sweep leaves on an epic it closes. It opens by
// naming the sweep, because the operator who opens the epic later needs to know
// why it closed without reading the audit table: no merge finished this epic, the
// tick's sweep did. The child count is the decision's, the same number the event
// records, not one recomputed here.
func sweepComment(decision Decision) string {
	return fmt.Sprintf(
		"%s all %d sub-issues of this epic are closed. Closing it — no merge finished this epic; "+
			"the tick's epic sweep found every sub-issue closed and closed it.",
		sweepCommentPrefix, decision.Children,
	)
}

// sweepCommentPrefix is the marker both a posted sweep comment and the dedupe
// check agree on. A single constant keeps the comment's opening and the test for
// it from drifting apart.
const sweepCommentPrefix = "Sweep:"

// sweepCommented reports whether the epic already carries a sweep comment, so a
// close retried across ticks does not post one per tick. Rollup comments start
// with "#<item> merged" and never carry this prefix, so the check distinguishes a
// sweep comment from a merge comment without a second field on the forge.
func (a *Actions) sweepCommented(ctx context.Context, epic int) (bool, error) {
	notes, err := a.Forge.Notes(ctx, epic)
	if err != nil {
		return false, err
	}
	for _, note := range notes {
		if strings.HasPrefix(note.Body, sweepCommentPrefix) {
			return true, nil
		}
	}
	return false, nil
}
