package act

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/decide"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// TestMalformedVerdictAskRaisesRowAndComment: the plan's decision.malformed_verdict
// ask (epic #345, design D2) reaches the reviewer twice over — one queue row to the
// reviewer seat whose brief carries the literal line, and the same text as one comment
// on the change request, so the thread carries the instruction the row points at. The
// second tick is a ledger NoOp: one row, one comment, never a second of either.
func TestMalformedVerdictAskRaisesRowAndComment(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	st := newStore(t)
	pid := newProject(t, st)

	fake := forge.NewFake(forge.KindGitHub)
	fake.ItemsValue = []forge.Item{{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Five"}}
	fake.RequestsValue = []forge.ChangeRequest{{
		Number: 9, HeadSHA: "abc1234", Mergeable: true,
		Branch: "feature/5-x", Body: "Closes #5", DocsOnly: true,
	}}
	queue := &queueStub{item: &rig.QueueItem{ID: "qitem-mv", State: "pending"}, created: &rig.CreateRequest{}}
	raised := []DecisionRaised{}
	project := store.ProjectRow{
		ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig",
		Rigs: []string{"rig"}, Enabled: true,
	}
	// The plan is computed by the same decide pass the server runs: a malformed
	// note standing for this head produces one decision.malformed_verdict intent.
	plan := decide.PlanTick(decide.Snapshot{
		Project: decide.Project{Key: "app", Forge: decide.ForgeGitHub, Enabled: true},
		Items:   []decide.Item{{Number: 5, Kind: decide.KindTask, State: decide.StateOpen, Title: "Five"}},
		CRs:     []decide.CRState{{Number: 9, Item: 5, State: "open", HeadSHA: "abc1234"}},
		MalformedVerdicts: map[int]decide.MalformedVerdict{5: {
			NoteID: "6847905312", Head: "abc1234", Line: "## QA Verdict: ACCEPT ✅",
		}},
		ForgeOK: true,
	}, now)
	var literal string
	for _, intent := range plan.Intents {
		if intent.Event.Kind == "decision.malformed_verdict" {
			literal = intent.Text
		}
	}
	if literal == "" {
		t.Fatalf("plan = %+v, want one decision.malformed_verdict intent", plan.Intents)
	}
	in := TickInput{
		Store: st, Project: project, Forge: fake, Rig: queue, Git: &fakeGit{},
		Gate: openGate(), Apply: true, Items: fake.ItemsValue, CRs: fake.RequestsValue,
		Plan: plan, WorktreeRoot: t.TempDir(), Now: func() time.Time { return now },
		OnDecision: func(d DecisionRaised) { raised = append(raised, d) },
	}

	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	if queue.createCalls != 1 {
		t.Fatalf("queue creates = %d, want exactly one reviewer row", queue.createCalls)
	}
	if len(report.Decisions) != 1 || report.Decisions[0] != "qitem-mv" {
		t.Fatalf("decisions = %v, want the malformed-verdict row", report.Decisions)
	}
	if len(raised) != 1 {
		t.Fatalf("OnDecision fired %d times, want once", len(raised))
	}
	if queue.created.Destination != "dev-review@rig" {
		t.Fatalf("destination = %q, want the reviewer seat of the project's rig", queue.created.Destination)
	}
	if !strings.Contains(queue.created.Summary, "6847905312") {
		t.Fatalf("summary = %q, want it to name the note", queue.created.Summary)
	}
	if !strings.Contains(queue.created.BodyFile, "malformed-verdict-app-5-") {
		t.Fatalf("brief = %q, want the malformed-verdict brief name", queue.created.BodyFile)
	}
	// The comment carries the literal byte for byte, on the change request.
	if !hasWrite(fake.Writes, "comment", 9, literal) {
		t.Fatalf("writes = %+v, want one comment with the literal on change request #9", fake.Writes)
	}
	for _, want := range []string{
		"The merge gate reads one line: ACCEPT abc1234 or REJECT abc1234",
		"note 6847905312's first line was", "## QA Verdict: ACCEPT ✅", "ACCEPT abc1234",
	} {
		if !strings.Contains(literal, want) {
			t.Fatalf("comment text = %q, want it to carry %q", literal, want)
		}
	}
	events, _ := st.RecentEvents(ctx, 20)
	found := ""
	for _, event := range events {
		if event.Kind == "decision.malformed_verdict" {
			found = event.Detail
		}
	}
	if !strings.Contains(found, "; row qitem-mv") {
		t.Fatalf("event = %q, want the ledger line naming the row", found)
	}

	// Tick 2: the plan asks again, the ledger answers NoOp — no second row,
	// no second comment.
	report, err = RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	if queue.createCalls != 1 {
		t.Fatalf("tick 2: creates = %d, want the ledger to hold the row at one", queue.createCalls)
	}
	comments := 0
	for _, write := range fake.Writes {
		if write.Op == "comment" && write.Number == 9 {
			comments++
		}
	}
	if comments != 1 {
		t.Fatalf("tick 2: comments = %d, want one", comments)
	}
	if len(report.Decisions) != 0 {
		t.Fatalf("tick 2: decisions = %v, want none", report.Decisions)
	}
}
