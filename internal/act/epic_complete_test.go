package act

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// TestBranchParsingReadsBothPrefixes (#490, task 6.1): the app's branch parser
// reads the item number from feature/<n>- and epic/<n>- alike, so the mirror can
// associate an epic branch's change request with its epic.
func TestBranchParsingReadsBothPrefixes(t *testing.T) {
	cases := []struct {
		branch string
		item   int
		ok     bool
	}{
		{"feature/476-x", 476, true},
		{"epic/476-x", 476, true},
		{"feature/476", 476, true},
		{"epic/476", 476, true},
		{"main", 0, false},
		{"epic/abc", 0, false},
		{"epic/", 0, false},
	}
	for _, tc := range cases {
		if got, ok := branchItem(tc.branch); got != tc.item || ok != tc.ok {
			t.Fatalf("branchItem(%q) = %d,%v want %d,%v", tc.branch, got, ok, tc.item, tc.ok)
		}
	}
	// epicBranchNumber is the epic-only view: feature branches are not epics.
	if number, ok := epicBranchNumber("epic/476-x"); !ok || number != 476 {
		t.Fatalf("epicBranchNumber(epic/476-x) = %d,%v", number, ok)
	}
	if _, ok := epicBranchNumber("feature/476-x"); ok {
		t.Fatal("epicBranchNumber read a feature branch as an epic")
	}
}

// TestOpenEpicDeliveryFindsTheDelivery: the delivery is the open change request
// whose head is epic/<n>-, never a merged or closed one.
func TestOpenEpicDeliveryFindsTheDelivery(t *testing.T) {
	requests := []forge.ChangeRequest{
		{Number: 1, State: "merged", Merged: true, Branch: "epic/21-x"},
		{Number: 2, State: "closed", Branch: "epic/21-x"},
		{Number: 3, State: "open", Branch: "feature/21-x"},
		{Number: 4, State: "open", Branch: "epic/21-epic-forty-eight"},
	}
	delivery := openEpicDelivery(requests, 21)
	if delivery == nil || delivery.Number != 4 || delivery.Branch != "epic/21-epic-forty-eight" {
		t.Fatalf("delivery = %+v, want the open epic/<21>- change request", delivery)
	}
	if openEpicDelivery(requests, 99) != nil {
		t.Fatal("a delivery was found for an epic with none")
	}
}

// TestRollupAndSweepHoldOnAnOpenDelivery (#490, design D5): both decisions skip
// with the same reason while the epic's delivery is open.
func TestRollupAndSweepHoldOnAnOpenDelivery(t *testing.T) {
	delivery := &EpicDelivery{Branch: "epic/21-epic-forty-eight", Number: 90}
	parent := 21
	items := []forge.Item{
		{Number: 21, Kind: forge.KindEpic, State: forge.StateOpen, Title: "Epic forty eight"},
		{Number: 22, Kind: forge.KindTask, State: forge.StateClosed, Parent: &parent},
	}
	rollup, err := Rollup(&parent, items, delivery)
	if err != nil || rollup.ShouldClose() {
		t.Fatalf("Rollup = %+v, %v; want a hold", rollup, err)
	}
	sweep := Sweep(items[0], items, delivery)
	if sweep.ShouldClose() {
		t.Fatalf("Sweep = %+v; want a hold", sweep)
	}
	for _, decision := range []Decision{rollup, sweep} {
		if !strings.Contains(decision.Reason, "integration branch open") ||
			!strings.Contains(decision.Reason, "epic/21-epic-forty-eight") ||
			!strings.Contains(decision.Reason, "#90") {
			t.Fatalf("hold reason = %q, want the branch and change request named", decision.Reason)
		}
	}
	t.Logf("hold: %s", rollup.Reason)
}

// completionWorld builds an opted-in project with one open epic and two closed
// children, ready for the sweep to deliver.
func completionWorld(t *testing.T) (*Actions, TickInput, *forge.Fake, *fakeGit, store.ProjectRow) {
	t.Helper()
	ctx := context.Background()
	db := newStore(t)
	pid, err := db.CreateProject(ctx, store.ProjectInput{
		Key: "epicapp", Name: "epicapp", ForgeKind: "github", ForgeProject: "o/r",
		Rig: "rig", ConcurrencyCap: 2, Enabled: true, DefaultBranch: "main",
	})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	row, err := db.ProjectByKey(ctx, "epicapp")
	if err != nil || row == nil {
		t.Fatalf("project read: %v", err)
	}
	parent := 21
	children := []forge.Item{
		{Number: 22, Kind: forge.KindTask, State: forge.StateClosed, Title: "First child", Parent: &parent},
		{Number: 23, Kind: forge.KindTask, State: forge.StateClosed, Title: "Second child", Parent: &parent},
	}
	fake := &forge.Fake{SubIssuesValue: map[int][]forge.Item{21: children}}
	git := &fakeGit{
		matchingBranches: []string{"origin/epic/21-epic-forty-eight"},
		refSHAs:          map[string]string{"origin/epic/21-epic-forty-eight": "epic-head"},
		ancestors:        map[string]bool{"base-sha epic-head": true}, // up to date, no drift
	}
	actions := &Actions{
		Store: db, Forge: fake, Git: git, ProjectID: pid,
		EpicBranches: true, DefaultBranch: "main",
	}
	in := TickInput{
		Project: *row,
		Items: []forge.Item{
			{Number: 21, Kind: forge.KindEpic, State: forge.StateOpen, Title: "Epic forty eight"},
		},
		WorktreeRoot: t.TempDir(),
	}
	return actions, in, fake, git, *row
}

// TestCompletionOpensOneEpicRequest (#490, task 6.2): every child closed, the
// branch ahead, no delivery yet — the sweep opens exactly one non-draft request
// naming every child and Closes #<epic>, and does not close the epic. A second
// tick with the delivery open opens nothing.
func TestCompletionOpensOneEpicRequest(t *testing.T) {
	ctx := context.Background()
	actions, in, fake, _, project := completionWorld(t)
	in.CRs = []forge.ChangeRequest{
		{Number: 70, State: "merged", Merged: true, Branch: "feature/22-first-child", Body: "Closes #22"},
	}
	report := &TickReport{}
	actions.sweepEpics(ctx, in, report, map[int]bool{}, map[int]bool{})

	if len(report.Completions) != 1 {
		t.Fatalf("completions = %+v, want one", report.Completions)
	}
	if len(fake.RequestsValue) != 1 {
		t.Fatalf("opened %d change requests, want exactly one: %+v", len(fake.RequestsValue), fake.RequestsValue)
	}
	created := fake.RequestsValue[0]
	if created.Branch != "epic/21-epic-forty-eight" || created.Base != "main" || created.Draft {
		t.Fatalf("created = %+v, want a non-draft epic/<21> request based on main", created)
	}
	if created.Title != "Epic #21: Epic forty eight" {
		t.Fatalf("title = %q", created.Title)
	}
	for _, want := range []string{"- #22 First child", "- #23 Second child", "Closes #21"} {
		if !strings.Contains(created.Body, want) {
			t.Fatalf("body misses %q:\n%s", want, created.Body)
		}
	}
	if !strings.Contains(created.Body, "### Merged\n- #22") {
		t.Fatalf("merged child not under ### Merged:\n%s", created.Body)
	}
	if !strings.Contains(created.Body, "### Not merged\n- #23") {
		t.Fatalf("unmerged child not under ### Not merged:\n%s", created.Body)
	}
	// The epic stays open for the operator to merge.
	for _, write := range fake.Writes {
		if write.Op == "close" && write.Number == 21 {
			t.Fatalf("the completion closed the epic: %+v", fake.Writes)
		}
		if write.Op == "merge" {
			t.Fatalf("the completion merged something: %+v", fake.Writes)
		}
	}

	// The second tick sees the delivery and holds; it opens nothing.
	in.CRs = append(in.CRs, created)
	fake.Writes = nil
	openedBefore := len(fake.RequestsValue)
	second := &TickReport{}
	actions.sweepEpics(ctx, in, second, map[int]bool{}, map[int]bool{})
	if len(fake.RequestsValue) != openedBefore {
		t.Fatalf("a second tick opened another change request: %+v", fake.RequestsValue)
	}
	if len(second.Completions) != 0 {
		t.Fatalf("a second tick completed again: %+v", second.Completions)
	}
	found := false
	for _, line := range second.Skipped {
		if strings.Contains(line, "integration branch open") && strings.Contains(line, "epic/21-epic-forty-eight") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the second tick's skip does not name the hold: %v", second.Skipped)
	}
	_ = project
}

// TestRollupPathCompletesOnLastChild (#490, task 6.2): the tick that merges the
// last child reaches completion through rollupAfterMerge, not only the sweep, and
// the body read comes from the sub-issues read the rollup path does not already
// have.
func TestRollupPathCompletesOnLastChild(t *testing.T) {
	ctx := context.Background()
	actions, in, fake, _, _ := completionWorld(t)
	parent := 21
	in.Items = append(in.Items,
		forge.Item{Number: 22, Kind: forge.KindTask, State: forge.StateClosed, Title: "First child", Parent: &parent})
	report := &TickReport{}
	actions.rollupAfterMerge(ctx, in, report, map[int]bool{}, map[int]bool{}, 22, &MergeResult{ChangeRequest: 70})

	if len(fake.RequestsValue) != 1 {
		t.Fatalf("the rollup path opened %d requests, want one: %+v", len(fake.RequestsValue), fake.RequestsValue)
	}
	if len(report.Completions) != 1 || report.Completions[0].ChangeRequest != fake.RequestsValue[0].Number {
		t.Fatalf("completions = %+v", report.Completions)
	}
	for _, write := range fake.Writes {
		if write.Op == "close" && write.Number == 21 {
			t.Fatalf("the rollup path closed the epic instead of delivering it: %+v", fake.Writes)
		}
	}
}

// TestCompletionResolvesTheBranchByPrefixAfterARetitle (#490, design D2): the
// delivery branch is found by epic/<n>-, not rebuilt from the current title, so
// a retitle between dispatches does not wedge completion.
func TestCompletionResolvesTheBranchByPrefixAfterARetitle(t *testing.T) {
	ctx := context.Background()
	actions, in, fake, git, _ := completionWorld(t)
	// The epic now reads "Epic forty eight" but its branch was cut as "old-title".
	git.matchingBranches = []string{"origin/epic/21-old-title"}
	git.refSHAs = map[string]string{"origin/epic/21-old-title": "epic-head"}
	report := &TickReport{}
	actions.sweepEpics(ctx, in, report, map[int]bool{}, map[int]bool{})

	if len(fake.RequestsValue) != 1 {
		t.Fatalf("a retitled epic delivered %d requests, want one: %v", len(fake.RequestsValue), report.Skipped)
	}
	if got := fake.RequestsValue[0].Branch; got != "epic/21-old-title" {
		t.Fatalf("delivery head = %q, want the existing prefix-resolved branch", got)
	}
}

// TestCompletionRefusesToGuessBetweenTwoBranches (#490, design D2): more than one
// epic/<n>- branch is named, not guessed.
func TestCompletionRefusesToGuessBetweenTwoBranches(t *testing.T) {
	ctx := context.Background()
	actions, in, fake, git, _ := completionWorld(t)
	git.matchingBranches = []string{"origin/epic/21-a-title", "origin/epic/21-b-title"}
	report := &TickReport{}
	actions.sweepEpics(ctx, in, report, map[int]bool{}, map[int]bool{})

	if len(fake.RequestsValue) != 0 {
		t.Fatalf("two epic branches still opened a delivery: %v", fake.RequestsValue)
	}
	found := false
	for _, line := range report.Skipped {
		if strings.Contains(line, "more than one branch") &&
			strings.Contains(line, "epic/21-a-title") && strings.Contains(line, "epic/21-b-title") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the ambiguity is not named: %v", report.Skipped)
	}
}

// TestEpicDirectIsClosedNotDelivered (#490, design D1): the per-epic escape opts
// the epic out of branch delivery entirely — it closes the ordinary way.
func TestEpicDirectIsClosedNotDelivered(t *testing.T) {
	ctx := context.Background()
	actions, in, fake, _, _ := completionWorld(t)
	in.Items[0].Labels = []string{epicDirectLabel}
	report := &TickReport{}
	actions.sweepEpics(ctx, in, report, map[int]bool{}, map[int]bool{})

	if len(fake.RequestsValue) != 0 {
		t.Fatalf("an epic::direct epic was delivered on a branch: %v", fake.RequestsValue)
	}
	closed := false
	for _, write := range fake.Writes {
		if write.Op == "close" && write.Number == 21 {
			closed = true
		}
	}
	if !closed {
		t.Fatalf("an epic::direct epic was neither delivered nor closed: %v", fake.Writes)
	}
}

// TestCompletionConflictStillOpens (#490, design D7): when the epic branch is
// behind and the forge refuses the update, the notice is recorded and the change
// request is still opened.
func TestCompletionConflictStillOpens(t *testing.T) {
	ctx := context.Background()
	actions, in, fake, git, _ := completionWorld(t)
	// The default has a commit the epic branch lacks, and the forge refuses.
	git.ancestors = map[string]bool{"base-sha epic-head": false}
	fake.UpdateBranchErr = errors.New("conflict: cannot rebase")
	report := &TickReport{}
	actions.sweepEpics(ctx, in, report, map[int]bool{}, map[int]bool{})

	if len(fake.RequestsValue) != 1 {
		t.Fatalf("the request was not opened despite the conflict: %+v", fake.RequestsValue)
	}
	if len(report.Completions) != 1 || !strings.Contains(report.Completions[0].Notice, "epic branch is behind and cannot be updated") {
		t.Fatalf("notice = %+v", report.Completions)
	}
	found := false
	for _, line := range report.Skipped {
		if strings.Contains(line, "epic branch is behind and cannot be updated") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the conflict notice is not in the tick's lines: %v", report.Skipped)
	}
	t.Logf("drift notice: %s", report.Completions[0].Notice)
}
