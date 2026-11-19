package act

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/forge"
)

// These tests call completeEpic directly, so they reach the arms the sweep holds
// before: an open delivery suppresses completion at the sweep, and a snapshot that
// cannot see the delivery is what lets the rollup or sweep reach it (#533).

// TestCompletionReusesTheOpenRequest (#533): the delivery is already open in the
// tick's snapshot. No second request is opened, the epic is still marked
// completed for this tick, the existing number is what gets reported, and no
// skip line is written.
func TestCompletionReusesTheOpenRequest(t *testing.T) {
	ctx := context.Background()
	actions, in, fake, git, _ := completionWorld(t)
	in.CRs = []forge.ChangeRequest{
		{Number: 90, State: "open", Branch: "epic/21-epic-forty-eight", Base: "main"},
	}
	report := &TickReport{}
	actions.completeEpic(ctx, in, report, map[int]bool{}, 21, nil)

	if len(fake.RequestsValue) != 0 {
		t.Fatalf("opened %d requests while #90 is already open: %+v", len(fake.RequestsValue), fake.RequestsValue)
	}
	if len(report.Completions) != 1 || report.Completions[0].ChangeRequest != 90 {
		t.Fatalf("completions = %+v, want one naming #90", report.Completions)
	}
	if len(report.Skipped) != 0 {
		t.Fatalf("an already-open delivery wrote skip lines: %v", report.Skipped)
	}
	if git.fetched != 1 {
		t.Fatalf("fetch ran %d times", git.fetched)
	}
}

// TestCompletionReuseRunsDriftAgainstTheExistingRequest (#533): the drift step
// is D7's outcome and must not be tied to a successful open. The branch is
// behind, so the existing request is asked to update.
func TestCompletionReuseRunsDriftAgainstTheExistingRequest(t *testing.T) {
	ctx := context.Background()
	actions, in, fake, git, _ := completionWorld(t)
	git.ancestors = map[string]bool{"base-sha epic-head": false}
	in.CRs = []forge.ChangeRequest{
		{Number: 90, State: "open", Branch: "epic/21-epic-forty-eight", Base: "main"},
	}
	report := &TickReport{}
	actions.completeEpic(ctx, in, report, map[int]bool{}, 21, nil)

	if len(fake.RequestsValue) != 0 {
		t.Fatalf("opened a second request: %+v", fake.RequestsValue)
	}
	updated := false
	for _, write := range fake.Writes {
		if write.Op == "update_branch" && write.Number == 90 {
			updated = true
		}
	}
	if !updated {
		t.Fatalf("drift did not update the existing #90: %v", fake.Writes)
	}
	if len(report.Completions) != 1 || report.Completions[0].ChangeRequest != 90 {
		t.Fatalf("completions = %+v", report.Completions)
	}
}

// TestCompletionOpensWhenNoRequestIsOpen (#533): the ordinary path is unchanged
// when nothing is open — exactly one open, against the epic branch and main.
func TestCompletionOpensWhenNoRequestIsOpen(t *testing.T) {
	ctx := context.Background()
	actions, in, fake, _, _ := completionWorld(t)
	report := &TickReport{}
	actions.completeEpic(ctx, in, report, map[int]bool{}, 21, nil)

	if len(fake.RequestsValue) != 1 {
		t.Fatalf("opened %d requests, want one: %+v", len(fake.RequestsValue), fake.RequestsValue)
	}
	if got := fake.RequestsValue[0]; got.Branch != "epic/21-epic-forty-eight" || got.Base != "main" || got.Draft {
		t.Fatalf("opened %+v, want a non-draft epic/<21> request based on main", got)
	}
	if len(report.Completions) != 1 {
		t.Fatalf("completions = %+v", report.Completions)
	}
}

// TestCompletionTreatsAlreadyExistsAsDelivered (#533): the snapshot was stale
// and the forge refused the open because the request exists. The refusal is not
// an error skip — the request is read back, its number is reported, and no
// second open is recorded as delivered by this tick.
func TestCompletionTreatsAlreadyExistsAsDelivered(t *testing.T) {
	ctx := context.Background()
	actions, in, fake, _, _ := completionWorld(t)
	fake.OpenCRErr = errors.New("Creating merge request: Another open merge request already exists for this source branch: !90")
	fake.RequestsValue = []forge.ChangeRequest{
		{Number: 90, State: "open", Branch: "epic/21-epic-forty-eight", Base: "main"},
	}
	report := &TickReport{}
	actions.completeEpic(ctx, in, report, map[int]bool{}, 21, nil)

	if len(report.Completions) != 1 || report.Completions[0].ChangeRequest != 90 {
		t.Fatalf("completions = %+v, want one naming the existing #90", report.Completions)
	}
	for _, line := range report.Skipped {
		if strings.Contains(line, "already exists") {
			t.Fatalf("the already-exists refusal is an error skip: %v", report.Skipped)
		}
	}
	if len(fake.RequestsValue) != 1 {
		t.Fatalf("read-back changed the snapshot unexpectedly: %+v", fake.RequestsValue)
	}
}
