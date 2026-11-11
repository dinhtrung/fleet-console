package act

import (
	"context"
	"fmt"
	"sort"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"strings"
)

// NotMergeableError is the merge gate's refusal when the verdict is an accept at
// the head but the forge says the change request cannot merge. A conflicting
// branch never becomes mergeable on its own — the head has to be rebased or the
// conflicts resolved — so this is a repair condition, not a transient one, and the
// tick reacts to it instead of skipping forever (#251). The fields name the facts
// the repair row quotes.
type NotMergeableError struct {
	Number int    // the change request number
	Item   int    // the issue it closes
	SHA    string // the accepted head
	Branch string // the head's branch
	Reason string // the forge's own word, e.g. dirty / cannot_be_merged
}

func (e *NotMergeableError) Error() string {
	if e.Reason == "" {
		return fmt.Sprintf("refusing merge: change request #%d is not mergeable", e.Number)
	}
	return fmt.Sprintf("refusing merge: change request #%d is not mergeable (%s)", e.Number, e.Reason)
}

// BlockedMerge is one merge refusal that is an operator obligation: the head
// will not merge on its own, and the next tick cannot fix it (#252). The tick
// reports the fact every tick it holds; the notifier owns the rate limit, so
// the same stall pages once per (item, reason, head sha).
type BlockedMerge struct {
	Item   int    // the issue the change request closes
	CR     int    // the change request number
	SHA    string // the head the refusal judged
	Reason string // the refusal, as the merge gate worded it
	Action string // what the operator or the builder is expected to do
}

// blockedMerge reports whether a merge refusal needs a human or a rework
// rather than the next tick, and names the action expected (#252). The classes
// are the refusals an ACCEPT cannot survive: conflicts, red checks, a verdict
// that no longer names the head, a draft that could not be readied. A plain
// "no ACCEPT verdict" is the normal wait for a review and is not a stall —
// paging it would page every item under review, every tick.
func blockedMerge(item int, request forge.ChangeRequest, err error) (BlockedMerge, bool) {
	if err == nil {
		return BlockedMerge{}, false
	}
	text := strings.TrimPrefix(err.Error(), "refusing merge: ")
	blocked := BlockedMerge{Item: item, CR: request.Number, SHA: request.HeadSHA}
	switch {
	case strings.Contains(text, "is not mergeable"):
		blocked.Reason, blocked.Action = text, "rebase onto the default branch and re-accept"
	case strings.Contains(text, "checks are not green"):
		blocked.Reason, blocked.Action = text, "fix or re-run the checks, then re-accept"
	case strings.Contains(text, "does not match head"):
		blocked.Reason, blocked.Action = text, "re-review at the current head and accept it"
	case strings.Contains(text, "could not be marked ready"):
		blocked.Reason, blocked.Action = text, "mark the change request ready for review"
	default:
		return BlockedMerge{}, false
	}
	return blocked, true
}

// MergeResult is the evidence of one watchdog merge.
//
// AuditGaps names every ledger write that failed after the merge had already
// landed. Those are reported rather than returned: past that point the return
// value describes the merge, and a merge that happened must not be reported as
// one that did not. Before that point a ledger failure is returned instead,
// because refusing then still leaves the pull request untouched and the row
// retryable on the next tick.
type MergeResult struct {
	Merged        bool
	ChangeRequest int
	SHA           string
	ClosedItem    bool
	ClosedByApp   bool
	MarkerCleared bool
	NextUnblocked int
	Events        []int64
	AuditGaps     []string
}

// gap records a ledger failure that must not fail the call, because the merge
// has already landed. Keeping it on the result is the only option left: the
// durable record is incomplete, and the caller is the one able to say so.
func gap(result *MergeResult, kind string, err error) {
	result.AuditGaps = append(result.AuditGaps, fmt.Sprintf("%s: %v", kind, err))
}

// Merge re-reads the four facts and refuses, naming the missing one. Only then
// does it call the forge merge at the verdict's sha.
func (a *Actions) Merge(ctx context.Context, dispatchID int64, item int) (*MergeResult, error) {
	if a.Store == nil || a.Forge == nil {
		return nil, fmt.Errorf("merge: store and forge are required")
	}
	dispatch, err := a.Store.Dispatch(ctx, dispatchID)
	if err != nil {
		return nil, err
	}
	if dispatch == nil {
		return nil, fmt.Errorf("refusing merge: dispatch %d not found", dispatchID)
	}
	verdicts, err := a.Store.Verdicts(ctx, dispatchID)
	if err != nil {
		return nil, err
	}
	if len(verdicts) == 0 {
		return nil, fmt.Errorf("refusing merge: no ACCEPT verdict for dispatch %d", dispatchID)
	}
	verdict := verdicts[0]
	if verdict.Decision != "accept" {
		return nil, fmt.Errorf("refusing merge: newest verdict for dispatch %d is %s", dispatchID, verdict.Decision)
	}
	requests, err := a.Forge.ChangeRequests(ctx)
	if err != nil {
		return nil, err
	}
	var request *forge.ChangeRequest
	for index := range requests {
		candidate := &requests[index]
		if candidate.ClosesItem != item && forge.ParseCloses(candidate.Body) != item {
			continue
		}
		// A closed or merged change request can never merge again, but it stays in the
		// list: a superseded pull request that also says "Closes #<item>" must not shadow
		// the live one, or a fresh ACCEPT at the live head is refused against a dead head.
		if candidate.Merged || strings.EqualFold(candidate.State, "closed") {
			continue
		}
		request = candidate
		break
	}
	if request == nil {
		return nil, fmt.Errorf("refusing merge: no change request closes #%d", item)
	}
	if verdict.SHA != request.HeadSHA {
		return nil, fmt.Errorf("refusing merge: verdict sha %s does not match head %s", verdict.SHA, request.HeadSHA)
	}
	if green, reason := request.HeadGreen(); !green {
		return nil, fmt.Errorf("refusing merge: checks are not green: %s", reason)
	}
	if !request.Mergeable {
		return nil, &NotMergeableError{
			Number: request.Number, Item: item, SHA: verdict.SHA,
			Branch: request.Branch, Reason: request.MergeState,
		}
	}
	if closes := forge.ParseCloses(request.Body); closes != item {
		return nil, fmt.Errorf("refusing merge: body does not close #%d", item)
	}

	// A draft pull request cannot be merged: GitHub answers `Pull request is still a
	// draft`, which reads like a real refusal in the log. Readiness is the integrator's.
	if request.Draft {
		if err := a.Forge.Ready(ctx, request.Number); err != nil {
			return nil, fmt.Errorf("refusing merge: change request #%d is a draft and could not be marked ready: %w", request.Number, err)
		}
		// Before Forge.Merge, so a ledger failure still refuses: the pull
		// request stays out of draft and unmerged, and the next tick retries.
		if _, err := a.event(ctx, "act", "change_request.ready", fmt.Sprintf("#%d", item),
			fmt.Sprintf("change request #%d taken out of draft before merge", request.Number)); err != nil {
			return nil, fmt.Errorf("refusing merge: change request #%d is ready but the ledger could not record it: %w", request.Number, err)
		}
	}

	if err := a.Forge.Merge(ctx, request.Number, verdict.SHA); err != nil {
		return nil, err
	}
	// Past this point the merge has landed, so nothing below returns an error for
	// a bookkeeping failure: `nil, err` from here would tell the caller a merge
	// did not happen when it did. Failures are collected in AuditGaps instead.
	result := &MergeResult{Merged: true, ChangeRequest: request.Number, SHA: verdict.SHA}
	if eventID, err := a.event(ctx, "act", "merge", fmt.Sprintf("#%d", item),
		fmt.Sprintf("change request #%d at %s", request.Number, verdict.SHA)); err != nil {
		gap(result, "merge event", err)
	} else {
		result.Events = append(result.Events, eventID)
	}
	if dispatch.EndedAt != nil {
		// The builder ended its row when it delivered; the merge still has to land and
		// the item still has to close, so the row is bookkeeping, not the trigger.
		if eventID, err := a.event(ctx, "act", "dispatch.merged", fmt.Sprintf("#%d", item), "merge landed after the row ended: "+dispatch.EndReason); err != nil {
			gap(result, "dispatch.merged event", err)
		} else {
			result.Events = append(result.Events, eventID)
		}
	} else if err := a.Store.EndDispatch(ctx, dispatchID, "merged"); err != nil {
		gap(result, "end dispatch", err)
	}
	if eventID, err := a.event(ctx, "act", "dispatch.merged", fmt.Sprintf("#%d", item), "dispatch ended merged"); err != nil {
		gap(result, "dispatch.merged event", err)
	} else {
		result.Events = append(result.Events, eventID)
	}
	finalized, err := a.Finalize(ctx, dispatchID, item, *request, result)
	if err != nil {
		// The merge has landed, so this is a gap, not a failure: returning the
		// error would tell the caller a merge did not happen. `finalized` carries
		// the partial result, and the failure is named beside it.
		if finalized == nil {
			finalized = result
		}
		gap(finalized, "post-merge finalize", err)
	}
	return finalized, nil
}

// Finalize is the post-merge pass: verify the item closed, close it when the body
// only referenced it, clear the marker and note the next unblocked item.
func (a *Actions) Finalize(ctx context.Context, dispatchID int64, item int, request forge.ChangeRequest, result *MergeResult) (*MergeResult, error) {
	if result == nil {
		result = &MergeResult{Merged: true, ChangeRequest: request.Number, SHA: request.HeadSHA}
	}
	items, err := a.Forge.Items(ctx, forge.Timeout{})
	if err != nil {
		return result, err
	}
	var current *forge.Item
	next := 0
	for index := range items {
		entry := items[index]
		if entry.Number == item {
			current = &items[index]
		}
		if entry.State == forge.StateOpen && entry.Kind == forge.KindTask {
			for _, link := range entry.BlockedBy {
				if link.Number == item {
					if next == 0 || entry.Number < next {
						next = entry.Number
					}
				}
			}
		}
	}
	if current != nil && current.State != forge.StateClosed {
		if err := a.Forge.CloseItem(ctx, item); err != nil {
			return result, err
		}
		result.ClosedByApp = true
		detail := fmt.Sprintf("merge did not close #%d; closed by the app", item)
		if forge.ParseCloses(request.Body) != item {
			detail = fmt.Sprintf("change request #%d only referenced #%d (Refs); closed by the app", request.Number, item)
		}
		if eventID, err := a.event(ctx, "act", "item.close", fmt.Sprintf("#%d", item), detail); err != nil {
			gap(result, "item.close event", err)
		} else {
			result.Events = append(result.Events, eventID)
		}
	} else if current != nil {
		result.ClosedItem = true
	}
	// The forge writes here (CloseItem, SetMarker) still return errors: an item
	// that stays open or a marker left behind is a real failure an operator must
	// act on. Only the ledger writes below are gaps, per the rule above.
	if err := a.Forge.SetMarker(ctx, item, false); err != nil {
		if eventID, eventErr := a.event(ctx, "act", "marker.clear.failed", fmt.Sprintf("#%d", item), err.Error()); eventErr != nil {
			gap(result, "marker.clear.failed event", eventErr)
		} else {
			result.Events = append(result.Events, eventID)
		}
	} else {
		result.MarkerCleared = true
		if eventID, err := a.event(ctx, "act", "marker.clear", fmt.Sprintf("#%d", item), "in-flight marker cleared"); err != nil {
			gap(result, "marker.clear event", err)
		} else {
			result.Events = append(result.Events, eventID)
		}
	}
	if next > 0 {
		result.NextUnblocked = next
		if eventID, err := a.event(ctx, "act", "item.unblocked", fmt.Sprintf("#%d", next),
			fmt.Sprintf("waits only on #%d", item)); err != nil {
			gap(result, "item.unblocked event", err)
		} else {
			result.Events = append(result.Events, eventID)
		}
	}
	sort.Slice(result.Events, func(i, j int) bool { return result.Events[i] < result.Events[j] })
	return result, nil
}

// branchPrefix is the app's branch convention: feature/<item>-<slug>. It is the
// only cheap way to reject a pull request that merely mentions the item, and a
// hand-made branch that follows it is treated exactly like the app's own.
func branchPrefix(item int) string { return fmt.Sprintf("feature/%d-", item) }

// MergeAdopted merges a change request the app never dispatched. It enforces the
// same four facts as Merge — mergeable, `Closes #<item>`, green checks, ACCEPT at
// the head sha — plus two refusals adoption alone needs, and it invents no
// dispatch row (design D2).
//
// The verdict is read by item, because there is no dispatch to key on; the
// caller has already captured it (design D3), so a missing verdict here means the
// reviewer has not spoken.
func (a *Actions) MergeAdopted(ctx context.Context, projectID int64, item int, request forge.ChangeRequest) (*MergeResult, error) {
	if a.Store == nil || a.Forge == nil {
		return nil, fmt.Errorf("merge adopted: store and forge are required")
	}
	// A live dispatch on a *different* branch is the dangerous case: two branches
	// close one item, and which work survives is a human decision, so this refuses
	// rather than picking a winner (design D4).
	live, err := a.Store.Dispatches(ctx, projectID, item)
	if err != nil {
		return nil, err
	}
	for _, dispatch := range live {
		if dispatch.EndedAt != nil {
			continue
		}
		if dispatch.Branch != request.Branch {
			return nil, fmt.Errorf("refusing merge: #%d has a live dispatch on %s and this change request is on %s",
				item, dispatch.Branch, request.Branch)
		}
	}
	if !strings.HasPrefix(request.Branch, branchPrefix(item)) {
		return nil, fmt.Errorf("refusing merge: change request #%d is on %s, which does not start with %s",
			request.Number, request.Branch, branchPrefix(item))
	}
	verdicts, err := a.Store.VerdictsForItem(ctx, projectID, item)
	if err != nil {
		return nil, err
	}
	if len(verdicts) == 0 {
		return nil, fmt.Errorf("refusing merge: no ACCEPT verdict for #%d", item)
	}
	verdict := verdicts[0]
	if verdict.Decision != "accept" {
		return nil, fmt.Errorf("refusing merge: newest verdict for #%d is %s", item, verdict.Decision)
	}
	if verdict.SHA != request.HeadSHA {
		return nil, fmt.Errorf("refusing merge: verdict sha %s does not match head %s", verdict.SHA, request.HeadSHA)
	}
	if green, reason := request.HeadGreen(); !green {
		return nil, fmt.Errorf("refusing merge: checks are not green: %s", reason)
	}
	if !request.Mergeable {
		return nil, &NotMergeableError{
			Number: request.Number, Item: item, SHA: verdict.SHA,
			Branch: request.Branch, Reason: request.MergeState,
		}
	}
	if closes := forge.ParseCloses(request.Body); closes != item {
		return nil, fmt.Errorf("refusing merge: body does not close #%d", item)
	}

	// The trail is written before the merge, because it is the only record of why
	// this ran: a ledger failure here still refuses and the next tick retries.
	if _, err := a.event(ctx, "act", "change_request.adopted", fmt.Sprintf("#%d", item),
		fmt.Sprintf("change request #%d at %s merged with no dispatch", request.Number, request.HeadSHA)); err != nil {
		return nil, fmt.Errorf("refusing merge: change request #%d adopted but the ledger could not record it: %w", request.Number, err)
	}
	if request.Draft {
		if err := a.Forge.Ready(ctx, request.Number); err != nil {
			return nil, fmt.Errorf("refusing merge: change request #%d is a draft and could not be marked ready: %w", request.Number, err)
		}
	}
	if err := a.Forge.Merge(ctx, request.Number, verdict.SHA); err != nil {
		return nil, err
	}
	// Past this point the merge has landed: bookkeeping failures are gaps, not
	// failures, exactly as in Merge.
	result := &MergeResult{Merged: true, ChangeRequest: request.Number, SHA: verdict.SHA}
	if eventID, err := a.event(ctx, "act", "merge", fmt.Sprintf("#%d", item),
		fmt.Sprintf("change request #%d at %s (adopted)", request.Number, verdict.SHA)); err != nil {
		gap(result, "merge event", err)
	} else {
		result.Events = append(result.Events, eventID)
	}
	// No dispatch: no EndDispatch, no dispatch.merged. Finalize reads the request
	// directly, so the item still closes, the marker still clears and a blocked
	// sibling is still noted.
	finalized, err := a.Finalize(ctx, 0, item, request, result)
	if err != nil {
		if finalized == nil {
			finalized = result
		}
		gap(finalized, "post-merge finalize", err)
	}
	return finalized, nil
}
