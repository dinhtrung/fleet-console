package act

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/dinhtrung/fleet-console/internal/forge"
)

// EpicCompletion is one epic this tick delivered: the change request it opened
// (or found already open), and any notice about the branch's drift.
type EpicCompletion struct {
	Epic          int
	ChangeRequest int
	Branch        string
	Notice        string
}

// openEpicDelivery finds the epic's own open change request, if any: a live
// change request whose head branch is epic/<n>- (#484, design D2/D5). Reading by
// the item number the branch names means a retitled epic's delivery is still
// found, and `branchItem` already accepts both feature/<n>- and epic/<n>-.
func openEpicDelivery(requests []forge.ChangeRequest, epic int) *EpicDelivery {
	for index := range requests {
		request := requests[index]
		if request.Merged || strings.EqualFold(request.State, forge.StateClosed) {
			continue
		}
		if number, ok := epicBranchNumber(request.Branch); ok && number == epic {
			return &EpicDelivery{Branch: request.Branch, Number: request.Number}
		}
	}
	return nil
}

// epicOpenRequest is the change request this tick would open for the epic: an
// open request whose head is the epic's branch and whose base is the default
// branch (#533). A request on another base is not the delivery — it merges
// elsewhere — so it does not suppress a new one, and a merged or closed request
// does not either. The lookup is the guard completeEpic runs before it opens,
// because the tick's snapshot is `state=open` only and a delivery that exists
// but is absent from it is what made every tick try again.
func epicOpenRequest(requests []forge.ChangeRequest, branch, base string) *forge.ChangeRequest {
	for index := range requests {
		request := requests[index]
		if request.Merged || strings.EqualFold(request.State, forge.StateClosed) {
			continue
		}
		if request.Branch == branch && request.Base == base {
			return &request
		}
	}
	return nil
}

// changeRequestAlreadyExists reports whether an OpenChangeRequest refusal is the
// forge saying the request is already there. A stale tick snapshot can miss a
// delivery another writer opened, and both forges name the refusal the same way
// ("Another open merge request already exists…"); the caller then reads the
// delivery back rather than reporting the tick as failed (#533).
func changeRequestAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "already exists")
}

// epicBranchByPrefix resolves the epic's branch by the epic/<n>- prefix after a
// fetch (#484, design D2: "resolved by prefix"). Re-deriving the name from the
// current title would wedge delivery the moment the epic is retitled between
// dispatches; the prefix finds the branch that already exists. No match means
// there is nothing to deliver; several matches are named rather than guessed.
func (a *Actions) epicBranchByPrefix(ctx context.Context, epic int) (branch string, found bool, err error) {
	matches, err := a.Git.BranchesMatching(ctx, fmt.Sprintf("refs/remotes/origin/epic/%d-*", epic))
	if err != nil {
		return "", false, fmt.Errorf("list epic branches: %w", err)
	}
	var names []string
	for _, match := range matches {
		name := strings.TrimPrefix(match, "origin/")
		if number, ok := epicBranchNumber(name); ok && number == epic {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	switch len(names) {
	case 0:
		return "", false, nil
	case 1:
		return names[0], true, nil
	default:
		return "", false, fmt.Errorf("epic #%d has more than one branch (%s); refusing to guess which to deliver",
			epic, strings.Join(names, ", "))
	}
}

// childMerged reports whether a child closed with a merged change request. The
// change-request snapshot carries merged requests too, so a child whose work
// landed through a merged change request is "Merged"; anything else (closed by
// hand, closed without merging) is "Not merged".
func childMerged(child int, requests []forge.ChangeRequest) bool {
	for index := range requests {
		request := requests[index]
		if !request.Merged {
			continue
		}
		if request.ClosesItem == child || forge.ParseCloses(request.Body) == child {
			return true
		}
	}
	return false
}

// epicBody is the delivery's body: every child under Merged or Not merged, and
// `Closes #<epic>` so the merge gate and the forge close the epic on the
// operator's merge (design D5).
func epicBody(epic int, children []forge.Item, requests []forge.ChangeRequest) string {
	sorted := append([]forge.Item(nil), children...)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a].Number < sorted[b].Number })
	var merged, notMerged []string
	for _, child := range sorted {
		line := fmt.Sprintf("- #%d %s", child.Number, child.Title)
		if childMerged(child.Number, requests) {
			merged = append(merged, line)
		} else {
			notMerged = append(notMerged, line)
		}
	}
	var body strings.Builder
	body.WriteString("## Children\n\n")
	if len(merged) > 0 {
		body.WriteString("### Merged\n" + strings.Join(merged, "\n") + "\n\n")
	}
	if len(notMerged) > 0 {
		body.WriteString("### Not merged\n" + strings.Join(notMerged, "\n") + "\n\n")
	}
	fmt.Fprintf(&body, "Closes #%d\n", epic)
	return body.String()
}

// completeEpic delivers a finished epic on its own branch (#484, design D5): when
// every child is closed and the epic branch carries work the default does not, it
// opens exactly one non-draft change request epic/<n>- -> default. The operator
// merges it; the app never does.
//
// `children` is the sweep's own open-and-closed read when it has one; the rollup
// path passes nil and the read is spent here, because the tick's snapshot is
// state=open only and cannot list the closed children the body must name.
//
// `completed` carries the epics this tick already delivered, so a merge and a
// sweep over the same epic open one change request, not two.
//
// A delivery that is already open is reused rather than reopened (#533): the
// tick's snapshot is `state=open` only, so an epic whose request the mirror did
// not carry — or whose request another writer opened a moment ago — would
// otherwise be offered to the forge every tick until it merged. Reuse is the
// same outcome with no write: the epic is marked completed for this tick, the
// existing request is the drift step's handle and the completion line's number,
// and nothing is reported as skipped.
func (a *Actions) completeEpic(ctx context.Context, in TickInput, report *TickReport, completed map[int]bool, epic int, children []forge.Item) {
	if completed[epic] {
		return
	}
	epicItem := itemFor(in.Items, epic)
	// The epic::direct escape opts the epic out entirely (design D1): its children
	// landed on the default branch, so there is no branch delivery to open and the
	// caller closes the epic the ordinary way.
	if epicDirected(epicItem) {
		return
	}
	title := ""
	if epicItem != nil {
		title = epicItem.Title
	}

	skip := func(format string, args ...any) {
		report.Skipped = append(report.Skipped, fmt.Sprintf("#%d completion: ", epic)+fmt.Sprintf(format, args...))
	}

	if err := a.Git.Fetch(ctx); err != nil {
		skip("fetch: %v", err)
		return
	}
	branch, found, err := a.epicBranchByPrefix(ctx, epic)
	if err != nil {
		skip("%v", err)
		return
	}
	if !found {
		return // no epic branch: nothing has been built on one
	}
	defaultSHA, err := a.Git.BaseSHA(ctx)
	if err != nil {
		skip("read %s: %v", a.defaultBranchName(), err)
		return
	}
	epicSHA, err := a.Git.RefSHA(ctx, "origin/"+branch)
	if err != nil {
		skip("epic branch %s: %v", branch, err)
		return
	}
	if epicSHA == defaultSHA {
		return // the branch carries nothing yet; there is no delivery to open
	}
	// Ahead means the epic branch has a commit the default does not: the default
	// is not an ancestor of the branch. Otherwise there is no work to deliver.
	if defaultIsAncestor, err := a.Git.IsAncestor(ctx, epicSHA, defaultSHA); err != nil {
		skip("compare %s with %s: %v", branch, a.defaultBranchName(), err)
		return
	} else if defaultIsAncestor {
		return
	}

	if children == nil {
		if children, err = a.Forge.SubIssues(ctx, epic); err != nil {
			skip("read sub-issues of #%d: %v", epic, err)
			return
		}
	}
	// The delivery may already be open (#533): the snapshot is `state=open` only,
	// so a request the mirror did not carry this tick is looked for before the
	// write, not after it. Reusing it is today's path when nothing is open.
	request := epicOpenRequest(in.CRs, branch, a.defaultBranchName())
	if request == nil {
		opened, err := a.Forge.OpenChangeRequest(ctx, forge.OpenChangeRequestInput{
			Head: branch, Base: a.defaultBranchName(),
			Title: fmt.Sprintf("Epic #%d: %s", epic, title),
			Body:  epicBody(epic, children, in.CRs), Draft: false,
		})
		if err != nil && !changeRequestAlreadyExists(err) {
			skip("open change request on %s: %v", branch, err)
			return
		}
		if err != nil {
			// The forge refused with "already exists": the request is there, this
			// tick just did not see it. Read it back — its number is the handle the
			// drift step and the completion line need.
			requests, readErr := a.Forge.ChangeRequests(ctx)
			if readErr != nil {
				skip("open change request on %s: %v (already exists; read back: %v)", branch, err, readErr)
				return
			}
			request = epicOpenRequest(requests, branch, a.defaultBranchName())
			if request == nil {
				skip("open change request on %s: %v (already exists, but the request is not in the snapshot)", branch, err)
				return
			}
		} else {
			request = &opened
		}
	}
	completed[epic] = true

	// Drift is reported, never forced (design D7): when the default has commits the
	// epic branch lacks, ask the forge to update the branch. The forge's update is
	// per change request, so it runs after the request exists — whether this tick
	// opened it or found it already open — with the request as its handle. The
	// outcome D7 asks for (the request stays open, the conflict is named) is
	// unchanged, and the app never force-pushes or merges.
	notice := ""
	if behind, err := a.Git.IsAncestor(ctx, defaultSHA, epicSHA); err != nil {
		notice = fmt.Sprintf("cannot compare %s with %s: %v", branch, a.defaultBranchName(), err)
		skip("%s", notice)
	} else if !behind {
		if err := a.Forge.UpdateBranch(ctx, request.Number); err != nil {
			notice = fmt.Sprintf("epic branch is behind and cannot be updated: %v", err)
			skip("%s", notice)
		}
	}

	report.Completions = append(report.Completions, EpicCompletion{
		Epic: epic, ChangeRequest: request.Number, Branch: branch, Notice: notice,
	})
	if _, err := a.event(ctx, "act", "epic.completion", fmt.Sprintf("#%d", epic),
		fmt.Sprintf("change request #%d on %s", request.Number, branch)); err != nil {
		skip("event: %v", err)
	}
}
