package decide

import "testing"

// TestManualTaskIsNeverDispatched is the unit's first acceptance (#527): an
// item carrying the default `manual` label is skipped with its own reason and
// the sibling beside it carries on.
func TestManualTaskIsNeverDispatched(t *testing.T) {
	s := base(task(2, "manual"), task(3))

	result := Decide(s)

	if got := dispatchNumbers(result); len(got) != 1 || got[0] != 3 {
		t.Fatalf("dispatches = %v, want only #3", got)
	}
	if got := reasonsOf(result)[2]; got != ReasonManual {
		t.Fatalf("#2 reason = %q, want %q", got, ReasonManual)
	}
	for _, skip := range result.Skips {
		if skip.Number == 2 && skip.Detail != "owner-only task" {
			t.Fatalf("#2 detail = %q, want the owner note", skip.Detail)
		}
	}
}

// TestManualLabelFromTheProject: the label is a project choice. A project that
// names `owner-only` has its own work parked, while an item labelled the
// default `manual` is ordinary work there.
func TestManualLabelFromTheProject(t *testing.T) {
	s := base(task(2, "manual"), task(3, "owner-only"))
	s.Project.ManualLabel = "owner-only"

	result := Decide(s)

	if got := dispatchNumbers(result); len(got) != 1 || got[0] != 2 {
		t.Fatalf("dispatches = %v, want only #2", got)
	}
	if got := reasonsOf(result)[3]; got != ReasonManual {
		t.Fatalf("#3 reason = %q, want %q", got, ReasonManual)
	}
}

// TestManualLabelDefaultsWhenTheCallerForgets: an empty label is the default,
// not "the rule is off" — a caller that never threads the config keeps the
// protection every project has before the key existed.
func TestManualLabelDefaultsWhenTheCallerForgets(t *testing.T) {
	s := base(task(2, "manual"))
	s.Project.ManualLabel = ""

	if got := reasonsOf(Decide(s))[2]; got != ReasonManual {
		t.Fatalf("#2 reason = %q, want %q", got, ReasonManual)
	}
}

// TestManualChildDoesNotHoldTheEpicSlot is the third acceptance: under the
// default serial-epic rule a manual child is not the epic's current work, so
// the next sibling dispatches in the same tick — even when a person already
// has a change request open on the manual child.
func TestManualChildDoesNotHoldTheEpicSlot(t *testing.T) {
	s := base(epic(1), child(task(2, "manual"), 1), child(task(3), 1))
	s.Branches = []Branch{{Name: "feature/2-owner-check", Item: 2, HasOpenCR: true}}

	result := Decide(s)

	if got := dispatchNumbers(result); len(got) != 1 || got[0] != 3 {
		t.Fatalf("dispatches = %v, want #3: the manual child must not hold the epic", got)
	}
	if got := reasonsOf(result)[2]; got != ReasonManual {
		t.Fatalf("#2 reason = %q, want %q", got, ReasonManual)
	}
	for _, skip := range result.Skips {
		if skip.Number == 3 {
			t.Fatalf("#3 skipped: %+v, want it dispatched", skip)
		}
	}
}
