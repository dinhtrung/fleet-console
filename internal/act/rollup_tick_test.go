package act

import (
	"context"
	"strings"
	"testing"
	"time"

	"fmt"

	"github.com/dinhtrung/fleet-console/internal/decide"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// rollupFixture is one epic with two children, one change request per child, and
// a verdict already accepted for each — everything RunTick needs to merge them.
type rollupFixture struct {
	st      *store.Store
	project store.ProjectRow
	fake    *forge.Fake
	in      TickInput
}

func newRollupFixture(t *testing.T, epicState string, childStates [2]string) *rollupFixture {
	t.Helper()
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)

	const parent = 64
	items := []forge.Item{
		{Number: parent, Kind: forge.KindEpic, State: epicState, Title: "An epic"},
		{Number: 62, Kind: forge.KindTask, State: childStates[0], Title: "First child", Parent: intPtr(parent)},
		{Number: 63, Kind: forge.KindTask, State: childStates[1], Title: "Second child", Parent: intPtr(parent)},
	}
	fake := forge.NewFake(forge.KindGitHub)
	fake.ItemsValue = items
	// Both children carry an accepted verdict on the current head and a docs-only
	// change request, which is the shortest green path through Merge.
	fake.RequestsValue = []forge.ChangeRequest{
		// Full 40-char shas: the merge gate's matcher needs two ≥ 7-char
		// spellings, and the fixture's old 5-char "sha62" would read as a typo.
		{Number: 90, HeadSHA: testFullSHA(62), Mergeable: true, Body: "Closes #62", DocsOnly: true},
		{Number: 91, HeadSHA: testFullSHA(63), Mergeable: true, Body: "Closes #63", DocsOnly: true},
	}
	dispatches := make(map[int]store.DispatchRow)
	for _, number := range []int{62, 63} {
		dispatchID, err := st.CreateDispatch(ctx, store.NewDispatch{
			ProjectID: pid, ItemNumber: number, Attempt: 1, Seat: "seat@rig",
			Branch: "feature/x", Worktree: t.TempDir(), BaseSHA: "base",
		})
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		if _, err := st.AddVerdict(ctx, store.NewVerdict{
			DispatchID: dispatchID, SHA: testFullSHA(number), Decision: "accept", Source: "note",
		}); err != nil {
			t.Fatalf("verdict: %v", err)
		}
		dispatches[number] = store.DispatchRow{ItemNumber: number, EndedAt: nil}
	}
	_ = dispatches

	// The mirror writes every item each tick, so the store carries the parent
	// even when a later forge read returns a truncated snapshot.
	records := make([]store.ItemRecord, 0, len(items))
	for _, item := range items {
		records = append(records, store.ItemRecord{
			Number: item.Number, Kind: string(item.Kind), State: item.State,
			Title: item.Title, Parent: item.Parent, URL: "u", UpdatedAt: time.Now().UTC(),
		})
	}
	if err := st.ReplaceItems(ctx, pid, records); err != nil {
		t.Fatalf("items: %v", err)
	}

	project := store.ProjectRow{
		ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r",
		Rig: "rig", ConcurrencyCap: 2, Enabled: true,
	}
	return &rollupFixture{
		st: st, project: project, fake: fake,
		in: TickInput{
			Store: st, Project: project, Forge: fake, Rig: &queueStub{}, Git: &fakeGit{},
			Gate: openGate(), Apply: true, Items: items, CRs: fake.RequestsValue,
			Plan: decide.Plan{}, WorktreeRoot: t.TempDir(),
		},
	}
}

// TestRollupClosesTheEpicAfterTheLastChild is #107's first assertion: the fake
// records comment(epic) then close(epic), and the report carries the rollup.
func TestRollupClosesTheEpicAfterTheLastChild(t *testing.T) {
	f := newRollupFixture(t, forge.StateOpen, [2]string{forge.StateClosed, forge.StateClosed})

	report, err := RunTick(context.Background(), f.in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Merges) == 0 {
		t.Fatalf("no merges; skipped = %v", report.Skipped)
	}

	// The writes, in order: every merge, then the epic comment, then the close.
	var ops []string
	for _, write := range f.fake.Writes {
		if write.Number == 64 {
			ops = append(ops, write.Op)
		}
	}
	if len(ops) != 2 || ops[0] != "comment" || ops[1] != "close" {
		t.Fatalf("epic writes = %v, want [comment close]", ops)
	}

	if len(report.Epics) != 1 || report.Epics[0].Epic != 64 || report.Epics[0].Children != 2 {
		t.Fatalf("report.Epics = %+v, want one close of #64 with 2 children", report.Epics)
	}

	// The comment names the item that finished the epic, so a reader a week later
	// knows why it closed.
	var comment string
	for _, write := range f.fake.Writes {
		if write.Op == "comment" && write.Number == 64 {
			comment = write.Value
		}
	}
	// The spec names the item that finished the epic, the change request that
	// carried it, and the fact that every sub-issue is closed — #63 is already
	// closed and is not the one that finished it.
	for _, want := range []string{"#62", "change request #90", "every sub-issue of this epic is closed"} {
		if !strings.Contains(comment, want) {
			t.Fatalf("comment = %q, want it to contain %q", comment, want)
		}
	}

	// And one epic.closed event carrying the count.
	events, err := f.st.RecentEvents(context.Background(), 50)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	var closed bool
	for _, event := range events {
		if event.Kind == "epic.closed" && event.Subject == "#64" && strings.Contains(event.Detail, "2 sub-issues") {
			closed = true
		}
	}
	if !closed {
		t.Fatalf("no epic.closed event for #64 among %d", len(events))
	}
}

// TestRollupStaysOpenWithAChildOpen is the second assertion: no write to the
// epic, and the skip line names the open child.
func TestRollupStaysOpenWithAChildOpen(t *testing.T) {
	f := newRollupFixture(t, forge.StateOpen, [2]string{forge.StateClosed, forge.StateOpen})

	report, err := RunTick(context.Background(), f.in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	for _, write := range f.fake.Writes {
		if write.Number == 64 {
			t.Fatalf("wrote to an epic with an open child: %+v", write)
		}
	}
	if len(report.Epics) != 0 {
		t.Fatalf("report.Epics = %+v, want none", report.Epics)
	}
	want := "#64 rollup: 1 sub-issue still open (#63)"
	var found bool
	for _, line := range report.Skipped {
		if line == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("skipped = %v, want %q", report.Skipped, want)
	}
}

// TestRollupSkipsAnEpicMissingFromTheSnapshot is the third assertion, and the one
// the issue singles out: a short read must never close an epic. The fixture has
// the merged child but not the epic itself.
func TestRollupSkipsAnEpicMissingFromTheSnapshot(t *testing.T) {
	f := newRollupFixture(t, forge.StateOpen, [2]string{forge.StateClosed, forge.StateClosed})
	// Drop the epic from the snapshot the tick reads, keeping the children: the
	// exact shape of a forge read that returned sub-issues but not the parent.
	items := make([]forge.Item, 0, len(f.in.Items))
	for _, item := range f.in.Items {
		if item.Kind == forge.KindEpic {
			continue
		}
		items = append(items, item)
	}
	f.in.Items = items

	report, err := RunTick(context.Background(), f.in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	for _, write := range f.fake.Writes {
		if write.Number == 64 {
			t.Fatalf("closed an epic that was not in the snapshot: %+v", write)
		}
	}
	if len(report.Epics) != 0 {
		t.Fatalf("report.Epics = %+v, want none", report.Epics)
	}
}

// TestRollupClosesOnceForTwoMergedSiblings is the fourth assertion: two merged
// items sharing a parent produce one close, not two.
func TestRollupClosesOnceForTwoMergedSiblings(t *testing.T) {
	f := newRollupFixture(t, forge.StateOpen, [2]string{forge.StateClosed, forge.StateClosed})

	report, err := RunTick(context.Background(), f.in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Merges) != 2 {
		t.Fatalf("merges = %d (%v), want two so the shared parent is exercised", len(report.Merges), report.Skipped)
	}
	closes := 0
	for _, write := range f.fake.Writes {
		if write.Number == 64 && write.Op == "close" {
			closes++
		}
	}
	if closes != 1 {
		t.Fatalf("closes of #64 = %d, want exactly 1 (%+v)", closes, f.fake.Writes)
	}
	if len(report.Epics) != 1 {
		t.Fatalf("report.Epics = %+v, want one", report.Epics)
	}
}

// TestRollupSkipLineIsSilentForANonEpicParent: the spec says a non-epic parent is
// "left alone" with nothing recorded, and most merged items have no parent at
// all — a line per merge would bury the rollups that matter.
func TestRollupSkipLineIsSilentForANonEpicParent(t *testing.T) {
	f := newRollupFixture(t, forge.StateOpen, [2]string{forge.StateClosed, forge.StateClosed})
	// Reparent both children onto a task.
	taskParent := 70
	for index := range f.in.Items {
		if f.in.Items[index].Kind == forge.KindTask {
			f.in.Items[index].Parent = &taskParent
		}
	}
	f.in.Items = append(f.in.Items, forge.Item{Number: taskParent, Kind: forge.KindTask, State: forge.StateOpen})

	report, err := RunTick(context.Background(), f.in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	for _, line := range report.Skipped {
		if strings.Contains(line, "rollup") {
			t.Fatalf("a non-epic parent produced a rollup line: %q", line)
		}
	}
}

func intPtr(n int) *int { return &n }

// testFullSHA builds a distinct 40-char sha per item number, so a fixture's
// verdict and change-request head name the same commit the way the merge gate
// compares them (the gate's matcher refuses a sha shorter than 7 chars).
func testFullSHA(n int) string {
	return fmt.Sprintf("%038x%d", n, n)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

var _ = rig.QueueItem{}

// TestRollupSkipsAnEpicWithNoChildrenInTheSnapshot is the snapshot guard itself,
// and it is the case a real partial read produces: the forge returned the epic
// but not its sub-issues. Without the guard the rule cannot tell "no children
// seen" from "all children closed", so the tick would close an epic whose work
// nobody looked at. This is the test the issue's acceptance names.
func TestRollupSkipsAnEpicWithNoChildrenInTheSnapshot(t *testing.T) {
	f := newRollupFixture(t, forge.StateOpen, [2]string{forge.StateClosed, forge.StateClosed})
	// Keep the epic, drop its children: the truncated read.
	// Keep the epic, drop everything under it: a forge read that returned the
	// parent but not its sub-issues. The merged children are absent too, which is
	// why the parent has to come from the mirror cache rather than the snapshot.
	f.in.Items = []forge.Item{{Number: 64, Kind: forge.KindEpic, State: forge.StateOpen, Title: "An epic"}}

	report, err := RunTick(context.Background(), f.in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	for _, write := range f.fake.Writes {
		if write.Number == 64 {
			t.Fatalf("closed an epic whose children were absent from the snapshot: %+v", write)
		}
	}
	if len(report.Epics) != 0 {
		t.Fatalf("report.Epics = %+v, want none", report.Epics)
	}
	want := "#64 rollup: no sub-issues in the snapshot"
	var found bool
	for _, line := range report.Skipped {
		if line == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("skipped = %v, want %q", report.Skipped, want)
	}
}
