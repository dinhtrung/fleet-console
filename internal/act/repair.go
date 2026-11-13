package act

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
)

// RaiseResult is already declared in review.go; RaiseMergeRepair returns it too.

// RaiseMergeRepair raises the one decision the merge gate cannot make: an
// accepted change request that cannot merge because its head conflicts with the
// default branch (#251). The row goes to the human, naming the item, the change
// request, the head and the forge's own mergeability word, with the two options —
// rework the branch, or defer the item.
//
// It is once per (item, head sha): the text carries the head, and HasEvent is the
// ledger. A second tick at the same head is a no-op; a rebased head is a new key
// that lands as a note on the item's one open repair row, never a second row
// (#340, design D4) — and the row itself is reconciled with its premise on
// every applied tick, so a resolved row frees the next conflict to raise again.
func (a *Actions) RaiseMergeRepair(ctx context.Context, projectKey string, failure *NotMergeableError, worktreeRoot string) (*RaiseResult, error) {
	if a.Store == nil || a.Rig == nil {
		return nil, fmt.Errorf("raise merge repair: store and rig are required")
	}
	if failure == nil {
		return nil, fmt.Errorf("raise merge repair: no refusal given")
	}
	reason := failure.Reason
	if reason == "" {
		reason = "unknown reason"
	}
	summary := fmt.Sprintf("#%d: accepted change request #%d cannot merge (%s)", failure.Item, failure.Number, reason)
	// The subject and the head are the idempotency key, exactly as the re-review
	// raise uses text-that-names-the-head: same item, same head, one row ever.
	kind := "decision.merge_repair"
	subject := fmt.Sprintf("#%d", failure.Item)
	exists, err := a.Store.HasEvent(ctx, kind, subject, failure.SHA)
	if err != nil {
		return nil, err
	}
	if exists {
		return &RaiseResult{NoOp: true}, nil
	}
	text := fmt.Sprintf("%s #%d: change request #%d carries ACCEPT at %s but cannot merge (%s). Rework the branch onto the current default head, or defer the item?",
		projectKey, failure.Item, failure.Number, failure.SHA, reason)
	// One open row per item (#340, design D4): a later head is a note on the
	// row the newest event names, never a second row. The (item, head) key
	// above already made this head a no-op; a new head first asks whether the
	// item still has an open repair row to carry it.
	noted, err := a.noteRepairRow(ctx, kind, subject, failure, reason, text)
	if err != nil {
		return nil, err
	}
	if noted != nil {
		return noted, nil
	}

	dir := worktreeRoot
	if dir == "" {
		dir = os.TempDir()
	} else {
		dir = filepath.Join(dir, "briefs")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("raise merge repair: brief dir: %w", err)
	}
	sum := sha256.Sum256([]byte(text))
	path := filepath.Join(dir, fmt.Sprintf("merge-repair-%s-%d-%s.md", slug(projectKey), failure.Item, hex.EncodeToString(sum[:4])))
	body := fmt.Sprintf("# Merge repair: %s #%d\n\nChange request #%d carries ACCEPT at %s but the forge reports it cannot merge (%s).\n\n- Branch: %s\n\nTwo options:\n\n1. Rework: rebase %s onto the default branch and resolve the conflicts; the next tick merges it when the head is accepted and green.\n2. Defer: park the item (onhold) and close this row; the branch stays as it is.\n\nReply with the row's answer prefix and your choice.\n",
		projectKey, failure.Item, failure.Number, failure.SHA, reason, failure.Branch, failure.Branch)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return nil, fmt.Errorf("raise merge repair: write brief: %w", err)
	}
	row, err := a.Rig.QueueCreate(ctx, rig.CreateRequest{
		Destination: "human@kernel",
		Summary:     summary,
		BodyFile:    path,
		EvidenceRef: path,
		Source:      "human@kernel",
	})
	if err != nil {
		_ = noteRefusal(ctx, a.Store, failure.Item, err)
		return nil, err
	}
	eventID, err := a.event(ctx, "act", kind, subject, text+"; row "+row.ID)
	if err != nil {
		return nil, err
	}
	return &RaiseResult{QItemID: row.ID, Events: []int64{eventID}}, nil
}

// noteRepairRow carries a later head on the item's open repair row (#340,
// design D4). It returns the result to return when the row was noted — the
// same row id, NoOp so the tick does not page a question that is already
// open — and nil when there is no open row to note, so the raise goes ahead
// and creates one. A failure to read or note the row is an error: raising a
// second row on an unknown row state could duplicate the ask, and the tick's
// error path is a skip line the next tick retries.
func (a *Actions) noteRepairRow(ctx context.Context, kind, subject string, failure *NotMergeableError, reason, text string) (*RaiseResult, error) {
	event, err := a.Store.EventByKindAndSubject(ctx, kind, subject)
	if err != nil {
		return nil, err
	}
	if event == nil {
		return nil, nil
	}
	rowID := repairRowFromDetail(event.Detail)
	if rowID == "" {
		return nil, nil
	}
	row, err := a.Rig.QueueShow(ctx, rowID)
	if err != nil {
		return nil, err
	}
	if repairRowTerminal(row) {
		return nil, nil
	}
	if _, err := a.Rig.QueueUpdate(ctx, rowID, rig.UpdateRequest{Note: fmt.Sprintf(
		"the head moved: %s also cannot merge (%s); this row is still the item's one repair ask",
		failure.SHA, reason)}); err != nil {
		return nil, err
	}
	// The event for the new head keeps the (item, head) ledger true — this
	// head is asked for — and keeps the newest event naming the row the next
	// head and the reconcile will read.
	eventID, err := a.event(ctx, "act", kind, subject, text+"; row "+rowID)
	if err != nil {
		return nil, err
	}
	return &RaiseResult{QItemID: rowID, NoOp: true, Events: []int64{eventID}}, nil
}

// repairRowFromDetail reads the row id the raise appended to the event's
// detail ("…; row qitem-x"). An event without one is a raise whose row was
// never created — nothing to reconcile.
func repairRowFromDetail(detail string) string {
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

// repairItemFromSubject reads the item a repair event names: the subject is
// "#<item>", exactly as the raise wrote it.
func repairItemFromSubject(subject string) int {
	if !strings.HasPrefix(subject, "#") {
		return 0
	}
	item, err := strconv.Atoi(strings.TrimPrefix(subject, "#"))
	if err != nil || item <= 0 {
		return 0
	}
	return item
}

// repairRowTerminal reports whether a shown row state ends the row. It shares
// the terminal set dispatch reconciliation uses.
func repairRowTerminal(row *rig.QueueItem) bool {
	state := strings.ToLower(strings.TrimSpace(row.State))
	if state == "" {
		state = strings.ToLower(strings.TrimSpace(row.PickupState))
	}
	_, terminal := reconcileTerminal[state]
	return terminal
}

// reconcileMergeRepairs resolves the open repair row whose premise is gone
// (#340, design D4): the item is closed, the change request is closed or
// merged, or the current head is green with an ACCEPT naming it. The
// resolution goes through ResolveRow — the same path a human answer uses — so
// a board answer and an automatic resolution cannot both win. A rig that
// refuses the write produces a skip line; the next tick retries. A head that
// merely moved ends nothing: a moved head with an open conflict still needs
// the decision.
func (a *Actions) reconcileMergeRepairs(ctx context.Context, in TickInput, report *TickReport) {
	events, err := a.Store.NewestEventsByKind(ctx, "decision.merge_repair")
	if err != nil {
		report.Skipped = append(report.Skipped, fmt.Sprintf("merge repair reconcile: %v", err))
		return
	}
	for _, event := range events {
		item := repairItemFromSubject(event.Subject)
		if item == 0 {
			continue
		}
		rowID := repairRowFromDetail(event.Detail)
		if rowID == "" {
			continue
		}
		note := repairPremiseEnded(ctx, in, report, item)
		if note == "" {
			continue
		}
		row, err := a.Rig.QueueShow(ctx, rowID)
		if err != nil {
			report.Skipped = append(report.Skipped, fmt.Sprintf("#%d merge repair reconcile: %v", item, err))
			continue
		}
		if repairRowTerminal(row) {
			continue
		}
		if _, err := a.ResolveRow(ctx, rowID, "resolved: "+note); err != nil {
			report.Skipped = append(report.Skipped, fmt.Sprintf("#%d merge repair reconcile: %v", item, err))
			continue
		}
		// The journal line is the tick's one visible record of the
		// resolution; the row.resolve event and the queue's own resolution
		// are the durable ones.
		report.Skipped = append(report.Skipped, fmt.Sprintf("#%d repair resolved: %s", item, note))
	}
}

// repairPremiseEnded names the fact that ends the item's repair ask, and is
// empty while the premise holds. The snapshot's change request still says
// what it said when the plan was built, so a merge this tick landed shows up
// in the report, not in the snapshot.
func repairPremiseEnded(ctx context.Context, in TickInput, report *TickReport, item int) string {
	if it := itemFor(in.Items, item); it != nil && !strings.EqualFold(it.State, forge.StateOpen) {
		return fmt.Sprintf("item #%d is %s", item, it.State)
	}
	cr := changeRequestFor(in.CRs, item)
	if cr == nil {
		return ""
	}
	if cr.Merged {
		return fmt.Sprintf("change request #%d merged", cr.Number)
	}
	switch strings.ToLower(cr.State) {
	case "closed", "merged":
		return fmt.Sprintf("change request #%d is %s", cr.Number, strings.ToLower(cr.State))
	}
	// The merge loop above this call: the head the row conflicts with is the
	// head that just merged.
	for _, merged := range report.Merges {
		if merged.Merged && merged.ChangeRequest == cr.Number {
			return fmt.Sprintf("change request #%d merged at %s", cr.Number, merged.SHA)
		}
	}
	// The current head is green with an ACCEPT naming it: the conflict is
	// gone — a rebase landed and the reviewer re-accepted — so the rework/
	// defer question is moot even where this tick's merge was withheld for a
	// reason the next tick clears.
	if !cr.Mergeable {
		return ""
	}
	green, _ := cr.HeadGreen()
	if !green {
		return ""
	}
	for _, captured := range report.Verdicts {
		if captured.Item == item && strings.EqualFold(captured.Decision, "accept") &&
			forge.SameSHA(captured.SHA, cr.HeadSHA) {
			return fmt.Sprintf("ACCEPT at %s names the current head and it is green", cr.HeadSHA)
		}
	}
	verdicts, err := in.Store.VerdictsForItem(ctx, in.Project.ID, item)
	if err != nil {
		return ""
	}
	for _, verdict := range verdicts {
		if strings.EqualFold(verdict.Decision, "accept") && forge.SameSHA(verdict.SHA, cr.HeadSHA) {
			return fmt.Sprintf("ACCEPT at %s names the current head and it is green", cr.HeadSHA)
		}
	}
	return ""
}
