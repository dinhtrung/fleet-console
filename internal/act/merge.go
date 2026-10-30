package act

import (
	"context"
	"fmt"
	"sort"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"strings"
)

// MergeResult is the evidence of one watchdog merge.
type MergeResult struct {
	Merged        bool
	ChangeRequest int
	SHA           string
	ClosedItem    bool
	ClosedByApp   bool
	MarkerCleared bool
	NextUnblocked int
	Events        []int64
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
		return nil, fmt.Errorf("refusing merge: change request #%d is not mergeable", request.Number)
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
		if eventID, err := a.event(ctx, "act", "change_request.ready", fmt.Sprintf("#%d", item),
			fmt.Sprintf("change request #%d taken out of draft before merge", request.Number)); err == nil {
			_ = eventID
		}
	}

	if err := a.Forge.Merge(ctx, request.Number, verdict.SHA); err != nil {
		return nil, err
	}
	result := &MergeResult{Merged: true, ChangeRequest: request.Number, SHA: verdict.SHA}
	eventID, err := a.event(ctx, "act", "merge", fmt.Sprintf("#%d", item),
		fmt.Sprintf("change request #%d at %s", request.Number, verdict.SHA))
	if err != nil {
		return nil, err
	}
	result.Events = append(result.Events, eventID)
	if dispatch.EndedAt != nil {
		// The builder ended its row when it delivered; the merge still has to land and
		// the item still has to close, so the row is bookkeeping, not the trigger.
		if eventID, err := a.event(ctx, "act", "dispatch.merged", fmt.Sprintf("#%d", item), "merge landed after the row ended: "+dispatch.EndReason); err == nil {
			result.Events = append(result.Events, eventID)
		}
	} else if err := a.Store.EndDispatch(ctx, dispatchID, "merged"); err != nil {
		return nil, err
	}
	if eventID, err := a.event(ctx, "act", "dispatch.merged", fmt.Sprintf("#%d", item), "dispatch ended merged"); err != nil {
		return nil, err
	} else {
		result.Events = append(result.Events, eventID)
	}
	finalized, err := a.Finalize(ctx, dispatchID, item, *request, result)
	if err != nil {
		return finalized, err
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
			return result, err
		} else {
			result.Events = append(result.Events, eventID)
		}
	} else if current != nil {
		result.ClosedItem = true
	}
	if err := a.Forge.SetMarker(ctx, item, false); err != nil {
		if eventID, eventErr := a.event(ctx, "act", "marker.clear.failed", fmt.Sprintf("#%d", item), err.Error()); eventErr == nil {
			result.Events = append(result.Events, eventID)
		}
	} else {
		result.MarkerCleared = true
		if eventID, err := a.event(ctx, "act", "marker.clear", fmt.Sprintf("#%d", item), "in-flight marker cleared"); err != nil {
			return result, err
		} else {
			result.Events = append(result.Events, eventID)
		}
	}
	if next > 0 {
		result.NextUnblocked = next
		if eventID, err := a.event(ctx, "act", "item.unblocked", fmt.Sprintf("#%d", next),
			fmt.Sprintf("waits only on #%d", item)); err != nil {
			return result, err
		} else {
			result.Events = append(result.Events, eventID)
		}
	}
	sort.Slice(result.Events, func(i, j int) bool { return result.Events[i] < result.Events[j] })
	return result, nil
}
