package act

import (
	"context"
	"fmt"
	"strings"

	"github.com/dinhtrung/fleet-console/internal/store"
)

// reconcileVerdictQuestions closes the queue row of a verdict question whose
// premise has ended (#376): a verdict now exists for the head the question
// named, or the change request it was about is merged or closed. This is the
// reconcile arm, not a post-merge step — a question can end because someone
// else answered it, or because the change request died — so it runs once per
// tick over the ledger, like the merge-repair reconcile does.
//
// The row's own state decides whether there is work: a terminal row is left
// alone, so a repeated tick is a no-op and a failed resolve is retried on the
// next one. The resolution text names the note and the ending, so the row's
// history explains itself.
func (a *Actions) reconcileVerdictQuestions(ctx context.Context, in TickInput, report *TickReport) {
	// The questions come from the store's own project-scoped read (#377): the
	// event table has no project column, so a MAX(id) GROUP BY subject query
	// would reconcile one project with another's facts, and would visit only the
	// newest of two questions on one item.
	projectKey := in.Project.Key
	if projectKey == "" {
		projectKey = a.ProjectKey
	}
	questions, err := a.Store.UnansweredQuestions(ctx, in.Project.ID, projectKey)
	if err != nil {
		report.Skipped = append(report.Skipped, fmt.Sprintf("verdict question reconcile: %v", err))
		return
	}
	for _, question := range questions {
		ending := a.questionEnding(ctx, in, question)
		if ending == "" {
			continue
		}
		row, err := a.Rig.QueueShow(ctx, question.QItemID)
		if err != nil {
			report.Skipped = append(report.Skipped, fmt.Sprintf("#%d verdict question reconcile: %v", question.Item, err))
			continue
		}
		if repairRowTerminal(row) {
			continue
		}
		if _, err := a.ResolveRow(ctx, question.QItemID, "resolved: "+ending); err != nil {
			report.Skipped = append(report.Skipped, fmt.Sprintf("#%d verdict question reconcile: %v", question.Item, err))
			continue
		}
		// The journal line is the tick's one visible record; the row.resolve
		// event and the queue's own resolution are the durable ones.
		report.Skipped = append(report.Skipped, fmt.Sprintf("#%d verdict question row resolved: %s", question.Item, ending))
	}
}

// questionEnding names why a question's premise is over, or "" while it stands.
// The change request is read first: its fate ends the question whatever the
// notes say, and it is the ending that needs no verdict to explain.
func (a *Actions) questionEnding(ctx context.Context, in TickInput, question store.QuestionRow) string {
	if question.ChangeRequest > 0 {
		for _, request := range in.CRs {
			if request.Number != question.ChangeRequest {
				continue
			}
			switch {
			case request.Merged || strings.EqualFold(request.State, "merged"):
				return fmt.Sprintf("change request #%d merged; note %s answered", question.ChangeRequest, question.NoteID)
			case strings.EqualFold(request.State, "closed"):
				return fmt.Sprintf("change request #%d closed; note %s answered", question.ChangeRequest, question.NoteID)
			}
		}
	}
	verdicts, err := a.Store.VerdictsForItem(ctx, in.Project.ID, question.Item)
	if err != nil {
		return ""
	}
	for _, verdict := range verdicts {
		if question.Head != "" && !strings.EqualFold(verdict.SHA, question.Head) {
			continue
		}
		return fmt.Sprintf("verdict %s on head %s (note %s)", verdict.Decision, verdict.SHA, question.NoteID)
	}
	return ""
}
