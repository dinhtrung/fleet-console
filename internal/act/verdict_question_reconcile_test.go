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
	queue := &queueStub{item: &rig.QueueItem{ID: questionRow, State: "done"}, shows: map[string]*rig.QueueItem{questionRow: {ID: questionRow, State: "pending"}}}

	in := questionTick(st, pid, now, []forge.ChangeRequest{{Number: 9, State: "merged", Merged: true}}, queue)
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(queue.resolves) != 1 {
		t.Fatalf("resolves = %v, want the question row resolved once", queue.resolves)
	}
	if !strings.Contains(queue.resolves[0], "change request #9 merged") || !strings.Contains(queue.resolves[0], "n1") {
		t.Fatalf("resolve = %q, want the ending and the note named", queue.resolves[0])
	}
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
	queue := &queueStub{item: &rig.QueueItem{ID: questionRow, State: "done"}, shows: map[string]*rig.QueueItem{questionRow: {ID: questionRow, State: "pending"}}}

	in := questionTick(st, pid, now, []forge.ChangeRequest{{Number: 9, State: "open", HeadSHA: questionHead}}, queue)
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(queue.resolves) != 1 {
		t.Fatalf("resolves = %v, want the question row resolved once", queue.resolves)
	}
	if !strings.Contains(queue.resolves[0], "verdict accept") || !strings.Contains(queue.resolves[0], "n1") {
		t.Fatalf("resolve = %q, want the verdict and the note named", queue.resolves[0])
	}
}

// TestVerdictQuestionStandsWhileUnanswered (#376): no verdict for the head and
// an open change request is a question still waiting — nothing is resolved.
func TestVerdictQuestionStandsWhileUnanswered(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 13, 15, 0, 0, 0, time.UTC)
	st := newStore(t)
	pid := newProject(t, st)
	seedQuestion(t, st, "app", 5, 9, "n1")
	queue := &queueStub{item: &rig.QueueItem{ID: questionRow, State: "done"}, shows: map[string]*rig.QueueItem{questionRow: {ID: questionRow, State: "pending"}}}

	in := questionTick(st, pid, now, []forge.ChangeRequest{{Number: 9, State: "open", HeadSHA: questionHead}}, queue)
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(queue.resolves) != 0 {
		t.Fatalf("resolves = %v, want none while the question stands", queue.resolves)
	}
}

// TestVerdictQuestionResolveIsIdempotent (#376): the row's own state decides —
// a terminal row is left alone, so a repeated tick is a no-op.
func TestVerdictQuestionResolveIsIdempotent(t *testing.T) {
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
	if len(queue.resolves) != 0 {
		t.Fatalf("resolves = %v, want none for a terminal row", queue.resolves)
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
		item: &rig.QueueItem{ID: "qitem-app", State: "done"},
		shows: map[string]*rig.QueueItem{
			"qitem-app":   {ID: "qitem-app", State: "pending"},
			"qitem-other": {ID: "qitem-other", State: "pending"},
		},
	}

	in := questionTick(st, pid, now, []forge.ChangeRequest{{Number: 9, State: "merged", Merged: true}}, queue)
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(queue.resolves) != 1 {
		t.Fatalf("resolves = %v, want only this project's question", queue.resolves)
	}
	if !strings.Contains(queue.resolves[0], "n1") {
		t.Fatalf("resolve = %q, want the app project's note", queue.resolves[0])
	}
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
		item: &rig.QueueItem{ID: "qitem-answered", State: "done"},
		shows: map[string]*rig.QueueItem{
			"qitem-answered": {ID: "qitem-answered", State: "pending"},
			"qitem-waiting":  {ID: "qitem-waiting", State: "pending"},
		},
	}

	in := questionTick(st, pid, now, []forge.ChangeRequest{{Number: 9, State: "open", HeadSHA: otherHead}}, queue)
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(queue.resolves) != 1 {
		t.Fatalf("resolves = %v, want exactly the answered question", queue.resolves)
	}
	if !strings.Contains(queue.resolves[0], "n1") {
		t.Fatalf("resolve = %q, want the question whose head got a verdict", queue.resolves[0])
	}
}
