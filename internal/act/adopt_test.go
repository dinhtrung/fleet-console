package act

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// adoptedFixture is the reported failure: an item the app never dispatched (no
// dispatch row), a pull request on the app's own branch convention that says
// Closes #<item>, green checks, and an ACCEPT at the head.
//
// Nothing here creates a dispatch row — that is the point of the case.
func adoptedFixture(t *testing.T, acceptSHA string) (context.Context, *store.Store, int64, store.ProjectRow, *forge.Fake, TickInput) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	st := newStore(t)
	pid := newProject(t, st)

	fake := forge.NewFake(forge.KindGitHub)
	fake.ItemsValue = []forge.Item{{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Five"}}
	fake.RequestsValue = []forge.ChangeRequest{{
		Number: 9, HeadSHA: "abc1234", Branch: "feature/5-hand-dispatch",
		Mergeable: true, Body: "Closes #5", DocsOnly: true,
	}}
	if acceptSHA != "" {
		fake.NotesValue = []forge.Comment{{ID: "n1", Body: "ACCEPT " + acceptSHA, CreatedAt: "2026-10-29T11:00:00Z"}}
	}
	project := store.ProjectRow{
		ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r",
		Rig: "rig", ConcurrencyCap: 2, Enabled: true,
	}
	in := TickInput{
		Store: st, Project: project, Forge: fake, Rig: &queueStub{}, Git: &fakeGit{},
		Gate: openGate(), Apply: true, Items: fake.ItemsValue, CRs: fake.RequestsValue,
		Now: func() time.Time { return now },
	}
	return ctx, st, pid, project, fake, in
}

// TestAdoptMergesAChangeRequestWithNoDispatchRow is the acceptance for #138: the
// pull request the app never dispatched still merges on the reviewer's verdict,
// the item closes, both events are written, and no dispatch row appears.
func TestAdoptMergesAChangeRequestWithNoDispatchRow(t *testing.T) {
	ctx, st, pid, _, fake, in := adoptedFixture(t, "abc1234")

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

	// The advertised behaviour is gone: no "closes no dispatch" line.
	for _, skipped := range report.Skipped {
		if strings.Contains(skipped, "closes no dispatch") {
			t.Fatalf("skip still says the old thing: %q", skipped)
		}
	}

	// Both events, in order: adopted, then merge.
	events, err := st.RecentEvents(ctx, 50)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	adoptedAt, mergeAt := -1, -1
	for index, event := range events {
		switch event.Kind {
		case "change_request.adopted":
			adoptedAt = index
		case "merge":
			mergeAt = index
		}
	}
	if adoptedAt < 0 || mergeAt < 0 {
		t.Fatalf("events = %v, want change_request.adopted and merge", eventKinds(events))
	}
	// RecentEvents is newest first, so the merge is written after the adoption.
	if mergeAt > adoptedAt {
		t.Fatalf("merge written before change_request.adopted: %v", eventKinds(events))
	}

	// No synthetic dispatch row: the table must stay empty for this item.
	rows, err := st.Dispatches(ctx, pid, 5)
	if err != nil {
		t.Fatalf("dispatches: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("dispatch rows = %+v, want none (design D2)", rows)
	}
}

// TestAdoptRefusesWithoutAVerdict: no ACCEPT comment means nothing merges and the
// skip line names the missing verdict.
func TestAdoptRefusesWithoutAVerdict(t *testing.T) {
	ctx, _, _, _, fake, in := adoptedFixture(t, "")

	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Merges) != 0 {
		t.Fatalf("merged without a verdict: %+v", report.Merges)
	}
	if !hasSkip(report, "no ACCEPT verdict for #5") {
		t.Fatalf("skipped = %v, want the missing verdict named", report.Skipped)
	}
	if hasWrite(fake.Writes, "merge", 9, "abc1234") {
		t.Fatalf("merge written without a verdict: %+v", fake.Writes)
	}
}

// TestAdoptRefusesAVerdictAtAnotherSha: an ACCEPT for a superseded head says
// nothing about this one, so nothing merges. The capture stores no row either —
// an accept at another sha is not this head's verdict — so the refusal names the
// absent verdict, not a sha mismatch.
func TestAdoptRefusesAVerdictAtAnotherSha(t *testing.T) {
	ctx, st, _, _, fake, in := adoptedFixture(t, "deadbee")

	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Merges) != 0 {
		t.Fatalf("merged on a stale verdict: %+v", report.Merges)
	}
	if !hasSkip(report, "no ACCEPT verdict for #5") {
		t.Fatalf("skipped = %v, want the missing verdict named", report.Skipped)
	}
	if hasWrite(fake.Writes, "merge", 9, "deadbee") {
		t.Fatalf("merged at the stale sha: %+v", fake.Writes)
	}
	// No verdict is stored for this head, so a later merge cannot find one.
	verdicts, err := st.VerdictsForItem(ctx, in.Project.ID, 5)
	if err != nil {
		t.Fatalf("verdicts: %v", err)
	}
	if len(verdicts) != 0 {
		t.Fatalf("verdict rows = %+v, want none for a superseded head", verdicts)
	}
}

// TestAdoptRefusesABranchThatCannotBelong: the branch convention is the cheap
// way to reject a pull request that merely mentions the item.
func TestAdoptRefusesABranchThatCannotBelong(t *testing.T) {
	ctx, _, _, _, fake, in := adoptedFixture(t, "abc1234")
	fake.RequestsValue[0].Branch = "hotfix/unrelated"
	in.CRs = fake.RequestsValue

	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Merges) != 0 {
		t.Fatalf("merged a branch that cannot belong: %+v", report.Merges)
	}
	if !hasSkip(report, "does not start with feature/5-") {
		t.Fatalf("skipped = %v, want the branch prefix named", report.Skipped)
	}
	if !hasSkip(report, "hotfix/unrelated") {
		t.Fatalf("skipped = %v, want the offending branch named", report.Skipped)
	}
	if hasWrite(fake.Writes, "merge", 9, "abc1234") {
		t.Fatalf("merge written for the wrong branch: %+v", fake.Writes)
	}
}

// TestAdoptRefusesWhileALiveDispatchIsOnAnotherBranch: two branches closing one
// item is a human decision, so adoption names both and picks neither.
func TestAdoptRefusesWhileALiveDispatchIsOnAnotherBranch(t *testing.T) {
	ctx, st, pid, _, fake, in := adoptedFixture(t, "abc1234")
	if _, err := st.CreateDispatch(ctx, store.NewDispatch{
		ProjectID: pid, ItemNumber: 5, Attempt: 1, Seat: "seat@rig",
		Branch: "feature/5-app", Worktree: "wt", BaseSHA: "base",
		CreatedAt: time.Date(2026, 10, 29, 11, 0, 0, 0, time.UTC),
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
	if !hasSkip(report, "feature/5-hand-dispatch") {
		t.Fatalf("skipped = %v, want the change request's branch named too", report.Skipped)
	}
	if hasWrite(fake.Writes, "merge", 9, "abc1234") {
		t.Fatalf("merged with two branches open: %+v", fake.Writes)
	}
}

// TestVerdictIsStoredWithNoDispatch is task 1.3: the verdict is a fact about the
// change request, so it is recorded even when nothing can merge yet.
func TestVerdictIsStoredWithNoDispatch(t *testing.T) {
	ctx, st, _, _, fake, in := adoptedFixture(t, "abc1234")
	// A branch that cannot belong refuses the merge, so only the capture can
	// explain a verdict row existing afterwards.
	fake.RequestsValue[0].Branch = "hotfix/unrelated"
	in.CRs = fake.RequestsValue

	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Verdicts) != 1 {
		t.Fatalf("verdicts = %+v, want the ACCEPT captured despite the refusal", report.Verdicts)
	}
	verdicts, err := st.VerdictsForItem(ctx, in.Project.ID, 5)
	if err != nil {
		t.Fatalf("verdicts: %v", err)
	}
	if len(verdicts) != 1 || verdicts[0].Decision != "accept" || verdicts[0].SHA != "abc1234" {
		t.Fatalf("stored verdicts = %+v, want one accept at abc1234", verdicts)
	}
	if verdicts[0].DispatchID != 0 {
		t.Fatalf("dispatch id = %d, want 0 for an adopted change request", verdicts[0].DispatchID)
	}
}

// TestAdoptedVerdictIsNotStoredTwice: a tick re-reads the same notes every time,
// so the capture must be idempotent by item when there is no dispatch.
func TestAdoptedVerdictIsNotStoredTwice(t *testing.T) {
	ctx, st, _, _, fake, in := adoptedFixture(t, "abc1234")
	fake.RequestsValue[0].Branch = "hotfix/unrelated"
	in.CRs = fake.RequestsValue

	for range 2 {
		if _, err := RunTick(ctx, in); err != nil {
			t.Fatalf("tick: %v", err)
		}
	}
	verdicts, err := st.VerdictsForItem(ctx, in.Project.ID, 5)
	if err != nil {
		t.Fatalf("verdicts: %v", err)
	}
	if len(verdicts) != 1 {
		t.Fatalf("verdict rows = %d, want 1 (capture is idempotent)", len(verdicts))
	}
}

// TestAdoptRefusesWhenTheHeadMovedAfterTheAccept is the stale-verdict case that
// matters: the reviewer accepted abc1234, then the branch moved to a new head.
// The stored verdict names the old sha, so adoption must refuse — an ACCEPT for a
// superseded head says nothing about the new one.
func TestAdoptRefusesWhenTheHeadMovedAfterTheAccept(t *testing.T) {
	ctx, _, _, _, fake, in := adoptedFixture(t, "abc1234")

	// Tick one: the accepted head is the current head, so the merge lands.
	first, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("first tick: %v", err)
	}
	if len(first.Merges) != 1 {
		t.Fatalf("first tick = %+v, want one merge", first)
	}

	// The same item again, now at a new head, with the old ACCEPT still in the
	// notes. The stored verdict is for the old sha, and the new head must not be
	// merged against it.
	fake.Writes = nil
	fake.RequestsValue[0].HeadSHA = "beef456"
	in.CRs = fake.RequestsValue
	in.Items = fake.ItemsValue

	second, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if len(second.Merges) != 0 {
		t.Fatalf("merged after the head moved: %+v", second.Merges)
	}
	if hasWrite(fake.Writes, "merge", 9, "beef456") {
		t.Fatalf("merged the new head on the old verdict: %+v", fake.Writes)
	}
}

func eventKinds(events []store.EventRow) []string {
	kinds := make([]string, 0, len(events))
	for _, event := range events {
		kinds = append(kinds, event.Kind)
	}
	return kinds
}
