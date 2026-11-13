package act

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// questionNote is one decision.verdict_question event read back: the note the
// gate could not read, the change request it sat on, the head it named and the
// queue row the raise created (#376).
type questionNote struct {
	Note          string
	ChangeRequest int
	Head          string
	RowID         string
}

// parseQuestionNote reads the fixed text act.RaiseVerdictQuestion writes:
//
//	<project> #<item>: note <note> on change request #<cr> cannot be read as a
//	verdict (<reason>); asked <dest>; note=<note>; head=<head>;; row <qid>
//
// The reason is free prose, so only the framed fields are read; a detail that
// does not carry them is skipped, never guessed at.
func parseQuestionNote(detail string) questionNote {
	var out questionNote
	if at := strings.Index(detail, "; note="); at >= 0 {
		rest := strings.TrimPrefix(detail[at:], "; note=")
		if end := strings.IndexByte(rest, ';'); end > 0 {
			out.Note = strings.TrimSpace(rest[:end])
		}
	}
	if at := strings.Index(detail, "change request #"); at >= 0 {
		rest := strings.TrimPrefix(detail[at:], "change request #")
		digits := ""
		for _, r := range rest {
			if r < '0' || r > '9' {
				break
			}
			digits += string(r)
		}
		if number, err := strconv.Atoi(digits); err == nil {
			out.ChangeRequest = number
		}
	}
	if at := strings.Index(detail, "head="); at >= 0 {
		rest := strings.TrimPrefix(detail[at:], "head=")
		if end := strings.IndexByte(rest, ';'); end >= 0 {
			out.Head = strings.TrimSpace(rest[:end])
		}
	}
	out.RowID = repairRowFromDetail(detail)
	return out
}

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
	events, err := a.Store.NewestEventsByKind(ctx, "decision.verdict_question")
	if err != nil {
		report.Skipped = append(report.Skipped, fmt.Sprintf("verdict question reconcile: %v", err))
		return
	}
	for _, event := range events {
		item := repairItemFromSubject(event.Subject)
		note := parseQuestionNote(event.Detail)
		if item == 0 || note.RowID == "" {
			continue
		}
		ending := a.questionEnding(ctx, in, item, note)
		if ending == "" {
			continue
		}
		row, err := a.Rig.QueueShow(ctx, note.RowID)
		if err != nil {
			report.Skipped = append(report.Skipped, fmt.Sprintf("#%d verdict question reconcile: %v", item, err))
			continue
		}
		if repairRowTerminal(row) {
			continue
		}
		if _, err := a.ResolveRow(ctx, note.RowID, "resolved: "+ending); err != nil {
			report.Skipped = append(report.Skipped, fmt.Sprintf("#%d verdict question reconcile: %v", item, err))
			continue
		}
		// The journal line is the tick's one visible record; the row.resolve
		// event and the queue's own resolution are the durable ones.
		report.Skipped = append(report.Skipped, fmt.Sprintf("#%d verdict question row resolved: %s", item, ending))
	}
}

// questionEnding names why a question's premise is over, or "" while it stands.
// The change request is read first: its fate ends the question whatever the
// notes say, and it is the ending that needs no verdict to explain.
func (a *Actions) questionEnding(ctx context.Context, in TickInput, item int, note questionNote) string {
	if note.ChangeRequest > 0 {
		for _, request := range in.CRs {
			if request.Number != note.ChangeRequest {
				continue
			}
			switch {
			case request.Merged || strings.EqualFold(request.State, "merged"):
				return fmt.Sprintf("change request #%d merged; note %s answered", note.ChangeRequest, note.Note)
			case strings.EqualFold(request.State, "closed"):
				return fmt.Sprintf("change request #%d closed; note %s answered", note.ChangeRequest, note.Note)
			}
		}
	}
	verdicts, err := a.Store.VerdictsForItem(ctx, in.Project.ID, item)
	if err != nil {
		return ""
	}
	for _, verdict := range verdicts {
		if note.Head != "" && !strings.EqualFold(verdict.SHA, note.Head) {
			continue
		}
		return fmt.Sprintf("verdict %s on head %s (note %s)", verdict.Decision, verdict.SHA, note.Note)
	}
	return ""
}
