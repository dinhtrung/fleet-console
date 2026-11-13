package act

import (
	"context"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// Three regressions that each stranded an accepted pull request: the watchdog keyed on a
// live dispatch, a draft nobody took out of draft, and a merge that refused to land after
// the builder had already ended its row.

func watchdogFixture(t *testing.T, dispatchEnded bool) (context.Context, *store.Store, int64, *forge.Fake, TickInput) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 10, 30, 17, 0, 0, 0, time.UTC)
	st := newStore(t)
	pid := newProject(t, st)
	dispatchID, err := st.CreateDispatch(ctx, store.NewDispatch{
		ProjectID: pid, ItemNumber: 5, Attempt: 1, Seat: "seat@rig",
		Branch: "feature/5-x", Worktree: "wt", BaseSHA: "base", CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if dispatchEnded {
		if err := st.EndDispatch(ctx, dispatchID, "superseded"); err != nil {
			t.Fatalf("end dispatch: %v", err)
		}
	}
	fake := forge.NewFake(forge.KindGitHub)
	fake.ItemsValue = []forge.Item{{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Five"}}
	fake.RequestsValue = []forge.ChangeRequest{{
		Number: 9, State: "open", HeadSHA: "abc1234", Mergeable: true, Body: "Closes #5", DocsOnly: true,
	}}
	fake.NotesValue = []forge.Comment{{ID: "n1", Body: "ACCEPT abc1234", CreatedAt: "2026-10-30T16:00:00Z"}}
	project := store.ProjectRow{ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", ConcurrencyCap: 2, Enabled: true}
	in := TickInput{
		Store: st, Project: project, Forge: fake, Rig: &queueStub{}, Git: &fakeGit{},
		Gate: openGate(), Apply: true, Items: fake.ItemsValue, CRs: fake.RequestsValue,
		Now: func() time.Time { return now },
	}
	return ctx, st, dispatchID, fake, in
}

func TestDraftChangeRequestIsMarkedReadyBeforeTheMerge(t *testing.T) {
	ctx, _, _, fake, in := watchdogFixture(t, false)
	fake.RequestsValue[0].Draft = true
	in.CRs = fake.RequestsValue

	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Merges) != 1 {
		t.Fatalf("report = %+v, want the draft merged after being marked ready", report)
	}
	readyAt, mergeAt := -1, -1
	for i, w := range fake.Writes {
		if w.Op == "ready" && w.Number == 9 && readyAt < 0 {
			readyAt = i
		}
		if w.Op == "merge" && w.Number == 9 && mergeAt < 0 {
			mergeAt = i
		}
	}
	if readyAt < 0 || mergeAt < 0 || readyAt > mergeAt {
		t.Fatalf("writes = %+v, want ready(9) before merge(9)", fake.Writes)
	}
}

func TestMergeLandsWhenTheBuilderAlreadyEndedItsRow(t *testing.T) {
	ctx, st, dispatchID, fake, in := watchdogFixture(t, true)

	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Merges) != 1 || !report.Merges[0].Merged {
		t.Fatalf("report = %+v, want the merge to land after the row ended", report)
	}
	if !hasWrite(fake.Writes, "merge", 9, "abc1234") {
		t.Fatalf("writes = %+v, want the merge at the verdict's sha", fake.Writes)
	}
	dispatch, err := st.Dispatch(ctx, dispatchID)
	if err != nil || dispatch == nil {
		t.Fatalf("dispatch = %+v, %v", dispatch, err)
	}
	if dispatch.EndedAt == nil || dispatch.EndReason != "superseded" {
		t.Fatalf("dispatch = %+v, want it left ended as superseded", dispatch)
	}
}

func TestMergeIgnoresAClosedChangeRequestThatClosesTheSameItem(t *testing.T) {
	ctx, _, _, fake, in := watchdogFixture(t, false)
	// A superseded pull request also says "Closes #5" and stays in the forge's list forever.
	fake.RequestsValue = []forge.ChangeRequest{
		{Number: 3, State: "closed", HeadSHA: "dead0000", Body: "Closes #5"},
		{Number: 9, State: "open", HeadSHA: "abc1234", Mergeable: true, Body: "Closes #5", DocsOnly: true},
	}
	in.CRs = fake.RequestsValue

	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Merges) != 1 {
		t.Fatalf("report = %+v, want one merge against the open change request", report)
	}
	if !hasWrite(fake.Writes, "merge", 9, "abc1234") {
		t.Fatalf("writes = %+v, want merge(9, abc1234) and never the closed #3", fake.Writes)
	}
	for _, w := range fake.Writes {
		if w.Op == "merge" && w.Number == 3 {
			t.Fatalf("merged the closed change request: %+v", fake.Writes)
		}
	}
}

// A dispatch that ended on an earlier attempt's branch is dead work. It must not
// keep an accepted change request on a new branch from merging (#386): the branch
// guard reads only dispatch rows that have not ended.
func branchGuardFixture(t *testing.T, requestBranch string, ended bool) (context.Context, TickInput, *forge.Fake) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 11, 13, 15, 30, 0, 0, time.UTC)
	st := newStore(t)
	pid := newProject(t, st)
	dispatchID, err := st.CreateDispatch(ctx, store.NewDispatch{
		ProjectID: pid, ItemNumber: 78, Attempt: 1, Seat: "seat@rig",
		Branch: "feature/78-old", Worktree: "wt", BaseSHA: "base", CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if ended {
		if err := st.EndDispatch(ctx, dispatchID, "superseded"); err != nil {
			t.Fatalf("end dispatch: %v", err)
		}
	}
	fake := forge.NewFake(forge.KindGitHub)
	fake.ItemsValue = []forge.Item{{Number: 78, Kind: forge.KindTask, State: forge.StateOpen, Title: "Seventy-eight"}}
	fake.RequestsValue = []forge.ChangeRequest{{
		Number: 83, State: "open", Branch: requestBranch, HeadSHA: "37fe8ac", Mergeable: true, Body: "Closes #78", DocsOnly: true,
	}}
	fake.NotesValue = []forge.Comment{{ID: "n1", Body: "ACCEPT 37fe8ac", CreatedAt: "2026-11-13T15:00:00Z"}}
	project := store.ProjectRow{ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", ConcurrencyCap: 2, Enabled: true}
	in := TickInput{
		Store: st, Project: project, Forge: fake, Rig: &queueStub{}, Git: &fakeGit{},
		Gate: openGate(), Apply: true, Items: fake.ItemsValue, CRs: fake.RequestsValue,
		Now: func() time.Time { return now },
	}
	return ctx, in, fake
}

// An ended dispatch on an old branch does not block the merge (#386).
func TestMergeLandsWhenTheOnlyDispatchOnAnotherBranchHasEnded(t *testing.T) {
	ctx, in, fake := branchGuardFixture(t, "feature/78-new", true)

	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Merges) != 1 || !report.Merges[0].Merged {
		t.Fatalf("report = %+v, want the merge to land after the old dispatch ended", report)
	}
	if !hasWrite(fake.Writes, "merge", 83, "37fe8ac") {
		t.Fatalf("writes = %+v, want merge(83, 37fe8ac)", fake.Writes)
	}
	if hasSkip(report, "refusing merge: live dispatch") {
		t.Fatalf("skips = %v, want no two-branch refusal", report.Skipped)
	}
}

// A live dispatch on another branch is still the two-branches-closed dead end,
// with today's message (#386).
func TestMergeRefusesWhenALiveDispatchIsOnAnotherBranch(t *testing.T) {
	ctx, in, fake := branchGuardFixture(t, "feature/78-new", false)

	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Merges) != 0 {
		t.Fatalf("report = %+v, want no merge while a live dispatch is on another branch", report)
	}
	if !hasSkip(report, "refusing merge: live dispatch on feature/78-old and change request #83 is on feature/78-new") {
		t.Fatalf("skips = %v, want today's two-branch refusal", report.Skipped)
	}
	if hasWrite(fake.Writes, "merge", 83, "37fe8ac") {
		t.Fatalf("writes = %+v, want no merge", fake.Writes)
	}
}
