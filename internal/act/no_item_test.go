package act

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// The measured case (#314): change request #683 (tsim) carries "Refs #448" in
// its body, names #448 in its title and on its branch, and carries an ACCEPT
// note for its own head. The gate reads "Closes" only, so the change request
// still never merges and its notes are never read as a verdict for a guessed
// item — but the silence itself is now visible: one skip line and one audit
// row per (change request, head), naming the item the change request mentioned
// elsewhere. A title or branch mention is a mention, never an association.
const (
	noItemHead   = "7e868054a1b2c3d4e5f60718293a4b5c6d7e8f90"
	noItemTitle  = "Draft: tsim #448: Scenarios: web entity title hides name"
	noItemBranch = "feature/448-scenarios-web-entity-title-hides-name"
)

func noItemFake() *forge.Fake {
	fake := forge.NewFake(forge.KindGitLab)
	fake.ItemsValue = []forge.Item{{Number: 448, Kind: forge.KindTask, State: forge.StateOpen, Title: "Scenarios"}}
	fake.RequestsValue = []forge.ChangeRequest{{
		Number: 683, State: "open", HeadSHA: noItemHead, Branch: noItemBranch, Title: noItemTitle,
		Draft: true, Body: "Refs #448\n\nThe web scenario entity title hides the scenario name.",
	}}
	return fake
}

func noItemInput(st *store.Store, pid int64, fake *forge.Fake) TickInput {
	return TickInput{
		Store: st,
		Project: store.ProjectRow{
			ID: pid, Key: "tsim", ForgeKind: "gitlab", ForgeProject: "o/r", Rig: "rig", Enabled: true,
		},
		Forge: fake, Rig: &queueStub{}, Git: &fakeGit{},
		Gate: openGate(), Apply: true, Items: fake.ItemsValue, CRs: fake.RequestsValue,
		Now: func() time.Time { return time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC) },
	}
}

func noItemEvents(t *testing.T, st *store.Store) []store.EventRow {
	t.Helper()
	page, err := st.Events(context.Background(), store.EventFilter{Kind: "change_request.no_item", Limit: 50})
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	return page.Events
}

// TestNoClosingItemIsVisible: the #683 shape — one skip line naming the change
// request and the item it mentioned elsewhere, and one audit row with the
// fixed vocabulary (subject #448, detail cr=683; head=…; named=448;).
func TestNoClosingItemIsVisible(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	fake := noItemFake()

	report, err := RunTick(ctx, noItemInput(st, pid, fake))
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if !hasSkip(report, `#448 change request #683: body closes no item: names #448 only in the title/branch; the gate reads "Closes #448" in the body`) {
		t.Fatalf("skipped = %v, want the named line", report.Skipped)
	}
	rows := noItemEvents(t, st)
	if len(rows) != 1 {
		t.Fatalf("events = %+v, want one change_request.no_item", rows)
	}
	if rows[0].Subject != "#448" ||
		rows[0].Detail != "cr=683; head="+noItemHead+"; named=448;" {
		t.Fatalf("event = %s / %q, want the fixed-vocabulary subject and detail", rows[0].Subject, rows[0].Detail)
	}
	// The diagnostic never associates: the change request's notes were not
	// read as a verdict for #448, and nothing merged.
	if len(fake.CRNotesAsked) != 0 {
		t.Fatalf("CRNotesAsked = %v, want the notes unread", fake.CRNotesAsked)
	}
	for _, write := range fake.Writes {
		if write.Op == "merge" {
			t.Fatalf("writes = %+v, want no merge", fake.Writes)
		}
	}
}

// TestNoClosingItemNamesTitleOrBranch: the title alone or the branch alone
// names the item; a change request that names one nowhere gets the plain line
// and the cr-<number> subject with named=none — no guessed item anywhere.
func TestNoClosingItemNamesTitleOrBranch(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name        string
		title       string
		branch      string
		wantSubject string
		wantDetail  string
		wantLine    string
	}{
		{
			name: "title names it", title: noItemTitle, branch: "main",
			wantSubject: "#448",
			wantDetail:  "cr=683; head=" + noItemHead + "; named=448;",
			wantLine:    `#448 change request #683: body closes no item: names #448 only in the title/branch; the gate reads "Closes #448" in the body`,
		},
		{
			name: "branch names it", title: "Draft: tsim scenarios", branch: noItemBranch,
			wantSubject: "#448",
			wantDetail:  "cr=683; head=" + noItemHead + "; named=448;",
			wantLine:    `#448 change request #683: body closes no item: names #448 only in the title/branch; the gate reads "Closes #448" in the body`,
		},
		{
			name: "nothing names it", title: "Draft: tsim scenarios", branch: "main",
			wantSubject: "cr-683",
			wantDetail:  "cr=683; head=" + noItemHead + "; named=none;",
			wantLine:    "change request #683: body closes no item",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newStore(t)
			pid := newProject(t, st)
			fake := noItemFake()
			fake.RequestsValue[0].Title = tc.title
			fake.RequestsValue[0].Branch = tc.branch
			fake.RequestsValue[0].Body = "Updates the scenario web entity."

			report, err := RunTick(ctx, noItemInput(st, pid, fake))
			if err != nil {
				t.Fatalf("tick: %v", err)
			}
			if !hasSkip(report, tc.wantLine) {
				t.Fatalf("skipped = %v, want %q", report.Skipped, tc.wantLine)
			}
			rows := noItemEvents(t, st)
			if len(rows) != 1 {
				t.Fatalf("events = %+v, want one change_request.no_item", rows)
			}
			if rows[0].Subject != tc.wantSubject || rows[0].Detail != tc.wantDetail {
				t.Fatalf("event = %s / %q, want %s / %q", rows[0].Subject, rows[0].Detail, tc.wantSubject, tc.wantDetail)
			}
		})
	}
}

// TestNoClosingItemPrintsOnce: ten ticks over the same (change request, head)
// print the line once and write one audit row — the ledger dedupe, the same
// rule verdict.unattributed follows.
func TestNoClosingItemPrintsOnce(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	fake := noItemFake()
	in := noItemInput(st, pid, fake)

	first, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	if !hasSkip(first, "body closes no item") {
		t.Fatalf("tick 1 skipped = %v, want the line", first.Skipped)
	}
	for tick := 2; tick <= 10; tick++ {
		later, err := RunTick(ctx, in)
		if err != nil {
			t.Fatalf("tick %d: %v", tick, err)
		}
		if hasSkip(later, "body closes no item") {
			t.Fatalf("tick %d skipped = %v, want silence over the same head", tick, later.Skipped)
		}
	}
	if rows := noItemEvents(t, st); len(rows) != 1 {
		t.Fatalf("events after ten ticks = %+v, want still one", rows)
	}
}

// TestRefsChangeRequestNeverMerges: the Refs change request never merges and
// its ACCEPT note is never captured — then the same change request with
// "Closes #448" in the body takes the unchanged path and the verdict lands.
func TestRefsChangeRequestNeverMerges(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	fake := noItemFake()
	fake.NotesValue = []forge.Comment{{
		ID: "37508", Body: "ACCEPT " + noItemHead, CreatedAt: "2026-11-12T08:00:00Z",
	}}
	in := noItemInput(st, pid, fake)

	refs, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("refs tick: %v", err)
	}
	if len(refs.Verdicts) != 0 {
		t.Fatalf("verdicts = %+v, want none captured off a Refs body", refs.Verdicts)
	}
	if len(fake.CRNotesAsked) != 0 {
		t.Fatalf("CRNotesAsked = %v, want the notes unread", fake.CRNotesAsked)
	}
	for _, write := range fake.Writes {
		if write.Op == "merge" {
			t.Fatalf("writes = %+v, want no merge off a Refs body", fake.Writes)
		}
	}

	// The same change request closing the item takes the unchanged path: the
	// verdict is captured from its notes (capture first, decide second).
	fake.RequestsValue[0].Body = "Closes #448"
	closes, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("closes tick: %v", err)
	}
	if len(closes.Verdicts) != 1 || !strings.EqualFold(closes.Verdicts[0].Decision, "ACCEPT") {
		t.Fatalf("verdicts = %+v, want the ACCEPT captured", closes.Verdicts)
	}
	if !sawCRNotes(fake.CRNotesAsked, 683) {
		t.Fatalf("CRNotesAsked = %v, want the change request's own conversation read", fake.CRNotesAsked)
	}
}

func sawCRNotes(asked []int, number int) bool {
	for _, got := range asked {
		if got == number {
			return true
		}
	}
	return false
}

func noItemDecisions(t *testing.T, st *store.Store) []store.EventRow {
	t.Helper()
	page, err := st.Events(context.Background(), store.EventFilter{Kind: "decision.no_item", Limit: 50})
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	return page.Events
}

// TestNoClosingItemAsksTheOperator (#368, the #700 shape): the named item is
// open, so the body edit that would work exists — the tick raises exactly one
// row naming the change request, the head and the item, and offers that edit.
// The skip line and the change_request.no_item event are unchanged beside it.
func TestNoClosingItemAsksTheOperator(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	fake := noItemFake() // #448 open
	var creates []rig.CreateRequest
	var raised []DecisionRaised
	in := noItemInput(st, pid, fake)
	in.Rig = &queueStub{creates: &creates}
	in.WorktreeRoot = t.TempDir()
	in.OnDecision = func(d DecisionRaised) { raised = append(raised, d) }

	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if !hasSkip(report, `#448 change request #683: body closes no item: names #448 only in the title/branch; the gate reads "Closes #448" in the body`) {
		t.Fatalf("skipped = %v, want the unchanged line", report.Skipped)
	}
	if len(creates) != 1 {
		t.Fatalf("creates = %+v, want one operator row", creates)
	}
	req := creates[0]
	if req.Destination != "human@kernel" {
		t.Fatalf("destination = %q, want human@kernel", req.Destination)
	}
	if !strings.Contains(req.Summary, "change request #683") {
		t.Fatalf("summary = %q, want the change request named", req.Summary)
	}
	if !strings.Contains(req.Summary, "#448") || !strings.Contains(req.Summary, "open") {
		t.Fatalf("summary = %q, want the named item and its state", req.Summary)
	}
	if req.EvidenceRef == "" {
		t.Fatalf("evidence ref empty; a human-routed row needs one")
	}
	brief, err := os.ReadFile(req.BodyFile)
	if err != nil {
		t.Fatalf("brief: %v", err)
	}
	if !strings.Contains(string(brief), `add "Closes #448" to the body`) {
		t.Fatalf("brief = %q, want the body edit for an open item", brief)
	}
	if !strings.Contains(string(brief), "no lawful path") {
		t.Fatalf("brief = %q, want the line saying the gate cannot take it", brief)
	}
	if len(report.Decisions) != 1 {
		t.Fatalf("decisions = %v, want the row reported", report.Decisions)
	}
	if len(raised) != 1 || raised[0].Item != 448 || raised[0].QItemID != report.Decisions[0] {
		t.Fatalf("onDecision = %+v, want one decision about #448", raised)
	}
	rows := noItemDecisions(t, st)
	if len(rows) != 1 {
		t.Fatalf("decision events = %+v, want one", rows)
	}
	if rows[0].Subject != "cr-683" ||
		!strings.HasPrefix(rows[0].Detail, "cr=683; head="+noItemHead+"; named=448; state=open;") {
		t.Fatalf("event = %s / %q, want the change request as subject and the fixed vocabulary", rows[0].Subject, rows[0].Detail)
	}
}

// TestNoClosingItemAsksWhenTheItemIsClosed: #468 is not in the open list, so
// "Closes #468" would not help — the row says so and offers the two answers
// that can work: merge by hand, or a follow-up item with a re-homed branch.
func TestNoClosingItemAsksWhenTheItemIsClosed(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	fake := noItemFake()
	fake.ItemsValue = nil // #448 is closed: Items reads the open list only
	var creates []rig.CreateRequest
	in := noItemInput(st, pid, fake)
	in.Rig = &queueStub{creates: &creates}
	in.WorktreeRoot = t.TempDir()

	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(creates) != 1 {
		t.Fatalf("creates = %+v, want one operator row", creates)
	}
	if !strings.Contains(creates[0].Summary, "not open") {
		t.Fatalf("summary = %q, want the item's state", creates[0].Summary)
	}
	brief, err := os.ReadFile(creates[0].BodyFile)
	if err != nil {
		t.Fatalf("brief: %v", err)
	}
	for _, want := range []string{`Adding "Closes #448" cannot help`, "Merge it by hand", "follow-up item"} {
		if !strings.Contains(string(brief), want) {
			t.Fatalf("brief = %q, want %q", brief, want)
		}
	}
	rows := noItemDecisions(t, st)
	if len(rows) != 1 || !strings.Contains(rows[0].Detail, "state=not-open;") {
		t.Fatalf("decision events = %+v, want one with the closed state", rows)
	}
}

// TestNoClosingItemNoNameAsksNothing: with nothing named there is nothing to
// ask about — the line stays the whole answer and no row is created.
func TestNoClosingItemNoNameAsksNothing(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	fake := noItemFake()
	fake.RequestsValue[0].Title = "Draft: tsim scenarios"
	fake.RequestsValue[0].Branch = "main"
	fake.RequestsValue[0].Body = "Updates the scenario web entity."
	stub := &queueStub{}
	in := noItemInput(st, pid, fake)
	in.Rig = stub

	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if !hasSkip(report, "change request #683: body closes no item") {
		t.Fatalf("skipped = %v, want the plain line", report.Skipped)
	}
	if stub.createCalls != 0 {
		t.Fatalf("createCalls = %d, want no row when nothing is named", stub.createCalls)
	}
	if rows := noItemDecisions(t, st); len(rows) != 0 {
		t.Fatalf("decision events = %+v, want none", rows)
	}
}

// TestNoClosingItemAsksOncePerHead: ten ticks over one head raise one row and
// one event; a moved head is a new fact and asks again (a resolved row does
// not silence it).
func TestNoClosingItemAsksOncePerHead(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	fake := noItemFake()
	stub := &queueStub{}
	in := noItemInput(st, pid, fake)
	in.Rig = stub
	in.WorktreeRoot = t.TempDir()

	for tick := 1; tick <= 10; tick++ {
		if _, err := RunTick(ctx, in); err != nil {
			t.Fatalf("tick %d: %v", tick, err)
		}
	}
	if stub.createCalls != 1 {
		t.Fatalf("createCalls = %d, want one row over one head", stub.createCalls)
	}
	if rows := noItemDecisions(t, st); len(rows) != 1 {
		t.Fatalf("decision events = %+v, want one", rows)
	}

	const movedHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	fake.RequestsValue[0].HeadSHA = movedHead
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("moved head tick: %v", err)
	}
	if stub.createCalls != 2 {
		t.Fatalf("createCalls = %d, want a second question for the moved head", stub.createCalls)
	}
	rows := noItemDecisions(t, st)
	if len(rows) != 2 {
		t.Fatalf("decision events = %+v, want a second question for the moved head", rows)
	}
	found := false
	for _, row := range rows {
		if strings.Contains(row.Detail, "head="+movedHead+";") {
			found = true
		}
	}
	if !found {
		t.Fatalf("decision events = %+v, want the moved head asked again", rows)
	}
}
