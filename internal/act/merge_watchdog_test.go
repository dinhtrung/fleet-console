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
