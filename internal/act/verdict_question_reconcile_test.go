package act

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

const (
	questionHead = "abc1234def5678abc1234def5678abc1234def5"
	questionRow  = "qitem-question"
)

// seedQuestion writes the event act.RaiseVerdictQuestion would have written:
// the same text shape, read back by the reconcile.
func seedQuestion(t *testing.T, st *store.Store, project string, item, cr int, note string) {
	seedQuestionAt(t, st, project, item, cr, note, questionHead, questionRow)
}

func seedQuestionAt(t *testing.T, st *store.Store, project string, item, cr int, note, head, row string) {
	t.Helper()
	detail := fmt.Sprintf(
		"%s #%d: note %s on change request #%d cannot be read as a verdict (reads like a verdict but names no commit); asked human@kernel; note=%s; head=%s;; row %s",
		project, item, note, cr, note, head, row)
	if _, err := st.AppendEvent(context.Background(), store.Event{
		Actor: "act", Kind: "decision.verdict_question",
		Subject: fmt.Sprintf("#%d", item), Detail: detail,
	}); err != nil {
		t.Fatalf("question event: %v", err)
	}
}

// questionTick is the smallest tick that reaches the reconcile: one project, no
// open items, and the change requests the case asks about.
func questionTick(st *store.Store, pid int64, now time.Time, requests []forge.ChangeRequest, queue *queueStub) TickInput {
	fake := forge.NewFake(forge.KindGitHub)
	fake.RequestsValue = requests
	return TickInput{
		Store: st,
		Project: store.ProjectRow{
			ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", Enabled: true,
		},
		Forge: fake, Rig: queue, Git: &fakeGit{}, Gate: openGate(), Apply: true,
		CRs: requests,
		Now: func() time.Time { return now },
	}
}

// TestVerdictQuestionClosesWhenTheChangeRequestDoes (#376): a question whose
// change request has merged is resolved with a text naming the note and the
// ending — the reconcile arm, not a post-merge step, so it fires even though
// this tick merged nothing.
func TestVerdictQuestionClosesWhenTheChangeRequestDoes(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 13, 15, 0, 0, 0, time.UTC)
	st := newStore(t)
	pid := newProject(t, st)
	seedQuestion(t, st, "app", 5, 9, "n1")
	queue := &queueStub{item: &rig.QueueItem{ID: questionRow, State: "done"}, resolveRefusesPending: true, shows: map[string]*rig.QueueItem{questionRow: {ID: questionRow, State: "pending"}}}

	in := questionTick(st, pid, now, []forge.ChangeRequest{{Number: 9, State: "merged", Merged: true}}, queue)
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick: %v", err)
	}
	assertClosedOnce(t, queue, "change request #9 merged", "n1")
}

// TestVerdictQuestionClosesWhenAVerdictExists (#376): the head the question
// named now carries a verdict, so the answer exists even though the change
// request is still open.
func TestVerdictQuestionClosesWhenAVerdictExists(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 13, 15, 0, 0, 0, time.UTC)
	st := newStore(t)
	pid := newProject(t, st)
	seedQuestion(t, st, "app", 5, 9, "n1")
	if _, err := st.AddVerdict(ctx, store.NewVerdict{
		ProjectID: pid, ItemNumber: 5, SHA: questionHead, Decision: "accept", Source: "note n1", At: now,
	}); err != nil {
		t.Fatalf("verdict: %v", err)
	}
	queue := &queueStub{item: &rig.QueueItem{ID: questionRow, State: "done"}, resolveRefusesPending: true, shows: map[string]*rig.QueueItem{questionRow: {ID: questionRow, State: "pending"}}}

	in := questionTick(st, pid, now, []forge.ChangeRequest{{Number: 9, State: "open", HeadSHA: questionHead}}, queue)
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick: %v", err)
	}
	assertClosedOnce(t, queue, "verdict accept", "n1")
}

// TestVerdictQuestionStandsWhileUnanswered (#376): no verdict for the head and
// an open change request is a question still waiting — nothing is resolved.
func TestVerdictQuestionStandsWhileUnanswered(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 13, 15, 0, 0, 0, time.UTC)
	st := newStore(t)
	pid := newProject(t, st)
	seedQuestion(t, st, "app", 5, 9, "n1")
	queue := &queueStub{item: &rig.QueueItem{ID: questionRow, State: "done"}, resolveRefusesPending: true, shows: map[string]*rig.QueueItem{questionRow: {ID: questionRow, State: "pending"}}}

	in := questionTick(st, pid, now, []forge.ChangeRequest{{Number: 9, State: "open", HeadSHA: questionHead}}, queue)
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if queue.updateCalls != 0 {
		t.Fatalf("updates = %v, want none while the question stands", queue.updates)
	}
}

// TestVerdictQuestionResolveIsIdempotent (#376): the row's own state decides —
// the first tick closes it, the second finds it terminal and writes nothing.
func TestVerdictQuestionResolveIsIdempotent(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 13, 15, 0, 0, 0, time.UTC)
	st := newStore(t)
	pid := newProject(t, st)
	seedQuestion(t, st, "app", 5, 9, "n1")
	queue := &queueStub{item: &rig.QueueItem{ID: questionRow, State: "done"}, resolveRefusesPending: true, shows: map[string]*rig.QueueItem{questionRow: {ID: questionRow, State: "pending"}}}

	in := questionTick(st, pid, now, []forge.ChangeRequest{{Number: 9, State: "merged", Merged: true}}, queue)
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	if queue.updateCalls != 1 {
		t.Fatalf("updates = %+v, want the row closed once", queue.updates)
	}
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	if queue.updateCalls != 1 {
		t.Fatalf("updates = %+v, want the second tick to write nothing", queue.updates)
	}
}

// TestVerdictQuestionResolveLeavesATerminalRowAlone (#376): a row already
// closed before the reconcile sees it is not written again.
func TestVerdictQuestionResolveLeavesATerminalRowAlone(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 13, 15, 0, 0, 0, time.UTC)
	st := newStore(t)
	pid := newProject(t, st)
	seedQuestion(t, st, "app", 5, 9, "n1")
	queue := &queueStub{item: &rig.QueueItem{ID: questionRow, State: "done"}, shows: map[string]*rig.QueueItem{questionRow: {ID: questionRow, State: "done"}}}

	in := questionTick(st, pid, now, []forge.ChangeRequest{{Number: 9, State: "merged", Merged: true}}, queue)
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if queue.updateCalls != 0 {
		t.Fatalf("updates = %v, want none for a terminal row", queue.updates)
	}
}

// assertClosedOnce checks the row was closed the way a pending seat row is
// closed: one update to done, closure no-follow-on, the ending in the
// resolution — never `queue resolve`, which refuses this shape.
func assertClosedOnce(t *testing.T, queue *queueStub, wants ...string) {
	t.Helper()
	if len(queue.resolves) != 0 {
		t.Fatalf("resolves = %v, want none: a pending row is not resolvable", queue.resolves)
	}
	if len(queue.updates) != 1 {
		t.Fatalf("updates = %+v, want exactly one close", queue.updates)
	}
	update := queue.updates[0]
	if update.State != "done" || update.ClosureReason != "no-follow-on" {
		t.Fatalf("update = %+v, want done/no-follow-on", update)
	}
	for _, want := range wants {
		if !strings.Contains(update.Resolution, want) {
			t.Fatalf("resolution = %q, want it to name %q", update.Resolution, want)
		}
	}
}

// TestVerdictQuestionReconcileIsProjectScoped (#376): two projects can both
// have item #5, and the reconcile must only touch this project's question. The
// other project's row is named in the same tick's change requests, so a
// subject-only query would resolve it with this project's facts.
func TestVerdictQuestionReconcileIsProjectScoped(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 13, 15, 0, 0, 0, time.UTC)
	st := newStore(t)
	pid := newProject(t, st)
	if _, err := st.CreateProject(ctx, store.ProjectInput{
		Key: "other", Name: "other", ForgeKind: "github", ForgeProject: "o/other",
		Rig: "rig", ConcurrencyCap: 2, Enabled: true,
	}); err != nil {
		t.Fatalf("second project: %v", err)
	}
	seedQuestionAt(t, st, "app", 5, 9, "n1", questionHead, "qitem-app")
	seedQuestionAt(t, st, "other", 5, 9, "n2", questionHead, "qitem-other")
	queue := &queueStub{
		item: &rig.QueueItem{ID: "qitem-app", State: "done"}, resolveRefusesPending: true,
		shows: map[string]*rig.QueueItem{
			"qitem-app":   {ID: "qitem-app", State: "pending"},
			"qitem-other": {ID: "qitem-other", State: "pending"},
		},
	}

	in := questionTick(st, pid, now, []forge.ChangeRequest{{Number: 9, State: "merged", Merged: true}}, queue)
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick: %v", err)
	}
	assertClosedOnce(t, queue, "change request #9 merged", "n1")
}

// TestVerdictQuestionReconcileVisitsEveryQuestionOnAnItem (#376): two
// questions on one item (a moved head) are two rows; the one whose head now
// carries a verdict closes, the other keeps waiting.
func TestVerdictQuestionReconcileVisitsEveryQuestionOnAnItem(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 13, 15, 0, 0, 0, time.UTC)
	st := newStore(t)
	pid := newProject(t, st)
	const otherHead = "def4567abc1234def4567abc1234def4567abc1"
	seedQuestionAt(t, st, "app", 5, 9, "n1", questionHead, "qitem-answered")
	seedQuestionAt(t, st, "app", 5, 9, "n2", otherHead, "qitem-waiting")
	if _, err := st.AddVerdict(ctx, store.NewVerdict{
		ProjectID: pid, ItemNumber: 5, SHA: questionHead, Decision: "accept", Source: "note n1", At: now,
	}); err != nil {
		t.Fatalf("verdict: %v", err)
	}
	queue := &queueStub{
		item: &rig.QueueItem{ID: "qitem-answered", State: "done"}, resolveRefusesPending: true,
		shows: map[string]*rig.QueueItem{
			"qitem-answered": {ID: "qitem-answered", State: "pending"},
			"qitem-waiting":  {ID: "qitem-waiting", State: "pending"},
		},
	}

	in := questionTick(st, pid, now, []forge.ChangeRequest{{Number: 9, State: "open", HeadSHA: otherHead}}, queue)
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick: %v", err)
	}
	assertClosedOnce(t, queue, "verdict accept", "n1")
}

// laterHead is the commit the reviewer moved on to in the #414 cases: the
// question was asked about questionHead, and this is what the change request
// carries now.
const laterHead = "def4567abc1234def4567abc1234def4567abc1"

// supersededFixture is the #414 shape: a standing question about questionHead,
// and a verdict on laterHead, which is the head the change request carries.
func supersededFixture(t *testing.T, decision string) (*queueStub, TickInput) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	st := newStore(t)
	pid := newProject(t, st)
	seedQuestionAt(t, st, "app", 5, 9, "n1", questionHead, questionRow)
	if _, err := st.AddVerdict(ctx, store.NewVerdict{
		ProjectID: pid, ItemNumber: 5, SHA: laterHead, Decision: decision, Source: "note n9", At: now,
	}); err != nil {
		t.Fatalf("verdict: %v", err)
	}
	queue := &queueStub{
		item: &rig.QueueItem{ID: questionRow, State: "done"}, resolveRefusesPending: true,
		shows: map[string]*rig.QueueItem{questionRow: {ID: questionRow, State: "pending"}},
	}
	return queue, questionTick(st, pid, now, []forge.ChangeRequest{{Number: 9, State: "open", HeadSHA: laterHead}}, queue)
}

// TestVerdictQuestionClosesWhenALaterHeadCarriesAVerdict (#414): the question
// asked about a commit the branch no longer carries, and the reviewer posted a
// verdict on the head it does. Nobody is going to answer the old question, so
// the row closes superseded — the operator closed six of these by hand on
// 2026-11-13. The resolution names the verdict and the head, so the row still
// explains itself a month later.
func TestVerdictQuestionClosesWhenALaterHeadCarriesAVerdict(t *testing.T) {
	ctx := context.Background()
	queue, in := supersededFixture(t, "reject")
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(queue.updates) != 1 {
		t.Fatalf("updates = %+v, want the superseded question closed once", queue.updates)
	}
	if len(queue.resolves) != 0 {
		t.Fatalf("resolves = %v, want none: a pending row is not resolvable", queue.resolves)
	}
	update := queue.updates[0]
	if update.State != "canceled" || update.ClosureReason != "superseded" {
		t.Fatalf("update = %+v, want canceled/superseded: the row was overtaken, not answered", update)
	}
	if update.ClosureTarget != laterHead {
		t.Fatalf("closure target = %q, want the head that overtook the question (%s)", update.ClosureTarget, laterHead)
	}
	for _, want := range []string{"reject", laterHead, questionHead, "n1"} {
		if !strings.Contains(update.Resolution, want) {
			t.Fatalf("resolution = %q, want it to name %q", update.Resolution, want)
		}
	}
}

// TestVerdictQuestionForTheCurrentHeadStaysOpen (#414): a verdict on some older
// commit is not "the reviewer moved on" for the head the branch carries now.
// The question is about the current head, so it keeps waiting for its answer.
func TestVerdictQuestionForTheCurrentHeadStaysOpen(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	st := newStore(t)
	pid := newProject(t, st)
	seedQuestionAt(t, st, "app", 5, 9, "n1", laterHead, questionRow)
	// The only verdict in the store is the old head's: it answers a question
	// nobody asked, and must not be read as the current head's verdict.
	if _, err := st.AddVerdict(ctx, store.NewVerdict{
		ProjectID: pid, ItemNumber: 5, SHA: questionHead, Decision: "accept", Source: "note n1", At: now,
	}); err != nil {
		t.Fatalf("verdict: %v", err)
	}
	queue := &queueStub{
		item: &rig.QueueItem{ID: questionRow, State: "done"}, resolveRefusesPending: true,
		shows: map[string]*rig.QueueItem{questionRow: {ID: questionRow, State: "pending"}},
	}
	in := questionTick(st, pid, now, []forge.ChangeRequest{{Number: 9, State: "open", HeadSHA: laterHead}}, queue)
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if queue.updateCalls != 0 {
		t.Fatalf("updates = %+v, want none: the question names the head the branch carries", queue.updates)
	}
}

// TestVerdictQuestionSupersededCloseIsIdempotent (#414): the row's own state
// decides — the first tick closes it, the second finds it canceled and writes
// nothing more.
func TestVerdictQuestionSupersededCloseIsIdempotent(t *testing.T) {
	ctx := context.Background()
	queue, in := supersededFixture(t, "accept")
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	if queue.updateCalls != 1 {
		t.Fatalf("updates = %+v, want the question closed once", queue.updates)
	}
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	if queue.updateCalls != 1 {
		t.Fatalf("updates = %+v, want the second tick to write nothing", queue.updates)
	}
}
