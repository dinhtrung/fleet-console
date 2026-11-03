package act

import (
	"reflect"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/forge"
)

// epicRow is the epic's own mirror row, the way the sweep receives it: the
// candidate, with its number and its state.
func epicRow(number int, state string) forge.Item {
	return forge.Item{Number: number, Kind: forge.KindEpic, State: state, Title: "an epic"}
}

// TestSweep is the table the issue names: all children closed → close; two of
// three closed → skip naming the open one; zero children → the guard, in both
// its arms (an empty read, and a read holding only another epic's children); an
// epic already closed → silent skip; a non-epic row → skip. Each case asserts the
// reason verbatim, because the tick prints it, and Quiet, because the sweep must
// not journal an unfinished epic on every tick.
func TestSweep(t *testing.T) {
	cases := []struct {
		name      string
		candidate forge.Item
		items     []forge.Item
		wantKind  DecisionKind
		wantEpic  int
		children  int
		reason    string
		wantQuiet bool
	}{
		{
			name:      "all children closed",
			candidate: epicRow(84, forge.StateOpen),
			items: []forge.Item{
				child(81, 84, forge.StateClosed),
				child(82, 84, forge.StateClosed),
				child(83, 84, forge.StateClosed),
			},
			wantKind: RollupClose,
			wantEpic: 84,
			children: 3,
		},
		{
			name:      "two of three closed names the open one",
			candidate: epicRow(84, forge.StateOpen),
			items: []forge.Item{
				child(81, 84, forge.StateClosed),
				child(82, 84, forge.StateClosed),
				child(83, 84, forge.StateOpen),
			},
			wantKind:  RollupSkip,
			wantEpic:  84,
			children:  3,
			reason:    "1 sub-issue still open (#83)",
			wantQuiet: true,
		},
		{
			name:      "zero children is the guard",
			candidate: epicRow(84, forge.StateOpen),
			wantKind:  RollupSkip,
			wantEpic:  84,
			reason:    "no sub-issues in the read",
		},
		{
			name:      "a read holding only another epic's children is the guard",
			candidate: epicRow(84, forge.StateOpen),
			items:     []forge.Item{child(90, 91, forge.StateOpen)},
			wantKind:  RollupSkip,
			wantEpic:  84,
			reason:    "no sub-issues in the read",
		},
		{
			name:      "an epic already closed",
			candidate: epicRow(84, forge.StateClosed),
			items:     []forge.Item{child(83, 84, forge.StateClosed)},
			wantKind:  RollupSkip,
			wantEpic:  84,
			reason:    "epic already closed",
			wantQuiet: true,
		},
		{
			name:      "a non-epic row",
			candidate: forge.Item{Number: 84, Kind: forge.KindTask, State: forge.StateOpen},
			items:     []forge.Item{child(83, 84, forge.StateClosed)},
			wantKind:  RollupSkip,
			wantEpic:  84,
			reason:    "not an epic",
			wantQuiet: true,
		},
		{
			name:      "a child of another epic is not counted",
			candidate: epicRow(84, forge.StateOpen),
			items: []forge.Item{
				child(83, 84, forge.StateClosed),
				child(90, 91, forge.StateOpen),
			},
			wantKind: RollupClose,
			wantEpic: 84,
			children: 1,
		},
		{
			name:      "the epic's own row does not count itself",
			candidate: epicRow(84, forge.StateOpen),
			items: []forge.Item{
				epicRow(84, forge.StateOpen),
				child(83, 84, forge.StateClosed),
			},
			wantKind: RollupClose,
			wantEpic: 84,
			children: 1,
		},
		{
			name:      "two open children are named in number order",
			candidate: epicRow(84, forge.StateOpen),
			items: []forge.Item{
				child(95, 84, forge.StateOpen),
				child(83, 84, forge.StateOpen),
				child(90, 84, forge.StateClosed),
			},
			wantKind:  RollupSkip,
			wantEpic:  84,
			children:  3,
			reason:    "2 sub-issues still open (#83, #95)",
			wantQuiet: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision := Sweep(tc.candidate, tc.items)
			if decision.Kind != tc.wantKind {
				t.Fatalf("kind = %q, want %q (reason %q)", decision.Kind, tc.wantKind, decision.Reason)
			}
			if decision.Epic != tc.wantEpic {
				t.Fatalf("epic = %d, want %d", decision.Epic, tc.wantEpic)
			}
			if decision.Children != tc.children {
				t.Fatalf("children = %d, want %d", decision.Children, tc.children)
			}
			if decision.Reason != tc.reason {
				t.Fatalf("reason = %q, want %q", decision.Reason, tc.reason)
			}
			if decision.Quiet != tc.wantQuiet {
				t.Fatalf("quiet = %v, want %v", decision.Quiet, tc.wantQuiet)
			}
			if decision.ShouldClose() != (tc.wantKind == RollupClose) {
				t.Fatalf("ShouldClose() = %v for kind %q", decision.ShouldClose(), decision.Kind)
			}
		})
	}
}

// TestSweepIsPure: the same read always gives the same verdict, and the caller's
// slice is not modified. A rule the tick runs over the mirror's own read must not
// be able to change what it read.
func TestSweepIsPure(t *testing.T) {
	items := []forge.Item{
		child(95, 84, forge.StateOpen),
		child(83, 84, forge.StateClosed),
	}
	before := make([]forge.Item, len(items))
	copy(before, items)

	first := Sweep(epicRow(84, forge.StateOpen), items)
	second := Sweep(epicRow(84, forge.StateOpen), items)

	// The scenario promises the same kind, reason, child count, open list and quiet
	// flag. DeepEqual covers all five in one assertion; comparing fields by hand is
	// how a regression that changes only Children, Open or Quiet slips through.
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("two calls differ: %+v vs %+v", first, second)
	}
	for index := range items {
		if items[index].Number != before[index].Number || items[index].State != before[index].State {
			t.Fatalf("item %d was modified by Sweep", before[index].Number)
		}
	}
	// Open is sorted, so a caller printing it gets a stable sentence.
	if len(first.Open) != 1 || first.Open[0] != 95 {
		t.Fatalf("open = %v, want [95]", first.Open)
	}
}
