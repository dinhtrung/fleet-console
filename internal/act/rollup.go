package act

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dinhtrung/fleet-console/internal/forge"
)

// DecisionKind is what the epic rollup wants the tick to do.
type DecisionKind string

const (
	// RollupClose means every sub-issue of the parent epic is closed: close it.
	RollupClose DecisionKind = "close"
	// RollupSkip means the parent is not to be closed, for the reason carried alongside.
	RollupSkip DecisionKind = "skip"
)

// Decision is the verdict on one merged item's parent epic.
//
// Children is the number of items whose Parent is the epic, counted whether they
// are open or closed — so "the snapshot held no children" can never be mistaken
// for "every child is closed". Open lists the ones still open, in number order.
//
// Quiet marks a skip that is the ordinary state of an unfinished epic rather than
// a considered decline: the sweep meets every open epic on every tick, so a skip
// line for one still gathering children would fill the journal with the reason
// nothing happened. Rollup leaves it false — a merge that cannot close its parent
// is news — and Sweep sets it for the cases it meets routinely.
type Decision struct {
	Kind     DecisionKind
	Epic     int
	Children int
	Open     []int
	Reason   string // the skip reason, printed verbatim in the tick's skip list
	Quiet    bool   // skip without a skip line in the journal
}

// ShouldClose reports whether the caller must close the epic.
func (d Decision) ShouldClose() bool { return d.Kind == RollupClose }

// Rollup decides whether a merged item completes its parent epic. It is pure: no
// forge, no store, no clock, so the same snapshot always gives the same verdict.
//
// The error return is part of the signature the sibling steps code against; no
// input can fail today, and an unexplained nil there is better than a caller that
// has to be rewritten when one can. `parent` is the merged item's parent
// pointer and `items` is the snapshot the tick already read.
//
// The order of the checks is the order the reasons are read in: a missing parent
// is not an epic, an epic that is already closed needs nothing, and only then is
// the children question asked. Every skip names its cause, because the tick
// prints the reason and an unexplained skip is indistinguishable from a bug.
func Rollup(parent *int, items []forge.Item) (Decision, error) {
	if parent == nil {
		return Decision{Kind: RollupSkip, Reason: "no parent"}, nil
	}
	epicNumber := *parent
	var epic *forge.Item
	byNumber := make(map[int]forge.Item, len(items))
	for index := range items {
		byNumber[items[index].Number] = items[index]
		if items[index].Number == epicNumber {
			epic = &items[index]
		}
	}
	if epic == nil || epic.Kind != forge.KindEpic {
		return Decision{Kind: RollupSkip, Epic: epicNumber, Reason: "parent not an epic"}, nil
	}
	if !strings.EqualFold(epic.State, forge.StateOpen) {
		return Decision{Kind: RollupSkip, Epic: epicNumber, Reason: "epic already closed"}, nil
	}

	var open []int
	children := 0
	for _, item := range items {
		if item.Parent == nil || *item.Parent != epicNumber {
			continue
		}
		children++
		if strings.EqualFold(item.State, forge.StateOpen) {
			open = append(open, item.Number)
		}
	}
	sort.Ints(open)

	if children == 0 {
		// An empty read is indistinguishable from "all closed", and closing an
		// epic on a short snapshot would close it over work nobody looked at.
		return Decision{Kind: RollupSkip, Epic: epicNumber, Reason: "no sub-issues in the snapshot"}, nil
	}
	if len(open) > 0 {
		return Decision{
			Kind: RollupSkip, Epic: epicNumber, Children: children, Open: open,
			Reason: fmt.Sprintf("%s still open (%s)", plural(len(open)), issueList(open)),
		}, nil
	}
	return Decision{Kind: RollupClose, Epic: epicNumber, Children: children}, nil
}

// plural is "1 sub-issue" or "N sub-issues" — the reason reads as a sentence in
// the tick's output, and "1 sub-issues" is the kind of typo that makes an
// operator distrust the number.
func plural(n int) string {
	if n == 1 {
		return "1 sub-issue"
	}
	return fmt.Sprintf("%d sub-issues", n)
}

// issueList renders numbers as `#a, #b`, which is the form the spec's example
// reason uses.
func issueList(numbers []int) string {
	parts := make([]string, 0, len(numbers))
	for _, number := range numbers {
		parts = append(parts, fmt.Sprintf("#%d", number))
	}
	return strings.Join(parts, ", ")
}
