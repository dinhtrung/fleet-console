package act

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/decide"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// noVerdictQuestion is the plan's own words for the missing-verdict question
// (#265): the row's body must carry the same facts the operator needs.
const noVerdictQuestion = "app #5 Connection pool: change request #20 has no verdict for head aaaa111. " +
	"Approve or reject it on the item drawer (POST /api/items/5/verdict) or reply fleet: accept / fleet: reject on the item."

// TestRaiseOperatorQuestion is the brief's first evidence: the question lands
// on one human@kernel row whose body carries the item, the change request, the
// head sha and both answer routes; the same (item, kind, head) again is a
// no-op; and a longer sha is a different fact that asks again — the key's
// terminator keeps head=aaaa111 from matching head=aaaa1110's event.
func TestRaiseOperatorQuestion(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	queue := &queueStub{created: &rig.CreateRequest{}}
	project := store.ProjectRow{ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r"}
	actions := &Actions{Store: st, Rig: queue, ProjectID: pid, ProjectKey: "app", WorktreeRoot: t.TempDir()}

	raise, err := actions.RaiseOperatorQuestion(ctx, project, 5, noVerdictQuestion, "decision.no_verdict", "AAAA111", actions.WorktreeRoot)
	if err != nil {
		t.Fatalf("raise: %v", err)
	}
	if raise.NoOp {
		t.Fatalf("first raise = NoOp, want a new question")
	}
	if queue.createCalls != 1 {
		t.Fatalf("queue creates = %d, want 1", queue.createCalls)
	}
	if created := queue.created; created.Destination != "human@kernel" || created.Source != "human@kernel" {
		t.Fatalf("row = %s from %s, want human@kernel both ways", created.Destination, created.Source)
	}
	body, err := os.ReadFile(queue.created.BodyFile)
	if err != nil {
		t.Fatalf("read brief: %v", err)
	}
	text := string(body)
	for _, want := range []string{
		"app #5 Connection pool", "change request #20", "aaaa111",
		"POST /api/items/5/verdict", "fleet: accept", "fleet: reject",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("brief = %q, want it to carry %q", text, want)
		}
	}
	exists, err := st.HasEvent(ctx, "decision.no_verdict", "#5", "head=aaaa111;")
	if err != nil || !exists {
		t.Fatalf("HasEvent = %v, %v; want the dedupe event", exists, err)
	}
	events, err := st.RecentEvents(ctx, 10)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	var detail string
	for _, event := range events {
		if event.Kind == "decision.no_verdict" {
			detail = event.Detail
		}
	}
	if !strings.Contains(detail, "head=aaaa111; row") {
		t.Fatalf("event detail = %q, want head=aaaa111; row", detail)
	}

	// The same question again is the same question: no second row.
	noop, err := actions.RaiseOperatorQuestion(ctx, project, 5, noVerdictQuestion, "decision.no_verdict", "aaaa111", actions.WorktreeRoot)
	if err != nil || !noop.NoOp {
		t.Fatalf("second raise = %+v, %v; want NoOp", noop, err)
	}
	if queue.createCalls != 1 {
		t.Fatalf("creates = %d, want the deduped raise to create nothing", queue.createCalls)
	}

	// A longer sha is a moved head: a different fact, asked again.
	moved, err := actions.RaiseOperatorQuestion(ctx, project, 5, noVerdictQuestion, "decision.no_verdict", "aaaa1110", actions.WorktreeRoot)
	if err != nil || moved.NoOp {
		t.Fatalf("moved-head raise = %+v, %v; want a new question", moved, err)
	}
	if queue.createCalls != 2 {
		t.Fatalf("creates = %d, want 2 after the moved-head raise", queue.createCalls)
	}
}

// TestReviewBriefCarriesTheVerdictCommand is the second evidence: the
// reviewer's brief carries the change request number, the head sha verbatim
// (lower-case), the literal command with the sha substituted, and the row
// fallback — for both forges the fleet serves.
func TestReviewBriefCarriesTheVerdictCommand(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)

	// The dedupe keys on the text, so each forge asks about its own head.
	raise := func(project store.ProjectRow, head, upper string) string {
		t.Helper()
		queue := &queueStub{created: &rig.CreateRequest{}}
		actions := &Actions{Store: st, Rig: queue, ProjectID: pid, ProjectKey: "app", WorktreeRoot: t.TempDir()}
		text := fmt.Sprintf("app #5: verdict stale - ACCEPT aaaa111 is on aaaa111, head is %s; review %s", head, head)
		result, err := actions.RaiseDecision(ctx, project, 5, text, "decision.stale", 20, upper, actions.WorktreeRoot)
		if err != nil || result.NoOp {
			t.Fatalf("raise = %+v, %v; want a new re-review row", result, err)
		}
		body, err := os.ReadFile(queue.created.BodyFile)
		if err != nil {
			t.Fatalf("read brief: %v", err)
		}
		return string(body)
	}

	github := raise(store.ProjectRow{ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rigs: []string{"rig"}}, "bbbb222", "BBBB222")
	for _, want := range []string{
		"Change request: #20", "Head: bbbb222",
		"gh pr comment 20 -R o/r --body 'ACCEPT bbbb222'",
		"REJECT bbbb222",
		"If the command cannot run, answer on the row with `ACCEPT bbbb222`.",
	} {
		if !strings.Contains(github, want) {
			t.Fatalf("github brief = %q, want it to carry %q", github, want)
		}
	}

	gitlab := raise(store.ProjectRow{ID: pid, Key: "app", ForgeKind: "gitlab", ForgeProject: "42", Rigs: []string{"rig"}}, "cccc333", "CCCC333")
	for _, want := range []string{
		"Change request: #20", "Head: cccc333",
		"glab api -X POST 'projects/42/merge_requests/20/notes' -F 'body=ACCEPT cccc333'",
		"If the command cannot run, answer on the row with `ACCEPT cccc333`.",
	} {
		if !strings.Contains(gitlab, want) {
			t.Fatalf("gitlab brief = %q, want it to carry %q", gitlab, want)
		}
	}
}

// TestTickRoutesDecisionAudience: one tick, two decisions, two destinations —
// the operator question reaches human@kernel and the re-review reaches the
// reviewer seat, with OnDecision firing for both.
func TestTickRoutesDecisionAudience(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	queue := &queueStub{creates: &[]rig.CreateRequest{}}
	project := store.ProjectRow{ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rigs: []string{"rig"}, Enabled: true}
	in := TickInput{
		Store: st, Project: project, Forge: forge.NewFake(forge.KindGitHub), Rig: queue, Git: &fakeGit{},
		Gate: openGate(), Apply: true, WorktreeRoot: t.TempDir(), Now: func() time.Time { return time.Now() },
		Plan: decide.Plan{Intents: []decide.Intent{
			{
				Kind: decide.IntentRaiseDecision, Project: "app", Item: 5, Audience: decide.AudienceOperator,
				Text: noVerdictQuestion, ChangeRequest: 20, HeadSHA: "aaaa111",
				Event: decide.Event{Actor: "decide", Kind: "decision.no_verdict", Subject: "#5", Detail: "cr=20 head=aaaa111"},
			},
			{
				Kind: decide.IntentRaiseDecision, Project: "app", Item: 6, Audience: decide.AudienceReviewer,
				Text: "app #6: verdict stale - ACCEPT aaaa111 is on aaaa111, head is bbbb222; review bbbb222",
				ChangeRequest: 21, HeadSHA: "bbbb222",
				Event: decide.Event{Actor: "decide", Kind: "decision.stale", Subject: "#6",
					Detail: "app #6: verdict stale - ACCEPT aaaa111 is on aaaa111, head is bbbb222; review bbbb222"},
			},
		}},
	}
	var raised []DecisionRaised
	in.OnDecision = func(r DecisionRaised) { raised = append(raised, r) }

	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	creates := *queue.creates
	if len(creates) != 2 {
		t.Fatalf("creates = %d, want one per audience", len(creates))
	}
	if creates[0].Destination != "human@kernel" || creates[0].Source != "human@kernel" {
		t.Fatalf("operator row = %s from %s, want human@kernel both ways", creates[0].Destination, creates[0].Source)
	}
	if creates[1].Destination != "dev-review@rig" {
		t.Fatalf("reviewer row = %s, want dev-review@rig", creates[1].Destination)
	}
	if len(raised) != 2 || raised[0].Item != 5 || raised[1].Item != 6 {
		t.Fatalf("decisions = %+v, want one per decision, both fired", raised)
	}
	exists, err := st.HasEvent(ctx, "decision.no_verdict", "#5", "head=aaaa111;")
	if err != nil || !exists {
		t.Fatalf("HasEvent(no_verdict) = %v, %v; want the operator's event", exists, err)
	}
	exists, err = st.HasEvent(ctx, "decision.stale", "#6", "review bbbb222")
	if err != nil || !exists {
		t.Fatalf("HasEvent(stale) = %v, %v; want the reviewer's event", exists, err)
	}
	_ = report
}

// TestSilentReviewerBecomesAnOperatorQuestion is the end-to-end: a reviewer
// seat ends its row, the change request carries no verdict, the plan's
// operator intent becomes exactly one human@kernel row, and the second tick —
// same head, question already on the ledger — adds nothing.
func TestSilentReviewerBecomesAnOperatorQuestion(t *testing.T) {
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
	// No NotesValue: the reviewer seat ended its row without a verdict note.

	// The plan is built the way the server builds it: from the pre-capture
	// state, where no verdict names the head.
	plan := decide.PlanTick(decide.Snapshot{
		Project: decide.Project{Key: "app", Forge: decide.ForgeGitHub, Enabled: true},
		Items:   []decide.Item{{Number: 5, Kind: decide.KindTask, State: decide.StateOpen, Title: "Five"}},
		CRs:     []decide.CRState{{Number: 9, Item: 5, State: "open", HeadSHA: "abc1234"}},
		ForgeOK: true,
	}, now)
	var operator *decide.Intent
	for index := range plan.Intents {
		if plan.Intents[index].Audience == decide.AudienceOperator {
			operator = &plan.Intents[index]
		}
	}
	if operator == nil {
		t.Fatalf("plan = %+v, want the operator question the missing verdict earns", plan.Intents)
	}

	queue := &queueStub{creates: &[]rig.CreateRequest{}}
	project := store.ProjectRow{
		ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", Enabled: true,
	}
	in := TickInput{
		Store: st, Project: project, Forge: fake, Rig: queue, Git: &fakeGit{},
		Gate: openGate(), Apply: true, Items: fake.ItemsValue, CRs: fake.RequestsValue,
		Plan: plan, WorktreeRoot: t.TempDir(), Now: func() time.Time { return now },
	}

	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("first tick: %v", err)
	}
	creates := *queue.creates
	if len(creates) != 1 {
		t.Fatalf("creates = %d, want exactly one human@kernel row", len(creates))
	}
	if creates[0].Destination != "human@kernel" || creates[0].Source != "human@kernel" {
		t.Fatalf("row = %s from %s, want human@kernel both ways", creates[0].Destination, creates[0].Source)
	}
	exists, err := st.HasEvent(ctx, "decision.no_verdict", "#5", "head=abc1234;")
	if err != nil || !exists {
		t.Fatalf("HasEvent = %v, %v; want the question on the ledger", exists, err)
	}

	// The same head again: the question stands, nothing new.
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if len(*queue.creates) != 1 {
		t.Fatalf("after second tick creates = %d, want the deduped question to add nothing", len(*queue.creates))
	}
}
