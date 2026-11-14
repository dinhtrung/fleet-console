package decide

import (
	"testing"
)

func liveRowFor(item int) Row {
	return Row{
		QItemID: "q-" + string(rune('0'+item)), State: "pending", Destination: "builder@rig-a",
		Body: "work on #" + string(rune('0'+item)),
	}
}

func branchFor(item int, open, merged bool) Branch {
	return Branch{Name: "feature/" + string(rune('0'+item)) + "-x", Item: item, HasOpenCR: open, HasMergedCR: merged}
}

func dispatchesOf(result Result) []int {
	out := make([]int, len(result.Dispatches))
	for index, dispatch := range result.Dispatches {
		out[index] = dispatch.Number
	}
	return out
}

func skipFor(result Result, item int) Skip {
	for _, skip := range result.Skips {
		if skip.Number == item {
			return skip
		}
	}
	return Skip{}
}

// TestEpicSerialOneChildInFlight (#443, design D3): two ready children and two
// free seats dispatch the lowest number; the sibling is skipped naming the
// child in flight, not guessed at by priority.
func TestEpicSerialOneChildInFlight(t *testing.T) {
	result := Decide(base(epic(1), child(task(5), 1), child(task(6), 1)))
	if got := dispatchesOf(result); len(got) != 1 || got[0] != 5 {
		t.Fatalf("dispatches = %v, want only the lowest sibling", got)
	}
	skip := skipFor(result, 6)
	if skip.Reason != ReasonEpicBusy || skip.Detail != "#5 in flight" {
		t.Fatalf("skip = %+v, want epic_busy naming #5 in flight", skip)
	}
}

// TestEpicSerialAdvancesOnMerge (#443): the epic advances when the previous
// child lands — the merged branch holds nothing and the next sibling dispatches.
func TestEpicSerialAdvancesOnMerge(t *testing.T) {
	s := base(epic(1), child(task(5), 1), child(task(6), 1))
	s.Branches = []Branch{branchFor(5, false, true)}
	result := Decide(s)
	if got := dispatchesOf(result); len(got) != 1 || got[0] != 6 {
		t.Fatalf("dispatches = %v, want the next sibling after the merge", got)
	}
}

// TestEpicSerialHoldsOnOpenCR (#443, design D1): a child whose dispatch ended but
// whose change request is open still holds the epic, and the skip says review.
func TestEpicSerialHoldsOnOpenCR(t *testing.T) {
	s := base(epic(1), child(task(5), 1), child(task(6), 1))
	s.Branches = []Branch{branchFor(5, true, false)}
	result := Decide(s)
	if got := dispatchesOf(result); len(got) != 0 {
		t.Fatalf("dispatches = %v, want none while #5 is in review", got)
	}
	skip := skipFor(result, 6)
	if skip.Reason != ReasonEpicBusy || skip.Detail != "#5 in review" {
		t.Fatalf("skip = %+v, want epic_busy naming #5 in review", skip)
	}
}

// TestEpicSerialIgnoresOnHoldSibling (#443, design D6): a sibling on hold is not
// in flight and holds nothing for anybody.
func TestEpicSerialIgnoresOnHoldSibling(t *testing.T) {
	s := base(epic(1), child(task(5, "onhold"), 1), child(task(6), 1))
	s.Rows = []Row{liveRowFor(5)}
	result := Decide(s)
	if got := dispatchesOf(result); len(got) != 1 || got[0] != 6 {
		t.Fatalf("dispatches = %v, want the free sibling", got)
	}
	if skip := skipFor(result, 5); skip.Reason != ReasonOnHold {
		t.Fatalf("skip = %+v, want the existing onhold rule to own it", skip)
	}
}

// TestEpicSerialOffDispatchesBoth (#443, design D5): the project's opt-out
// restores parallel siblings.
func TestEpicSerialOffDispatchesBoth(t *testing.T) {
	s := base(epic(1), child(task(5), 1), child(task(6), 1))
	s.Project.EpicParallel = true
	if got := dispatchesOf(Decide(s)); len(got) != 2 {
		t.Fatalf("dispatches = %v, want both siblings with the opt-out", got)
	}
}

// TestEpicSerialLeavesUnparentedItemsAlone (#443, design D6): items without an
// epic parent are unaffected and still run together.
func TestEpicSerialLeavesUnparentedItemsAlone(t *testing.T) {
	if got := dispatchesOf(Decide(base(task(2), task(3)))); len(got) != 2 {
		t.Fatalf("dispatches = %v, want both parentless items", got)
	}
}
