package decide

import (
	"fmt"
	"testing"
	"time"
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

// --- The scenarios that had no test until #467 -----------------------------
//
// The four below are the spec's remaining sentences. Each asserts through
// Decide on a snapshot, so the rule is exercised where it actually runs rather
// than by calling a helper.

// liveRowAged is a builder's row for one item, as old as the caller says.
//
// The age matters for the silent-seat scenario: the row is still live, and
// nothing in Decide reads how long it has been alive. A "silent" seat is a row
// nobody is working any more — not a row Decide may treat as ended.
func liveRowAged(item int, created time.Time) Row {
	return Row{
		QItemID: fmt.Sprintf("q-%d", item), State: "pending", Destination: "builder@rig-a",
		Body: fmt.Sprintf("work on #%d", item), CreatedAt: created, LastNudgeAt: created,
	}
}

// endedRowFor is a dispatch that ended with no change request: the queue says
// the row is terminal, and no branch or CR exists for the item.
func endedRowFor(item int) Row {
	return Row{
		QItemID: fmt.Sprintf("q-%d", item), State: "done", Pickup: "terminal",
		Destination: "builder@rig-a", Body: fmt.Sprintf("work on #%d", item),
	}
}

// TestEpicSerialHoldsOnSilentSeat (#467, task 3.7): a live dispatch whose seat
// went silent for hours keeps the epic held.
//
// The alternative — treating a quiet row as ended — is what lets two builders
// into one epic, which is the bug this rule closed. Decide cannot see a seat's
// silence at all: the row is live, and the silent-dispatch question that
// eventually pages a human is raised by the act side from the same row. What
// this pins is the decide half: no second builder, and the epic_busy skip names
// the silent holder as in flight.
func TestEpicSerialHoldsOnSilentSeat(t *testing.T) {
	s := base(epic(1), child(task(5), 1), child(task(6), 1))
	s.Rows = []Row{liveRowAged(5, time.Now().Add(-6*time.Hour))}

	result := Decide(s)
	if got := dispatchesOf(result); len(got) != 0 {
		t.Fatalf("dispatches = %v, want none: a live row is not work to start", got)
	}
	if skip := skipFor(result, 6); skip.Reason != ReasonEpicBusy || skip.Detail != "#5 in flight" {
		t.Fatalf("skip = %+v, want epic_busy naming the silent holder in flight", skip)
	}
	// The holder is not re-dispatched either: its row is live, which is the
	// existing live_row rule's fact, not a second dispatch opportunity.
	if skip := skipFor(result, 5); skip.Reason != ReasonLiveRow {
		t.Fatalf("skip = %+v, want live_row for the silent holder", skip)
	}
}

// TestEpicSerialHoldsOnDraftCR (#467, task 3.8): an open unmerged change
// request with no live dispatch still holds the epic, and the skip says the
// holder is in review.
//
// Decide cannot tell a draft from a ready change request: the snapshot's Branch
// carries HasOpenCR/HasMergedCR and no draft flag at all, and the mirror sets
// HasOpenCR for any open, unmerged change request (a draft included). So this
// pins Decide's half of "a draft still holds", and the mirror's projection of a
// draft into HasOpenCR is pinned by
// internal/mirror's TestBuildSnapshotOpenCRIncludesADraft.
func TestEpicSerialHoldsOnDraftCR(t *testing.T) {
	s := base(epic(1), child(task(5), 1), child(task(6), 1))
	s.Branches = []Branch{{Name: "feature/5-x", Item: 5, HasOpenCR: true}}

	result := Decide(s)
	if got := dispatchesOf(result); len(got) != 0 {
		t.Fatalf("dispatches = %v, want none while the holder's change request is open", got)
	}
	if skip := skipFor(result, 6); skip.Reason != ReasonEpicBusy || skip.Detail != "#5 in review" {
		t.Fatalf("skip = %+v, want epic_busy naming the holder as in review", skip)
	}
}

// TestEpicSerialGrandfathersTwoInFlight (#467, task 3.9): two children already
// dispatched stay dispatched and no third child starts.
//
// The rule landed after those dispatches existed. It must never cancel one — no
// path in Decide writes to a dispatch — and the epic stays held until both land.
func TestEpicSerialGrandfathersTwoInFlight(t *testing.T) {
	s := base(epic(1), child(task(5), 1), child(task(6), 1), child(task(7), 1))
	s.Rows = []Row{liveRowAged(5, time.Now().Add(-2*time.Hour)), liveRowAged(6, time.Now().Add(-time.Hour))}

	result := Decide(s)
	if got := dispatchesOf(result); len(got) != 0 {
		t.Fatalf("dispatches = %v, want no third child while two are live", got)
	}
	for _, number := range []int{5, 6} {
		if skip := skipFor(result, number); skip.Reason != ReasonLiveRow {
			t.Fatalf("#%d skip = %+v, want live_row: an existing dispatch is not cancelled or re-planned", number, skip)
		}
	}
	if skip := skipFor(result, 7); skip.Reason != ReasonEpicBusy {
		t.Fatalf("skip = %+v, want the third child held with epic_busy", skip)
	}
}

// TestEpicSerialFreesOnADispatchThatEndedWithoutACR (#467, task 3.10): the
// spec's "A dispatch that ends without work" scenario, which had neither a test
// nor a task.
//
// This is the branch that would deadlock an epic behind a failed build. The
// ended dispatch belongs to the HIGHER-numbered child on purpose: if the row
// still counted as a holder, #6 would block #5 and nothing would dispatch, so
// the lowest child going out is the observable difference between "freed" and
// "still held". With the pair, the ending row is the only thing that changed.
func TestEpicSerialFreesOnADispatchThatEndedWithoutACR(t *testing.T) {
	held := base(epic(1), child(task(5), 1), child(task(6), 1))
	held.Rows = []Row{liveRowAged(6, time.Now().Add(-time.Hour))}
	if got := dispatchesOf(Decide(held)); len(got) != 0 {
		t.Fatalf("live row: dispatches = %v, want none while #6 still holds the epic", got)
	}
	if skip := skipFor(Decide(held), 5); skip.Reason != ReasonEpicBusy {
		t.Fatalf("skip = %+v, want #5 held behind the live holder", skip)
	}

	freed := base(epic(1), child(task(5), 1), child(task(6), 1))
	freed.Rows = []Row{endedRowFor(6)}
	result := Decide(freed)
	if got := dispatchesOf(result); len(got) != 1 || got[0] != 5 {
		t.Fatalf("ended row: dispatches = %v, want the lowest child once the epic is free", got)
	}
	if skip := skipFor(result, 6); skip.Reason != ReasonEpicBusy || skip.Detail != "#5 in flight" {
		t.Fatalf("skip = %+v, want the newly dispatched sibling named as the holder", skip)
	}
}
