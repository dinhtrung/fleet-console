package decide

import (
	"strings"
	"testing"
	"time"
)

// readyItems is n open tasks, the shape every capacity test needs.
func readyItems(n int) []Item {
	items := make([]Item, 0, n)
	for number := 101; number < 101+n; number++ {
		items = append(items, Item{Number: number, Kind: KindTask, State: "open", Title: "ready work"})
	}
	return items
}

// TestUnreadRigDoesNotTakeTheOtherRigsCapacity is #438's first half: rig-b did not
// answer, rig-a did, and the item dispatches to rig-a's seat. Capacity is the sum
// over the rigs that answered — one, not zero.
func TestUnreadRigDoesNotTakeTheOtherRigsCapacity(t *testing.T) {
	snapshot := Snapshot{
		Project:   Project{Key: "app", Forge: ForgeGitHub, Enabled: true, ConcurrencyCap: 2},
		ForgeOK:   true,
		Items:     readyItems(1),
		Builders:  []string{"build@rig-a"},
		RigUnread: []string{"rig-b"},
		RigError:  "rig rig-b: rig ps --nodes --full --json --rig rig-b: exit 1",
	}
	result := Decide(snapshot)
	if len(result.Dispatches) != 1 || result.Dispatches[0].Number != 101 || result.Dispatches[0].Seat != "build@rig-a" {
		t.Fatalf("dispatches = %+v, want #101 on rig-a's seat", result.Dispatches)
	}
	if result.Free != 1 {
		t.Fatalf("free = %d, want 1: only the readable rig contributes capacity", result.Free)
	}
	if result.Status != StatusPlanned {
		t.Fatalf("status = %q, want planned", result.Status)
	}
}

// TestUnreadRigIsNamedWhenNoSeatIsFree is #438's second half: nothing free to
// dispatch to, and the line the operator reads says which rig went blind instead
// of implying the whole fleet is busy.
func TestUnreadRigIsNamedWhenNoSeatIsFree(t *testing.T) {
	snapshot := Snapshot{
		Project:   Project{Key: "app", Forge: ForgeGitHub, Enabled: true, ConcurrencyCap: 2},
		ForgeOK:   true,
		Items:     readyItems(1),
		RigUnread: []string{"rig-b"},
		RigError:  "rig rig-b: rig ps --nodes --full --json --rig rig-b: exit 1",
	}
	result := Decide(snapshot)
	if len(result.Dispatches) != 0 {
		t.Fatalf("dispatches = %+v, want none", result.Dispatches)
	}
	if len(result.Skips) != 1 || !strings.Contains(result.Skips[0].Detail, "rig rig-b unread") {
		t.Fatalf("skips = %+v, want one detail naming rig-b as unread", result.Skips)
	}
}

// TestEveryRigUnreadDecidesNothing: with no rig answering, capacity is unknown,
// and unknown is not empty — the plan reports stale and dispatches nothing
// rather than claiming no builder could take the work.
func TestEveryRigUnreadDecidesNothing(t *testing.T) {
	snapshot := Snapshot{
		Project:    Project{Key: "app", Forge: ForgeGitHub, Enabled: true, ConcurrencyCap: 2},
		ForgeOK:    true,
		Items:      readyItems(2),
		RigUnread:  []string{"rig-a", "rig-b"},
		RigUnknown: true,
		RigError:   "rig rig-a: no daemon; rig rig-b: no daemon",
	}
	plan := PlanTick(snapshot, time.Now())
	if plan.Status != StatusStale {
		t.Fatalf("status = %q, want stale: an unread fleet is not an empty one", plan.Status)
	}
	if len(plan.Intents) != 0 || len(plan.Skips) != 0 {
		t.Fatalf("intents = %d skips = %d, want neither", len(plan.Intents), len(plan.Skips))
	}
	if !strings.Contains(plan.Error, "rig rig-a") {
		t.Fatalf("plan error = %q, want the rig failure", plan.Error)
	}
}

// TestNoFreeBuilderDetailAddsTheUnreadRigOnlyWhenItExplainsTheZero: while a free
// seat remains the skip is the cap's doing, and a blind rig did not cause it.
func TestNoFreeBuilderDetailAddsTheUnreadRigOnlyWhenItExplainsTheZero(t *testing.T) {
	refusal := tierRefusal{}
	looked := noFreeBuilderDetail(refusal, nil, nil, []string{"rig-b"}, true)
	if !strings.HasSuffix(looked, "; rig rig-b unread") {
		t.Fatalf("detail = %q, want the unread rig named", looked)
	}
	capBound := noFreeBuilderDetail(refusal, nil, nil, []string{"rig-b"}, false)
	if strings.Contains(capBound, "unread") {
		t.Fatalf("cap-bound detail = %q, want no unread suffix", capBound)
	}
	answered := noFreeBuilderDetail(refusal, nil, nil, nil, true)
	if answered != "every admitted seat is taken" {
		t.Fatalf("detail = %q, want the bare form", answered)
	}
	if plural := noFreeBuilderDetail(refusal, nil, nil, []string{"rig-a", "rig-b"}, true); !strings.HasSuffix(plural, "; rigs rig-a, rig-b unread") {
		t.Fatalf("detail = %q, want both named", plural)
	}
}
