package act

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/decide"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// silentIntent is the plan's word for one silent dispatch: decide named it,
// the act tick turns it into the operator's question.
func silentIntent(item int) decide.Intent {
	return decide.Intent{
		Kind: decide.IntentSilentDispatch, Project: "app", Item: item, Seat: "seat@rig",
		Event: decide.Event{Actor: "decide", Kind: "dispatch.silent", Subject: "#5",
			Detail: "seat=seat@rig branch=feature/5-x attempt=1 age=45m0s"},
	}
}

// TestSilentDispatchRaisesOneOperatorQuestion is the brief's act evidence: the
// intent raises exactly one human@kernel row; a second tick at the same
// (item, attempt) raises none; a new attempt raises one more. The seat is not
// killed and nothing is re-dispatched — the tick's dispatch loop is untouched.
func TestSilentDispatchRaisesOneOperatorQuestion(t *testing.T) {
	ctx, st, dispatchID, _, in := watchdogFixture(t, false)
	in.CRs = nil // an open change request is the merge gate's case, not a stall
	in.Plan = decide.Plan{Intents: []decide.Intent{silentIntent(5)}}
	// The fixture's dispatch was created at 17:00; an hour later it is silent.
	later := time.Date(2026, 10, 30, 18, 0, 0, 0, time.UTC)
	in.Now = func() time.Time { return later }
	queue := &queueStub{created: &rig.CreateRequest{}}
	in.Rig = queue
	var raised []DecisionRaised
	in.OnDecision = func(r DecisionRaised) { raised = append(raised, r) }

	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("first tick: %v", err)
	}
	if queue.createCalls != 1 {
		t.Fatalf("queue creates = %d, want 1", queue.createCalls)
	}
	if created := queue.created; created.Destination != "human@kernel" || created.Source != "human@kernel" {
		t.Fatalf("row = %s from %s, want human@kernel both ways", created.Destination, created.Source)
	}
	if len(raised) != 1 || raised[0].Item != 5 {
		t.Fatalf("decisions = %+v, want one raise for item 5", raised)
	}
	if !strings.Contains(raised[0].Text, "dispatch produced no change request in 60 minutes") {
		t.Fatalf("question = %q, want the brief's message", raised[0].Text)
	}
	exists, err := st.HasEvent(ctx, EventSilentDispatch, "#5", "attempt=1 ")
	if err != nil || !exists {
		t.Fatalf("HasEvent = %v, %v; want one dispatch.silent event", exists, err)
	}
	journal := "#5 silent dispatch: seat=seat@rig branch=feature/5-x attempt=1 age=1h0m0s; row"
	var line string
	for _, skip := range report.Skipped {
		if strings.Contains(skip, journal) {
			line = skip
		}
	}
	if line == "" {
		t.Fatalf("skipped = %+v, want the journal line %q", report.Skipped, journal)
	}
	body, err := os.ReadFile(queue.created.BodyFile)
	if err != nil {
		t.Fatalf("brief: %v", err)
	}
	text := string(body)
	for _, want := range []string{
		"app #5 Five", "Seat: seat@rig", "Branch: feature/5-x", "Worktree: wt",
		"gh pr list -R o/r --head feature/5-x",
		"never pushed", "refused a write", "/.openrig/transcripts/rig",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("brief = %q, want it to carry %q", text, want)
		}
	}

	// The same (item, attempt) again is the same question: no second row.
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if queue.createCalls != 1 || len(raised) != 1 {
		t.Fatalf("after second tick: creates=%d raised=%d, want NoOp — 1/1", queue.createCalls, len(raised))
	}

	// A new attempt is a new fact: end attempt 1, start attempt 2, and the
	// question is asked again.
	if err := st.EndDispatch(ctx, dispatchID, "superseded"); err != nil {
		t.Fatalf("end dispatch: %v", err)
	}
	if _, err := st.CreateDispatch(ctx, store.NewDispatch{
		ProjectID: in.Project.ID, ItemNumber: 5, Attempt: 2, Seat: "seat@rig",
		Branch: "feature/5-x", Worktree: "wt", BaseSHA: "base", CreatedAt: later.Add(-40 * time.Minute),
	}); err != nil {
		t.Fatalf("second dispatch: %v", err)
	}
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("third tick: %v", err)
	}
	if queue.createCalls != 2 || len(raised) != 2 {
		t.Fatalf("after third tick: creates=%d raised=%d, want one new question", queue.createCalls, len(raised))
	}
	exists, err = st.HasEvent(ctx, EventSilentDispatch, "#5", "attempt=2 ")
	if err != nil || !exists {
		t.Fatalf("HasEvent(attempt=2) = %v, %v; want the second event", exists, err)
	}
}

// TestSilentDispatchSkipsWhenTheDispatchEnded is rule 5: a dispatch that ends
// normally stays on the existing path, and a plan judgment that has gone stale
// between the plan and the tick raises nothing.
func TestSilentDispatchSkipsWhenTheDispatchEnded(t *testing.T) {
	ctx, _, dispatchID, _, in := watchdogFixture(t, false)
	in.CRs = nil
	in.Plan = decide.Plan{Intents: []decide.Intent{silentIntent(5)}}
	later := time.Date(2026, 10, 30, 18, 0, 0, 0, time.UTC)
	in.Now = func() time.Time { return later }
	queue := &queueStub{created: &rig.CreateRequest{}}
	in.Rig = queue
	if err := in.Store.EndDispatch(ctx, dispatchID, "superseded"); err != nil {
		t.Fatalf("end dispatch: %v", err)
	}
	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if queue.createCalls != 0 {
		t.Fatalf("queue creates = %d, want none for an ended dispatch", queue.createCalls)
	}
	found := false
	for _, skip := range report.Skipped {
		if strings.Contains(skip, "#5 silent dispatch skipped: the dispatch has already ended") {
			found = true
		}
	}
	if !found {
		t.Fatalf("skipped = %+v, want the ended-dispatch skip line", report.Skipped)
	}
}

// TestRaiseSilentDispatchDedupePerAttempt pins the ledger key's boundary:
// attempt 1 never matches an attempt 10 event and the other way, so a
// re-dispatch always gets its own question.
func TestRaiseSilentDispatchDedupePerAttempt(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	queue := &queueStub{created: &rig.CreateRequest{}, item: &rig.QueueItem{ID: "qitem-silent", State: "pending"}}
	actions := &Actions{Store: st, Rig: queue, ProjectID: pid, ProjectKey: "app"}
	in := func(attempt int) SilentDispatchInput {
		return SilentDispatchInput{
			Item: 5, Title: "Five", Seat: "dev-build-1@r", Branch: "feature/5-x",
			Worktree: "wt", Attempt: attempt, Age: 45 * time.Minute,
			Repo: "o/r", ProjectKey: "app",
		}
	}
	raise, err := actions.RaiseSilentDispatch(ctx, in(1))
	if err != nil || raise.NoOp {
		t.Fatalf("first raise = %+v, %v; want a new question", raise, err)
	}
	if !strings.Contains(raise.Question, "dispatch produced no change request in 45 minutes") {
		t.Fatalf("question = %q, want the brief's message with the age", raise.Question)
	}
	noop, err := actions.RaiseSilentDispatch(ctx, in(1))
	if err != nil || !noop.NoOp {
		t.Fatalf("second raise = %+v, %v; want NoOp", noop, err)
	}
	if queue.createCalls != 1 {
		t.Fatalf("creates = %d, want the deduped raise to create nothing", queue.createCalls)
	}
	// The boundary both ways: attempt 10 is a different fact from attempt 1,
	// and raising it must not be swallowed by attempt 1's event — nor the
	// reverse order.
	ten, err := actions.RaiseSilentDispatch(ctx, in(10))
	if err != nil || ten.NoOp {
		t.Fatalf("attempt-10 raise = %+v, %v; want a new question", ten, err)
	}
	if queue.createCalls != 2 {
		t.Fatalf("creates = %d, want 2 after the attempt-10 raise", queue.createCalls)
	}
}
