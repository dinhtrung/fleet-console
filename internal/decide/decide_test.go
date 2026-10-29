package decide

import (
	"testing"
	"time"
)

func base(items ...Item) Snapshot {
	return Snapshot{
		Project:  Project{Key: "test", Forge: ForgeGitHub, Enabled: true},
		Items:    items,
		Builders: []string{"seat-1", "seat-2", "seat-3"},
		ForgeOK:  true,
	}
}

func task(number int, labels ...string) Item {
	return Item{Number: number, Kind: KindTask, State: StateOpen, Labels: labels}
}

func epic(number int, labels ...string) Item {
	return Item{Number: number, Kind: KindEpic, State: StateOpen, Labels: labels}
}

func child(item Item, parent int) Item {
	item.Parent = &parent
	return item
}

type ruleCase struct {
	name       string
	snapshot   Snapshot
	dispatches []int
	reasons    map[int]Reason
}

func runCases(t *testing.T, cases []ruleCase) {
	t.Helper()
	for _, test := range cases {
		test := test
		t.Run(test.name, func(t *testing.T) {
			result := Decide(test.snapshot)
			got := make([]int, len(result.Dispatches))
			for index, dispatch := range result.Dispatches {
				got[index] = dispatch.Number
			}
			if len(got) != len(test.dispatches) {
				t.Fatalf("dispatches = %v, want %v", got, test.dispatches)
			}
			for index := range test.dispatches {
				if got[index] != test.dispatches[index] {
					t.Fatalf("dispatches = %v, want %v", got, test.dispatches)
				}
			}
			reasons := make(map[int]Reason, len(result.Skips))
			for _, skip := range result.Skips {
				reasons[skip.Number] = skip.Reason
			}
			for number, want := range test.reasons {
				if got := reasons[number]; got != want {
					t.Fatalf("#%d reason = %q, want %q", number, got, want)
				}
			}
		})
	}
}

// TestRuleEpicSkipped: an epic is never a candidate; its children are.
func TestRuleEpicSkipped(t *testing.T) {
	runCases(t, []ruleCase{{
		name:       "epic is skipped, children run",
		snapshot:   base(epic(1), child(task(2), 1), child(task(3), 1)),
		dispatches: []int{2, 3},
		reasons:    map[int]Reason{1: ReasonEpic},
	}})
}

// TestRuleParentGate: only a child of an open epic is a candidate.
func TestRuleParentGate(t *testing.T) {
	closed := epic(9)
	closed.State = StateClosed
	runCases(t, []ruleCase{
		{
			name:       "child of an open epic runs",
			snapshot:   base(epic(1), closed, child(task(2), 1)),
			dispatches: []int{2},
		},
		{
			name:       "child of a closed epic does not run",
			snapshot:   base(closed, child(task(3), 9)),
			dispatches: nil,
			reasons:    map[int]Reason{3: ReasonParentNotOpenEpic},
		},
		{
			name:       "child of a missing parent does not run",
			snapshot:   base(child(task(4), 99)),
			dispatches: nil,
			reasons:    map[int]Reason{4: ReasonParentNotOpenEpic},
		},
	})
}

// TestRuleOnHoldCascade: onhold on a task or on its parent epic skips it.
func TestRuleOnHoldCascade(t *testing.T) {
	runCases(t, []ruleCase{
		{
			name:       "task carries onhold",
			snapshot:   base(task(5, "onhold"), task(6)),
			dispatches: []int{6},
			reasons:    map[int]Reason{5: ReasonOnHold},
		},
		{
			name:       "parent epic carries onhold",
			snapshot:   base(epic(1, "onhold"), epic(2), child(task(3), 1), child(task(4), 2)),
			dispatches: []int{4},
			reasons:    map[int]Reason{3: ReasonParentOnHold},
		},
	})
}

// TestRuleLiveRowIdempotency: a live row whose body or summary names the item blocks a dispatch.
func TestRuleLiveRowIdempotency(t *testing.T) {
	bodyRow := base(task(2), task(3))
	bodyRow.Rows = []Row{{QItemID: "qitem-body", State: "pending", Destination: "seat-1", Body: "Take #2."}}
	summaryRow := base(task(2), task(3))
	summaryRow.Rows = []Row{{QItemID: "qitem-summary", State: "in-progress", Destination: "seat-1", Summary: "Ticket #2"}}
	runCases(t, []ruleCase{
		{name: "body names the item", snapshot: bodyRow, dispatches: []int{3}, reasons: map[int]Reason{2: ReasonLiveRow}},
		{name: "summary names the item", snapshot: summaryRow, dispatches: []int{3}, reasons: map[int]Reason{2: ReasonLiveRow}},
	})
}

// TestRuleBranchOnOrigin: a branch with an open or merged CR blocks; a branch with no CR
// is unfinished work and is adopted.
func TestRuleBranchOnOrigin(t *testing.T) {
	withBranches := base(task(2), task(3), task(4), task(5))
	withBranches.Branches = []Branch{
		{Name: "feature/2-open", Item: 2, HasOpenCR: true},
		{Name: "feature/3-merged", Item: 3, HasMergedCR: true},
		{Name: "feature/4-started", Item: 4},
	}
	runCases(t, []ruleCase{
		{
			name:       "open, merged and started branches",
			snapshot:   withBranches,
			dispatches: []int{4, 5},
			reasons:    map[int]Reason{2: ReasonBranchOpenCR, 3: ReasonBranchMergedCR},
		},
	})
}

// TestRuleOneWayBlockingGitLab: relates_to is symmetric, so only a smaller open number blocks.
func TestRuleOneWayBlockingGitLab(t *testing.T) {
	snapshot := base(
		Item{Number: 110, Kind: KindTask, State: StateOpen, Related: []Link{{Number: 120, State: StateOpen}}},
		Item{Number: 120, Kind: KindTask, State: StateOpen, Related: []Link{{Number: 110, State: StateOpen}}},
	)
	snapshot.Project.Forge = ForgeGitLab
	runCases(t, []ruleCase{{
		name:       "the larger number is blocked",
		snapshot:   snapshot,
		dispatches: []int{110},
		reasons:    map[int]Reason{120: ReasonBlocked},
	}})
}

// TestRuleOneWayBlockingGitHub: native blocked_by is directional, whatever the numbers.
func TestRuleOneWayBlockingGitHub(t *testing.T) {
	snapshot := base(
		Item{Number: 110, Kind: KindTask, State: StateOpen, BlockedBy: []Link{{Number: 120, State: StateOpen}}},
		Item{Number: 120, Kind: KindTask, State: StateOpen},
	)
	runCases(t, []ruleCase{{
		name:       "the blocked_by edge is the only direction",
		snapshot:   snapshot,
		dispatches: []int{120},
		reasons:    map[int]Reason{110: ReasonBlocked},
	}})
}

// TestRuleAscendingOrder: the plan takes candidates in ascending number.
func TestRuleAscendingOrder(t *testing.T) {
	runCases(t, []ruleCase{{
		name:       "out-of-order input is sorted",
		snapshot:   base(task(9), task(2), task(5)),
		dispatches: []int{2, 5, 9},
	}})
}

// TestRuleCapByFreeBuilders: free builders cap the dispatch; the rest say why.
func TestRuleCapByFreeBuilders(t *testing.T) {
	free := base(task(1), task(2), task(3), task(4), task(5))
	free.Builders = []string{"seat-1", "seat-2"}
	capped := base(task(1), task(2), task(3))
	capped.Project.ConcurrencyCap = 1
	noBuilders := base(task(1))
	noBuilders.Builders = nil
	runCases(t, []ruleCase{
		{
			name:       "two free builders take the two smallest",
			snapshot:   free,
			dispatches: []int{1, 2},
			reasons:    map[int]Reason{3: ReasonNoFreeBuilder, 4: ReasonNoFreeBuilder, 5: ReasonNoFreeBuilder},
		},
		{
			name:       "the project cap wins when smaller",
			snapshot:   capped,
			dispatches: []int{1},
			reasons:    map[int]Reason{2: ReasonNoFreeBuilder, 3: ReasonNoFreeBuilder},
		},
		{
			name:       "no builders leaves every candidate with the reason",
			snapshot:   noBuilders,
			dispatches: nil,
			reasons:    map[int]Reason{1: ReasonNoFreeBuilder},
		},
	})
}

// TestRuleBusyBuilder: a live row's destination is not free.
func TestRuleBusyBuilder(t *testing.T) {
	snapshot := base(task(1), task(2))
	snapshot.Builders = []string{"seat-1", "seat-2"}
	snapshot.Rows = []Row{{QItemID: "qitem-1", State: "in-progress", Destination: "seat-1"}}
	result := Decide(snapshot)
	if len(result.Dispatches) != 1 || result.Dispatches[0].Number != 1 || result.Dispatches[0].Seat != "seat-2" {
		t.Fatalf("dispatches = %+v, want #1 on seat-2", result.Dispatches)
	}
	if result.Free != 1 {
		t.Fatalf("free = %d, want 1", result.Free)
	}
	reasons := map[int]Reason{}
	for _, skip := range result.Skips {
		reasons[skip.Number] = skip.Reason
	}
	if reasons[2] != ReasonNoFreeBuilder {
		t.Fatalf("#2 reason = %q, want %q", reasons[2], ReasonNoFreeBuilder)
	}
}

// TestForgeReadStale: a failed forge read is stale, never "no tasks".
func TestForgeReadStale(t *testing.T) {
	snapshot := base(task(1))
	snapshot.ForgeOK = false
	snapshot.ForgeError = "forge read failed: 502"
	result := Decide(snapshot)
	if result.Status != StatusStale {
		t.Fatalf("status = %q, want %q", result.Status, StatusStale)
	}
	if len(result.Dispatches) != 0 || len(result.Skips) != 0 {
		t.Fatalf("stale plan carried work: %+v", result)
	}
}

func dispatchNumbers(result Result) []int {
	out := make([]int, len(result.Dispatches))
	for index, dispatch := range result.Dispatches {
		out[index] = dispatch.Number
	}
	return out
}

func TestDisabledProjectPlanHasNoIntent(t *testing.T) {
	snapshot := base(task(1))
	snapshot.Project.Enabled = false
	plan := PlanTick(snapshot, time.Now())
	if plan.Status != StatusNothing || len(plan.Intents) != 0 {
		t.Fatalf("plan = %+v, want nothing with no intents for a disabled project", plan)
	}
}

func TestTwoRigsFillInListedOrderUnderOneCap(t *testing.T) {
	snapshot := base(task(1), task(2))
	snapshot.Builders = []string{"build-1@rig-a", "build-1@rig-b"}
	snapshot.Project.ConcurrencyCap = 2
	plan := PlanTick(snapshot, time.Now())
	if len(plan.Intents) != 2 {
		t.Fatalf("intents = %+v, want two", plan.Intents)
	}
	if plan.Intents[0].Seat != "build-1@rig-a" || plan.Intents[1].Seat != "build-1@rig-b" {
		t.Fatalf("seats = %s, %s; want the listed rig order", plan.Intents[0].Seat, plan.Intents[1].Seat)
	}
	snapshot.Project.ConcurrencyCap = 1
	plan = PlanTick(snapshot, time.Now())
	if len(plan.Intents) != 1 {
		t.Fatalf("intents with cap 1 = %+v, want one", plan.Intents)
	}
}
