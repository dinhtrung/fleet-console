package act

import (
	"testing"

	"github.com/dinhtrung/fleet-console/internal/forge"
)

func intp(n int) *int { return &n }

// epic is one epic with children in whatever states a case needs.
func epic(number int, state string, children ...forge.Item) []forge.Item {
	items := []forge.Item{{Number: number, Kind: forge.KindEpic, State: state, Title: "an epic"}}
	return append(items, children...)
}

func child(number, parent int, state string) forge.Item {
	return forge.Item{Number: number, Kind: forge.KindTask, State: state, Parent: intp(parent)}
}

// TestRollup is the table the task names: all closed / one open / none in the
// snapshot / parent not epic / no parent / parent already closed. Each case
// asserts the reason verbatim, because the tick prints it.
func TestRollup(t *testing.T) {
	cases := []struct {
		name     string
		parent   *int
		items    []forge.Item
		wantKind DecisionKind
		wantEpic int
		children int
		reason   string
	}{
		{
			name:     "all children closed",
			parent:   intp(64),
			items:    epic(64, forge.StateOpen, child(62, 64, forge.StateClosed), child(63, 64, forge.StateClosed)),
			wantKind: RollupClose,
			wantEpic: 64,
			children: 2,
		},
		{
			name:     "one open",
			parent:   intp(64),
			items:    epic(64, forge.StateOpen, child(62, 64, forge.StateClosed), child(63, 64, forge.StateOpen)),
			wantKind: RollupSkip,
			wantEpic: 64,
			children: 2,
			reason:   "1 sub-issue still open (#63)",
		},
		{
			name:     "two open names both in order",
			parent:   intp(64),
			items:    epic(64, forge.StateOpen, child(70, 64, forge.StateOpen), child(62, 64, forge.StateOpen), child(63, 64, forge.StateClosed)),
			wantKind: RollupSkip,
			wantEpic: 64,
			children: 3,
			reason:   "2 sub-issues still open (#62, #70)",
		},
		{
			name:     "none in the snapshot",
			parent:   intp(64),
			items:    epic(64, forge.StateOpen),
			wantKind: RollupSkip,
			wantEpic: 64,
			children: 0,
			reason:   "no sub-issues in the snapshot",
		},
		{
			name:     "parent not an epic",
			parent:   intp(64),
			items:    []forge.Item{{Number: 64, Kind: forge.KindTask, State: forge.StateOpen}},
			wantKind: RollupSkip,
			wantEpic: 64,
			reason:   "parent not an epic",
		},
		{
			name:     "no parent",
			parent:   nil,
			items:    epic(64, forge.StateOpen, child(62, 64, forge.StateClosed)),
			wantKind: RollupSkip,
			reason:   "no parent",
		},
		{
			name:     "parent already closed",
			parent:   intp(64),
			items:    epic(64, forge.StateClosed, child(62, 64, forge.StateClosed)),
			wantKind: RollupSkip,
			wantEpic: 64,
			reason:   "epic already closed",
		},
		{
			name:     "parent missing from the snapshot entirely",
			parent:   intp(99),
			items:    epic(64, forge.StateOpen, child(62, 64, forge.StateClosed)),
			wantKind: RollupSkip,
			wantEpic: 99,
			reason:   "parent not an epic",
		},
		{
			name:     "a child of another epic is not counted",
			parent:   intp(64),
			items:    append(epic(64, forge.StateOpen, child(62, 64, forge.StateClosed)), child(80, 70, forge.StateOpen)),
			wantKind: RollupClose,
			wantEpic: 64,
			children: 1,
		},
		{
			name:     "an epic that is its own child does not count itself",
			parent:   intp(64),
			items:    epic(64, forge.StateOpen, child(62, 64, forge.StateClosed)),
			wantKind: RollupClose,
			wantEpic: 64,
			children: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision, err := Rollup(tc.parent, tc.items)
			if err != nil {
				t.Fatalf("Rollup returned %v, want nil", err)
			}
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
			if decision.ShouldClose() != (tc.wantKind == RollupClose) {
				t.Fatalf("ShouldClose() = %v for kind %q", decision.ShouldClose(), decision.Kind)
			}
		})
	}
}

// TestRollupIsPure: the same snapshot always gives the same verdict, and the
// caller's slice is not modified. A rule the tick runs on the mirror's own read
// must not be able to change what it read.
func TestRollupIsPure(t *testing.T) {
	items := epic(64, forge.StateOpen,
		child(70, 64, forge.StateOpen),
		child(62, 64, forge.StateClosed),
	)
	before := make([]forge.Item, len(items))
	copy(before, items)

	first, _ := Rollup(intp(64), items)
	second, _ := Rollup(intp(64), items)

	if first.Kind != second.Kind || first.Reason != second.Reason {
		t.Fatalf("two calls differ: %+v vs %+v", first, second)
	}
	for index := range items {
		if items[index].Number != before[index].Number || items[index].State != before[index].State {
			t.Fatalf("item %d was modified by Rollup", before[index].Number)
		}
	}
	// Open is sorted, so a caller printing it gets a stable sentence.
	if len(first.Open) != 1 || first.Open[0] != 70 {
		t.Fatalf("open = %v, want [70]", first.Open)
	}
}

// TestRollupReasonReadsAsASentence: "1 sub-issue" not "1 sub-issues", because the
// tick prints this line and a wrong plural makes the number look unreliable.
func TestRollupReasonReadsAsASentence(t *testing.T) {
	one, _ := Rollup(intp(64), epic(64, forge.StateOpen, child(62, 64, forge.StateOpen)))
	if one.Reason != "1 sub-issue still open (#62)" {
		t.Fatalf("reason = %q", one.Reason)
	}
	two, _ := Rollup(intp(64), epic(64, forge.StateOpen,
		child(62, 64, forge.StateOpen), child(63, 64, forge.StateOpen)))
	if two.Reason != "2 sub-issues still open (#62, #63)" {
		t.Fatalf("reason = %q", two.Reason)
	}
}
