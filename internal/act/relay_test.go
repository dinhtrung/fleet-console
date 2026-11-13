package act

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/config"
	"github.com/dinhtrung/fleet-console/internal/decide"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// relayHead is the full 40-character head the relay brief must carry verbatim.
const relayHead = "aaaa1111111111111111111111111111111a"

// relayText is the plan's own words for a finished review without a verdict.
const relayText = "app #5: the review of change request #20 ended without a verdict for head " + relayHead + ". Post the verdict note for head " + relayHead + ", or answer INCONCLUSIVE on your row."

// raiseRelay is the one raise the brief tests go through.
func raiseRelay(t *testing.T, project store.ProjectRow, notes []forge.Comment) (*store.Store, *queueStub, *RaiseResult, string) {
	t.Helper()
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	fake := forge.NewFake(forge.KindGitHub)
	fake.NotesValue = notes
	queue := &queueStub{created: &rig.CreateRequest{}}
	if project.ID == 0 {
		project.ID = pid
	}
	actions := &Actions{Store: st, Forge: fake, Rig: queue, ProjectID: pid, ProjectKey: project.Key, WorktreeRoot: t.TempDir()}
	result, err := actions.RaiseVerdictRelay(ctx, project, 5, 20, relayText, "verdict.relayed", relayHead, "verdict-scribe@rig", actions.WorktreeRoot)
	if err != nil {
		t.Fatalf("raise: %v", err)
	}
	if result.NoOp {
		t.Fatalf("raise = NoOp, want a new relay row")
	}
	body, err := os.ReadFile(queue.created.BodyFile)
	if err != nil {
		t.Fatalf("read brief: %v", err)
	}
	return st, queue, result, string(body)
}

// TestRaiseVerdictRelay is the row mechanics: one row to the configured seat
// from human@kernel, the forge conversation read through CRNotes, the ledger
// event with the terminated dedupe key, and a second raise of the same head
// is a no-op.
func TestRaiseVerdictRelay(t *testing.T) {
	ctx := context.Background()
	st, queue, _, body := raiseRelay(t, store.ProjectRow{Key: "app", ForgeKind: "github", ForgeProject: "o/r"}, []forge.Comment{
		{ID: "7", CreatedAt: "2026-11-13T10:00:00Z", Body: "The pool sizing looks right; the retry path needs a second look."},
	})
	if queue.createCalls != 1 {
		t.Fatalf("creates = %d, want 1", queue.createCalls)
	}
	if queue.created.Destination != "verdict-scribe@rig" || queue.created.Source != "human@kernel" {
		t.Fatalf("row = %s from %s, want the configured seat from human@kernel", queue.created.Destination, queue.created.Source)
	}
	for _, want := range []string{
		relayHead, "#20", "via: verdict-scribe@rig",
		"denied", "no-follow-on", "Merging", "forbidden",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("brief = %q, want it to carry %q", body, want)
		}
	}
	exists, err := st.HasEvent(ctx, "verdict.relayed", "#5", "head="+relayHead+";")
	if err != nil || !exists {
		t.Fatalf("HasEvent = %v, %v; want the relayed event", exists, err)
	}

	// The same head again is the same relay: no second row.
	dedupeSt := newStore(t)
	pid := newProject(t, dedupeSt)
	fake := forge.NewFake(forge.KindGitHub)
	dedupeActions := &Actions{Store: dedupeSt, Forge: fake, Rig: &queueStub{created: &rig.CreateRequest{}}, ProjectID: pid, ProjectKey: "app", WorktreeRoot: t.TempDir()}
	// The first raise writes the ledger entry the second raise dedupes on.
	if _, err := dedupeActions.RaiseVerdictRelay(ctx, store.ProjectRow{ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r"}, 5, 20, relayText, "verdict.relayed", relayHead, "verdict-scribe@rig", t.TempDir()); err != nil {
		t.Fatalf("first raise: %v", err)
	}
	noop, err := dedupeActions.RaiseVerdictRelay(ctx, store.ProjectRow{ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r"}, 5, 20, relayText, "verdict.relayed", strings.ToUpper(relayHead), "verdict-scribe@rig", t.TempDir())
	if err != nil || !noop.NoOp {
		t.Fatalf("second raise = %+v, %v; want NoOp", noop, err)
	}

	// A seat-less relay is refused with the named reason, not a row to nowhere.
	if _, err := dedupeActions.RaiseVerdictRelay(ctx, store.ProjectRow{ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r"}, 6, 21, relayText, "verdict.relayed", "bbbb222", "", t.TempDir()); err == nil || !strings.Contains(err.Error(), "no verdict_relay.seat") {
		t.Fatalf("seat-less raise = %v, want the no-seat refusal", err)
	}
}

// TestRelayBrief is the brief's contents per forge, and the no-notes case:
// the review quoted verbatim with note ids, the full head sha, the exact
// posting command with the via line substituted, and the INCONCLUSIVE route.
func TestRelayBrief(t *testing.T) {
	notes := []forge.Comment{
		{ID: "12", CreatedAt: "2026-11-13T10:00:00Z", Body: "First pass: the retry loop swallows context cancellation."},
		{ID: "13", CreatedAt: "2026-11-13T10:05:00Z", Body: "Looked again - the pool sizing is fine, ship it."},
	}
	_, _, _, github := raiseRelay(t, store.ProjectRow{Key: "app", ForgeKind: "github", ForgeProject: "o/r"}, notes)
	for _, want := range []string{
		"[note 12]", "First pass: the retry loop swallows context cancellation.",
		"[note 13]", "Looked again - the pool sizing is fine, ship it.",
		"Head under review: " + relayHead,
		"Change request: #20", "Item: #5",
		"gh pr comment 20 -R o/r --body 'ACCEPT " + relayHead + "\nvia: verdict-scribe@rig'",
		"INCONCLUSIVE",
	} {
		if !strings.Contains(github, want) {
			t.Fatalf("github brief = %q, want it to carry %q", github, want)
		}
	}

	_, _, _, gitlab := raiseRelay(t, store.ProjectRow{Key: "app", ForgeKind: "gitlab", ForgeProject: "42"}, notes)
	want := "glab api -X POST 'projects/42/merge_requests/20/notes' -F 'body=ACCEPT " + relayHead + "\nvia: verdict-scribe@rig'"
	if !strings.Contains(gitlab, want) {
		t.Fatalf("gitlab brief = %q, want it to carry %q", gitlab, want)
	}

	_, _, _, empty := raiseRelay(t, store.ProjectRow{Key: "app", ForgeKind: "github", ForgeProject: "o/r"}, nil)
	if !strings.Contains(empty, "The change request carries no notes.") {
		t.Fatalf("empty brief = %q, want the no-notes sentence", empty)
	}
}

// TestOperatorDestination: operator mode is a destination switch, not a
// second intent (verdict-relay, design D4) — the configured seat takes the
// operator questions when the project's relay mode is operator, and
// human@kernel keeps them otherwise, scribe mode included.
func TestOperatorDestination(t *testing.T) {
	for _, tc := range []struct {
		mode, seat, want string
	}{
		{"", "", "human@kernel"},
		{config.RelayModeScribe, "verdict-scribe@rig", "human@kernel"},
		{config.RelayModeOperator, "operator-agent@kernel", "operator-agent@kernel"},
		{config.RelayModeOperator, "  ", "human@kernel"},
	} {
		if got := operatorDestination(tc.mode, tc.seat); got != tc.want {
			t.Fatalf("operatorDestination(%q, %q) = %q, want %q", tc.mode, tc.seat, got, tc.want)
		}
	}

	ctx := context.Background()
	raise := func(mode, seat string) string {
		t.Helper()
		st := newStore(t)
		pid := newProject(t, st)
		queue := &queueStub{creates: &[]rig.CreateRequest{}}
		in := TickInput{
			Store: st, Project: store.ProjectRow{ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Enabled: true},
			Forge: forge.NewFake(forge.KindGitHub), Rig: queue, Git: &fakeGit{},
			Gate: openGate(), Apply: true, WorktreeRoot: t.TempDir(), Now: func() time.Time { return time.Now() },
			RelayMode: mode, RelaySeat: seat,
			Plan: decide.Plan{Intents: []decide.Intent{{
				Kind: decide.IntentRaiseDecision, Project: "app", Item: 5, Audience: decide.AudienceOperator,
				Text: noVerdictQuestion, ChangeRequest: 20, HeadSHA: "aaaa111",
				Event: decide.Event{Actor: "decide", Kind: "decision.no_verdict", Subject: "#5", Detail: "cr=20 head=aaaa111"},
			}}},
		}
		if _, err := RunTick(ctx, in); err != nil {
			t.Fatalf("tick: %v", err)
		}
		creates := *queue.creates
		if len(creates) != 1 {
			t.Fatalf("creates = %d, want the one operator question", len(creates))
		}
		return creates[0].Destination
	}
	if got := raise(config.RelayModeOperator, "operator-agent@kernel"); got != "operator-agent@kernel" {
		t.Fatalf("operator-mode destination = %q, want the configured seat", got)
	}
	if got := raise(config.RelayModeScribe, "verdict-scribe@rig"); got != "human@kernel" {
		t.Fatalf("scribe-mode destination = %q, want human@kernel", got)
	}
}

// TestCaptureRecordsRelayMarker: a note whose second non-empty line is
// `via: <seat>` stores the marker in the capture detail; absent, mangled, or
// overpopulated second lines record nothing and never error — line 1's
// grammar is untouched.
func TestCaptureRecordsRelayMarker(t *testing.T) {
	const head = "abc1234def5678"
	cases := []struct {
		name       string
		body       string
		wantVia    string
		noCapture  bool
	}{
		{"relayed note", "ACCEPT " + head + "\nvia: verdict-scribe@rig", "verdict-scribe@rig", false},
		{"reviewer's own note", "ACCEPT " + head + "\nThe pool sizing is right.", "", false},
		{"mangled marker", "ACCEPT " + head + "\nvia verdict-scribe@rig", "", false},
		{"marker with tail", "ACCEPT " + head + "\nvia: verdict-scribe@rig (relayed)", "", false},
		// A marker before the verdict is not a verdict note at all: line 1's
		// grammar is untouched, so capture stores nothing and reads no marker.
		{"marker as first line", "via: verdict-scribe@rig\nACCEPT " + head, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st := newStore(t)
			pid := newProject(t, st)
			actions := &Actions{Store: st, ProjectID: pid, ProjectKey: "app"}
			captured, err := actions.CaptureVerdict(ctx, 0, 5, head, []forge.Comment{
				{ID: "4", CreatedAt: "2026-11-13T10:00:00Z", Body: tc.body},
			})
			if err != nil {
				t.Fatalf("capture: %v", err)
			}
			if tc.noCapture {
				if captured != nil {
					t.Fatalf("capture = %+v, want none: the note is not a verdict", captured)
				}
				return
			}
			if captured == nil {
				t.Fatalf("capture = nil, want a stored verdict")
			}
			events, err := st.RecentEvents(ctx, 10)
			if err != nil {
				t.Fatalf("events: %v", err)
			}
			var detail string
			for _, event := range events {
				if event.Kind == "verdict.capture" {
					detail = event.Detail
				}
			}
			if tc.wantVia != "" && !strings.Contains(detail, "; via="+tc.wantVia) {
				t.Fatalf("detail = %q, want it to carry via=%s", detail, tc.wantVia)
			}
			if tc.wantVia == "" && strings.Contains(detail, "via=") {
				t.Fatalf("detail = %q, want no marker", detail)
			}
		})
	}
}

// TestInconclusiveRelayEscalates is the INCONCLUSIVE path (verdict-relay,
// design D6): the relay row exists, the review does not decide, the relay
// answers its row, and the operator question for that head fires by itself —
// because askedForHead counts live rows only, and an answered row is none.
// Nothing is ever written on the forge: the relay reads, it does not post.
func TestInconclusiveRelayEscalates(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	st := newStore(t)
	pid := newProject(t, st)

	fake := forge.NewFake(forge.KindGitHub)
	fake.ItemsValue = []forge.Item{{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Five"}}
	fake.RequestsValue = []forge.ChangeRequest{{
		Number: 9, HeadSHA: relayHead, Mergeable: true, Body: "Closes #5", DocsOnly: true,
	}}
	// No NotesValue: the review carried no verdict note.

	// Tick 1: the plan carries the relay intent; the tick raises the relay row
	// and nothing else — the operator question's own ask is covered by its
	// tests, and here only the relay is on the plan so the escalation after
	// the answer is visible.
	queue := &queueStub{creates: &[]rig.CreateRequest{}}
	project := store.ProjectRow{ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", Enabled: true}
	in := TickInput{
		Store: st, Project: project, Forge: fake, Rig: queue, Git: &fakeGit{},
		Gate: openGate(), Apply: true, Items: fake.ItemsValue, CRs: fake.RequestsValue,
		WorktreeRoot: t.TempDir(), Now: func() time.Time { return now },
		RelayMode: config.RelayModeScribe, RelaySeat: "verdict-scribe@rig",
		Plan: decide.Plan{Intents: []decide.Intent{{
			Kind: decide.IntentRelayVerdict, Project: "app", Item: 5,
			Text: relayText, ChangeRequest: 9, HeadSHA: relayHead,
			Event: decide.Event{Actor: "decide", Kind: "verdict.relayed", Subject: "#5",
				Detail: "head=" + relayHead + "; planned"},
		}}},
	}
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("first tick: %v", err)
	}
	if len(*queue.creates) != 1 || (*queue.creates)[0].Destination != "verdict-scribe@rig" {
		t.Fatalf("first tick creates = %+v, want exactly the relay row to the scribe", *queue.creates)
	}
	if len(fake.Writes) != 0 {
		t.Fatalf("forge writes = %+v, want none from a relay", fake.Writes)
	}

	// The relay reads the review and answers INCONCLUSIVE: its row ends.
	answeredRows := []decide.Row{
		{
			QItemID: "qitem-review", State: "done", Destination: "dev-review@rig",
			Body: "app #5 review of head " + relayHead + " ended",
		},
		{
			QItemID: "qitem-relay", State: "done", Destination: "verdict-scribe@rig",
			Body: "app #5 relay for head " + relayHead + " answered denied: the review does not decide",
		},
	}

	// Tick 2: the relay is deduped by the ledger fact on the snapshot, and the
	// operator question fires by itself — no live row names the head any more.
	plan := decide.PlanTick(decide.Snapshot{
		Project: decide.Project{Key: "app", Forge: decide.ForgeGitHub, Enabled: true, RelayMode: config.RelayModeScribe},
		Items:   []decide.Item{{Number: 5, Kind: decide.KindTask, State: decide.StateOpen, Title: "Five"}},
		CRs:     []decide.CRState{{Number: 9, Item: 5, State: "open", HeadSHA: relayHead}},
		Rows:    answeredRows,
		RelayedHeads: []decide.RelayedHead{{Item: 5, Head: relayHead}},
		ForgeOK: true,
	}, now)
	relays := 0
	var operator *decide.Intent
	for index := range plan.Intents {
		if plan.Intents[index].Kind == decide.IntentRelayVerdict {
			relays++
		}
		if plan.Intents[index].Audience == decide.AudienceOperator {
			operator = &plan.Intents[index]
		}
	}
	if relays != 0 {
		t.Fatalf("plan carries %d relay intents, want the ledger to dedupe them", relays)
	}
	if operator == nil {
		t.Fatalf("plan = %+v, want the operator question the answered relay earns", plan.Intents)
	}

	in2 := in
	in2.Plan = decide.Plan{Intents: []decide.Intent{*operator}}
	creates2 := &[]rig.CreateRequest{}
	queue2 := &queueStub{creates: creates2}
	in2.Rig = queue2
	if _, err := RunTick(ctx, in2); err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if len(*creates2) != 1 || (*creates2)[0].Destination != "human@kernel" {
		t.Fatalf("second tick creates = %+v, want exactly the operator question", *creates2)
	}
	if len(fake.Writes) != 0 {
		t.Fatalf("forge writes = %+v, want none across both ticks", fake.Writes)
	}
	// Every forge read went to the change request's conversation — the relay
	// reads, it never posts. The extra reads are the merge gate's own.
	for _, number := range fake.CRNotesAsked {
		if number != 9 {
			t.Fatalf("CRNotesAsked holds %d, want only the change request 9", number)
		}
	}
}
