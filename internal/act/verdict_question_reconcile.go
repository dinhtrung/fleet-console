package act

import (
	"context"
	"fmt"
	"strings"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// reconcileVerdictQuestions closes the queue row of a verdict question whose
// premise has ended (#376): a verdict now exists for the head the question
// named, the change request it was about is merged or closed, or a verdict
// landed on a later head and overtook the question (#414). This is the
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
		closing := a.questionEnding(ctx, in, question)
		if closing == nil {
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
		if err := a.closeQuestionRow(ctx, question.QItemID, *closing); err != nil {
			report.Skipped = append(report.Skipped, fmt.Sprintf("#%d verdict question reconcile: %v", question.Item, err))
			continue
		}
		// The journal line is the tick's one visible record; the row.close
		// event and the queue's own resolution are the durable ones.
		report.Skipped = append(report.Skipped, fmt.Sprintf("#%d verdict question row resolved: %s", question.Item, closing.Resolution))
	}
}

// questionClose is how a question ends: the text its row and the journal
// record, and — when a later head's verdict overtook the question instead of
// its own premise ending (#414) — the head that superseded it.
type questionClose struct {
	Resolution   string
	SupersededBy string // "" when the question's own premise ended
}

// questionEnding says how a question's premise ended, or nil while it stands.
// The change request is read first: its fate ends the question whatever the
// notes say, and it is the ending that needs no verdict to explain.
func (a *Actions) questionEnding(ctx context.Context, in TickInput, question store.QuestionRow) *questionClose {
	if question.ChangeRequest > 0 {
		for _, request := range in.CRs {
			if request.Number != question.ChangeRequest {
				continue
			}
			switch {
			case request.Merged || strings.EqualFold(request.State, "merged"):
				return &questionClose{Resolution: fmt.Sprintf("change request #%d merged; note %s answered", question.ChangeRequest, question.NoteID)}
			case strings.EqualFold(request.State, "closed"):
				return &questionClose{Resolution: fmt.Sprintf("change request #%d closed; note %s answered", question.ChangeRequest, question.NoteID)}
			}
		}
	}
	verdicts, err := a.Store.VerdictsForItem(ctx, in.Project.ID, question.Item)
	if err != nil {
		return nil
	}
	for _, verdict := range verdicts {
		if question.Head != "" && !strings.EqualFold(verdict.SHA, question.Head) {
			continue
		}
		return &questionClose{Resolution: fmt.Sprintf("verdict %s on head %s (note %s)", verdict.Decision, verdict.SHA, question.NoteID)}
	}
	// A verdict on the head the change request carries now answers every
	// earlier head's question (#414): the reviewer has moved on, so nobody is
	// going to answer a question about a commit the branch no longer carries.
	// The operator was closing these by hand, six of them on 2026-11-13.
	return supersededQuestion(verdicts, question, changeRequestHead(in.CRs, question.ChangeRequest))
}

// supersededQuestion is the #414 arm: a question about an older head whose
// change request now carries a head that has a verdict. A question about the
// head itself is left to the verdict arm above, and an unreadable head leaves
// the question standing — this reconcile never closes a row on a guess.
func supersededQuestion(verdicts []store.VerdictRow, question store.QuestionRow, head string) *questionClose {
	if head == "" || question.Head == "" || strings.EqualFold(head, question.Head) {
		return nil
	}
	for _, verdict := range verdicts {
		if !strings.EqualFold(verdict.SHA, head) {
			continue
		}
		return &questionClose{
			Resolution: fmt.Sprintf("superseded: verdict %s on head %s; the question asked about %s (note %s)",
				verdict.Decision, verdict.SHA, question.Head, question.NoteID),
			SupersededBy: head,
		}
	}
	return nil
}

// changeRequestHead is the head the forge read carries for a change request, or
// "" when this tick has no such request.
func changeRequestHead(requests []forge.ChangeRequest, number int) string {
	for _, request := range requests {
		if request.Number == number {
			return request.HeadSHA
		}
	}
	return ""
}

// closeQuestionRow closes a pending question row. `rig queue resolve` is for a
// parked (blocked) row and is not a closure even then — a live question row is
// pending, addressed to a seat — so this is the update the operator uses by
// hand: the row ends with the closure that names its kind of ending, and the
// ending text recorded as the resolution. The row.close / row.superseded event
// is the audit trail `ResolveRow` would have written.
func (a *Actions) closeQuestionRow(ctx context.Context, id string, closing questionClose) error {
	if closing.SupersededBy != "" {
		// A superseded question was not answered, it was overtaken, so the row
		// closes the way every other superseded row does (CloseSuperseded):
		// canceled, reason superseded, target the thing that overtook it.
		if _, err := a.Rig.QueueUpdate(ctx, id, rig.UpdateRequest{
			State: "canceled", ClosureReason: "superseded", ClosureTarget: closing.SupersededBy,
			Resolution: closing.Resolution,
		}); err != nil {
			return err
		}
		_, err := a.event(ctx, "act", "row.superseded", id, closing.Resolution)
		return err
	}
	resolution := "resolved: " + closing.Resolution
	if _, err := a.Rig.QueueUpdate(ctx, id, rig.UpdateRequest{
		State: "done", ClosureReason: "no-follow-on", Resolution: resolution,
	}); err != nil {
		return err
	}
	_, err := a.event(ctx, "act", "row.closed", id, resolution)
	return err
}
