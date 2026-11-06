package act

import (
	"context"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/decide"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// TestTickDoesNotRaiseForTheVerdictItJustCaptured reproduces #142 on the real
// pipeline order: the plan is built from a snapshot that holds no verdict, and
// the tick then captures the reviewer's ACCEPT from the change request's notes,
// which invalidates the plan's "no verdict" judgment.
//
// Before the guard the owner was paged for a decision the reviewer had already
// answered — twice in production on 2026-11-04 (#133, #138). After it, the raise
// is dropped and the skip line says why.
func TestTickDoesNotRaiseForTheVerdictItJustCaptured(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	st := newStore(t)
	pid := newProject(t, st)

	// The item has a dispatch row but no verdict, which is the production shape:
	// the row is ended or live, and the reviewer's answer exists only as a note.
	if _, err := st.CreateDispatch(ctx, store.NewDispatch{
		ProjectID: pid, ItemNumber: 5, Attempt: 1, Seat: "seat@rig",
		Branch: "feature/5-x", Worktree: "wt", BaseSHA: "base", CreatedAt: now,
	}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	// The reviewer's ACCEPT has landed as a note, and no verdict row exists yet:
	// exactly the state the plan reads as "no verdict".
	fake := forge.NewFake(forge.KindGitHub)
	fake.ItemsValue = []forge.Item{{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Five"}}
	fake.RequestsValue = []forge.ChangeRequest{{
		Number: 9, HeadSHA: "abc1234",
		Mergeable: true, Body: "Closes #5", DocsOnly: true,
	}}
	fake.NotesValue = []forge.Comment{{
		ID: "n1", Body: "ACCEPT abc1234", CreatedAt: "2026-10-29T11:00:00Z",
	}}

	// The plan is built the way the server builds it: from the pre-capture state.
	plan := decide.PlanTick(decide.Snapshot{
		Project: decide.Project{Key: "app", Forge: decide.ForgeGitHub, Enabled: true},
		Items:   []decide.Item{{Number: 5, Kind: decide.KindTask, State: decide.StateOpen, Title: "Five"}},
		CRs:     []decide.CRState{{Number: 9, Item: 5, State: "open", HeadSHA: "abc1234"}},
		ForgeOK: true,
	}, now)
	var raise *decide.Intent
	for index := range plan.Intents {
		if plan.Intents[index].Kind == decide.IntentRaiseDecision {
			raise = &plan.Intents[index]
		}
	}
	if raise == nil {
		t.Fatalf("plan = %+v, want the no-verdict raise the bug is about", plan)
	}
	if raise.Event.Kind != "decision.raise" {
		t.Fatalf("raise kind = %q, want decision.raise", raise.Event.Kind)
	}

	queue := &queueStub{item: &rig.QueueItem{ID: "qitem-should-not-exist", State: "pending"}}
	project := store.ProjectRow{
		ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", Enabled: true,
	}
	in := TickInput{
		Store: st, Project: project, Forge: fake, Rig: queue, Git: &fakeGit{},
		Gate: openGate(), Apply: true, Items: fake.ItemsValue, CRs: fake.RequestsValue,
		Plan: plan, WorktreeRoot: t.TempDir(), Now: func() time.Time { return now },
	}

	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Verdicts) != 1 {
		t.Fatalf("verdicts captured = %d, want 1 (the note the plan could not see)", len(report.Verdicts))
	}
	if len(report.Decisions) != 0 {
		t.Fatalf("decisions = %v, want none: the tick captured the verdict the plan called missing", report.Decisions)
	}
	if queue.createCalls != 0 {
		t.Fatalf("queue creates = %d, want 0: no row for a decision that was already answered", queue.createCalls)
	}
	if !hasSkip(report, "decision skipped: this tick captured the verdict") {
		t.Fatalf("skipped = %v, want the skip line naming why", report.Skipped)
	}

	// No decision event reached the ledger, and no telegram message could follow.
	events, err := st.RecentEvents(ctx, 50)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	for _, event := range events {
		if event.Kind == "decision.raise" {
			t.Fatalf("event %v: a decision.raise reached the ledger", event)
		}
	}
}

// TestTickStillRaisesWhenNoVerdictArrives is the other arm: the same plan, but
// the notes carry no verdict. Nothing is captured, so the raise must still go
// out — the guard suppresses only what this tick actually resolved.
func TestTickStillRaisesWhenNoVerdictArrives(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	st := newStore(t)
	pid := newProject(t, st)

	fake := forge.NewFake(forge.KindGitHub)
	fake.ItemsValue = []forge.Item{{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Five"}}
	fake.RequestsValue = []forge.ChangeRequest{{
		Number: 9, HeadSHA: "abc1234",
		Mergeable: true, Body: "Closes #5", DocsOnly: true,
	}}
	// No NotesValue: the reviewer has not spoken.

	plan := decide.PlanTick(decide.Snapshot{
		Project: decide.Project{Key: "app", Forge: decide.ForgeGitHub, Enabled: true},
		Items:   []decide.Item{{Number: 5, Kind: decide.KindTask, State: decide.StateOpen, Title: "Five"}},
		CRs:     []decide.CRState{{Number: 9, Item: 5, State: "open", HeadSHA: "abc1234"}},
		ForgeOK: true,
	}, now)

	queue := &queueStub{item: &rig.QueueItem{ID: "qitem-review-1", State: "pending"}}
	project := store.ProjectRow{
		ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", Enabled: true,
	}
	in := TickInput{
		Store: st, Project: project, Forge: fake, Rig: queue, Git: &fakeGit{},
		Gate: openGate(), Apply: true, Items: fake.ItemsValue, CRs: fake.RequestsValue,
		Plan: plan, WorktreeRoot: t.TempDir(), Now: func() time.Time { return now },
	}

	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Verdicts) != 0 {
		t.Fatalf("verdicts captured = %d, want 0", len(report.Verdicts))
	}
	if len(report.Decisions) != 1 {
		t.Fatalf("decisions = %v, want the raise: nobody answered this one", report.Decisions)
	}
	if queue.createCalls != 1 {
		t.Fatalf("queue creates = %d, want 1", queue.createCalls)
	}
}
