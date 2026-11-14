package act

import (
	"context"
	"errors"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// branchEvents reads the branch warnings on the ledger for one item.
func branchEvents(t *testing.T, ctx context.Context, st *store.Store, item int) []store.EventRow {
	t.Helper()
	events, err := st.EventsForItem(ctx, item, 50)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	var notes []store.EventRow
	for _, event := range events {
		if event.Kind == "change_request.branch" {
			notes = append(notes, event)
		}
	}
	return notes
}

// TestAdoptedMergeAcceptsABranchWithNoItem: the item comes from the body, so a
// branch that names no item is a naming sloppiness, not a reason to hold the
// merge: it proceeds and earns one line naming the convention
// (openspec/changes/adopt-on-the-body, D1/D3).
func TestAdoptedMergeAcceptsABranchWithNoItem(t *testing.T) {
	ctx, st, _, _, fake, in := adoptedFixture(t, "abc1234")
	fake.RequestsValue[0].Branch = "fix/delete-project-verdicts-by-project-id"
	in.CRs = fake.RequestsValue

	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Merges) != 1 || !report.Merges[0].Merged {
		t.Fatalf("report = %+v, want one adopted merge; skipped = %v", report, report.Skipped)
	}
	if !hasWrite(fake.Writes, "merge", 9, "abc1234") {
		t.Fatalf("writes = %+v, want the merge at the verdict sha", fake.Writes)
	}
	notes := branchEvents(t, ctx, st, 5)
	if len(notes) != 1 {
		t.Fatalf("branch warnings = %+v, want exactly one", notes)
	}
	detail := notes[0].Detail
	if !containsAll(detail, "fix/delete-project-verdicts-by-project-id", "names no item", "feature/5-") {
		t.Fatalf("detail = %q, want the branch, the missing item and the convention named", detail)
	}
}

// TestAdoptedMergeAcceptsFixPrefixedBranch: a `fix/<item>-` branch names the
// item, so the check agrees with the body and there is nothing to warn about.
func TestAdoptedMergeAcceptsFixPrefixedBranch(t *testing.T) {
	ctx, st, _, _, fake, in := adoptedFixture(t, "abc1234")
	fake.RequestsValue[0].Branch = "fix/5-clean-up"
	in.CRs = fake.RequestsValue

	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Merges) != 1 || !report.Merges[0].Merged {
		t.Fatalf("report = %+v, want one adopted merge; skipped = %v", report, report.Skipped)
	}
	if notes := branchEvents(t, ctx, st, 5); len(notes) != 0 {
		t.Fatalf("branch warnings = %+v, want none for a branch that names the item", notes)
	}
}

// TestAdoptedMergeFeatureBranchUnchanged: the app's own convention is treated
// exactly as it was — no warning, no extra refusal.
func TestAdoptedMergeFeatureBranchUnchanged(t *testing.T) {
	ctx, st, _, _, _, in := adoptedFixture(t, "abc1234")

	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Merges) != 1 || !report.Merges[0].Merged {
		t.Fatalf("report = %+v, want one adopted merge; skipped = %v", report, report.Skipped)
	}
	if notes := branchEvents(t, ctx, st, 5); len(notes) != 0 {
		t.Fatalf("branch warnings = %+v, want none for feature/5-", notes)
	}
}

// TestAdoptedMergeRefusesABranchNamingAnotherItem: a branch naming a different
// *open* item is the shape that must still refuse — two live items would claim
// one branch — and the error names both numbers (D2/D4).
func TestAdoptedMergeRefusesABranchNamingAnotherItem(t *testing.T) {
	ctx, st, _, _, fake, in := adoptedFixture(t, "abc1234")
	fake.ItemsValue = append(fake.ItemsValue,
		forge.Item{Number: 438, Kind: forge.KindTask, State: forge.StateOpen, Title: "Old"})
	fake.RequestsValue[0].Branch = "fix/438-follow-up"
	in.CRs = fake.RequestsValue

	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Merges) != 0 {
		t.Fatalf("merged a branch naming another open item: %+v", report.Merges)
	}
	if !hasSkip(report, "closes #5 but is on fix/438-follow-up, which names #438") {
		t.Fatalf("skipped = %v, want both numbers named", report.Skipped)
	}
	if hasWrite(fake.Writes, "merge", 9, "abc1234") {
		t.Fatalf("merge written for the wrong branch: %+v", fake.Writes)
	}
	if notes := branchEvents(t, ctx, st, 5); len(notes) != 0 {
		t.Fatalf("branch warnings = %+v, want none: a refusal is the line", notes)
	}
}

// TestAdoptedMergeAcceptsABranchNamingAClosedItem: the work was replayed on a
// fresh item and the branch kept its name, so the mismatch is a re-home — it
// merges, and the warning says which items are involved and that it was re-homed
// (#454).
//
// #438 is deliberately *absent* from the item list, the way both real adapters
// behave: they ask for open items only, so a closed item never appears in Items.
// The state comes from ItemState, which is the read that can see it (#464). A
// fixture that leaves the closed item in ItemsValue would pass while the re-home
// arm was unreachable in production.
func TestAdoptedMergeAcceptsABranchNamingAClosedItem(t *testing.T) {
	ctx, st, _, _, fake, in := adoptedFixture(t, "abc1234")
	fake.ItemStateValue = map[int]string{438: forge.StateClosed}
	fake.RequestsValue[0].Branch = "fix/438-follow-up"
	in.CRs = fake.RequestsValue

	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Merges) != 1 || !report.Merges[0].Merged {
		t.Fatalf("report = %+v, want one adopted merge; skipped = %v", report, report.Skipped)
	}
	notes := branchEvents(t, ctx, st, 5)
	if len(notes) != 1 {
		t.Fatalf("branch warnings = %+v, want exactly one", notes)
	}
	detail := notes[0].Detail
	if !containsAll(detail, "fix/438-follow-up", "names #438 (closed)", "re-homed to #5") {
		t.Fatalf("detail = %q, want the branch, the closed item and the re-home named", detail)
	}
}

// TestBranchWarningIsOncePerItemAndHead is task 2.2: a change request that waits
// is one line, not one per tick, and a new head is worth a second line — the key
// is (item, head) through the same ledger every other raise uses.
func TestBranchWarningIsOncePerItemAndHead(t *testing.T) {
	// No verdict, so every tick reaches the branch check and refuses after it:
	// that is the "sits unmerged for several ticks" shape.
	ctx, st, _, _, fake, in := adoptedFixture(t, "")
	fake.RequestsValue[0].Branch = "fix/delete-project-verdicts-by-project-id"
	in.CRs = fake.RequestsValue

	for range 3 {
		if _, err := RunTick(ctx, in); err != nil {
			t.Fatalf("tick: %v", err)
		}
	}
	if notes := branchEvents(t, ctx, st, 5); len(notes) != 1 {
		t.Fatalf("branch warnings after three ticks = %d, want 1", len(notes))
	}

	fake.RequestsValue[0].HeadSHA = "beef456"
	in.CRs = fake.RequestsValue
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if notes := branchEvents(t, ctx, st, 5); len(notes) != 2 {
		t.Fatalf("branch warnings after a new head = %d, want 2", len(notes))
	}
}

// TestAdoptedMergeRefusesABranchNamingAnItemTheForgeCannotRead: a number nobody
// has is not a re-home. Items is the open list, so a closed item and a typo are
// equally absent from it — the only way to tell them apart is a read that answers
// for a closed item, and when that read fails the merge refuses rather than guess
// (D4).
func TestAdoptedMergeRefusesABranchNamingAnItemTheForgeCannotRead(t *testing.T) {
	ctx, st, _, _, fake, in := adoptedFixture(t, "abc1234")
	fake.ItemStateErr = errors.New("forge: item #438 was not found")
	fake.RequestsValue[0].Branch = "fix/438-follow-up"
	in.CRs = fake.RequestsValue

	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Merges) != 0 {
		t.Fatalf("merged on a branch whose item could not be read: %+v", report.Merges)
	}
	if !hasSkip(report, "which names #438, whose state cannot be read") {
		t.Fatalf("skipped = %v, want the unreadable item named", report.Skipped)
	}
	if hasWrite(fake.Writes, "merge", 9, "abc1234") {
		t.Fatalf("merge written: %+v", fake.Writes)
	}
	if notes := branchEvents(t, ctx, st, 5); len(notes) != 0 {
		t.Fatalf("branch warnings = %+v, want none: a refusal is the line", notes)
	}
}

// TestAdoptedMergeStillRefusesLiveDispatchOnAnotherBranch pins the refusal the
// change set deliberately leaves alone (task 3.5): a live dispatch on another
// branch still wins over the body, because two branches close one item.
func TestAdoptedMergeStillRefusesLiveDispatchOnAnotherBranch(t *testing.T) {
	ctx, st, pid, _, fake, in := adoptedFixture(t, "abc1234")
	fake.RequestsValue[0].Branch = "fix/delete-project-verdicts-by-project-id"
	in.CRs = fake.RequestsValue
	if _, err := st.CreateDispatch(ctx, store.NewDispatch{
		ProjectID: pid, ItemNumber: 5, Attempt: 1, Seat: "seat@rig",
		Branch: "feature/5-app", Worktree: "wt", BaseSHA: "base",
	}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Merges) != 0 {
		t.Fatalf("adopted over a live dispatch: %+v", report.Merges)
	}
	if !hasSkip(report, "live dispatch on feature/5-app") {
		t.Fatalf("skipped = %v, want the live branch named", report.Skipped)
	}
	if hasWrite(fake.Writes, "merge", 9, "abc1234") {
		t.Fatalf("merged with two branches open: %+v", fake.Writes)
	}
}
