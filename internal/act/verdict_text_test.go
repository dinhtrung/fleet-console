package act

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// TestScanVerdictText pins the text scan's rules: a verdict word that opens a
// line after decoration, a link, an icon and one short label is a verdict; a
// word inside a sentence is not; anything the scan cannot attribute is a
// question, never a guess.
func TestScanVerdictText(t *testing.T) {
	const head = "3f2a1b9c"
	cases := []struct {
		name string
		body string
		want string // accept | reject | question | none
	}{
		{"the shape that broke #368", "QA ACCEPT " + head, "accept"},
		{"heading and label", "## QA Verdict: ACCEPT " + head, "accept"},
		{"icon and bold", "✅ **ACCEPT " + head + "**", "accept"},
		{"link before the token", "[ACCEPT](https://example.test/x) " + head, "accept"},
		{"sha on the next line", "Verdict:\n\nACCEPT\n\n" + head, "accept"},
		{"verdict after a paragraph", "Reviewed the diff.\n\nACCEPT " + head, "accept"},
		{"reject with a trailing reason", "REJECT " + head + " (CI red)", "reject"},
		{"short sha that prefixes the head", "ACCEPT 3f2a1b", "accept"},
		{"short sha that names nothing", "ACCEPT ffff", "none"},
		{"prose about a verdict stays prose", "Not ready to ACCEPT " + head + " until CI is green", "none"},
		{"past tense is not the token", "ACCEPTED " + head, "none"},
		{"both polarities", "ACCEPT " + head + "\n\nREJECT " + head, "question"},
		{"a negated verdict", "Verdict: not ACCEPT " + head, "question"},
		{"a verdict with no commit", "ACCEPT", "question"},
		{"a verdict about another commit", "ACCEPT deadbee", "accept"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scan := scanVerdictText(tc.body, head)
			switch tc.want {
			case "none":
				if scan.Reason != "" || scan.Decision != "" {
					t.Fatalf("scan = %+v, want no verdict", scan)
				}
			case "question":
				// A question is either the scan's own reason (both, negated) or a
				// decision with no commit for the caller to ask about.
				if scan.Reason == "" && !(scan.Decision != "" && scan.SHA == "") {
					t.Fatalf("scan = %+v, want a question", scan)
				}
			default:
				if scan.Reason != "" {
					t.Fatalf("scan = %+v, want %s, got a question", scan, tc.want)
				}
				if scan.Decision != tc.want {
					t.Fatalf("decision = %q, want %q", scan.Decision, tc.want)
				}
			}
		})
	}
}

// TestScanVerdictTextReadsOnlyTheWindow pins the 1024-character bound: a verdict
// below the cut is not read, so a note that opens with a wall of prose cannot
// smuggle a verdict in from a page down.
func TestScanVerdictTextReadsOnlyTheWindow(t *testing.T) {
	const head = "3f2a1b9c"
	body := strings.Repeat("x", verdictWindow) + "\nACCEPT " + head
	if scan := scanVerdictText(body, head); scan.Decision != "" || scan.Reason != "" {
		t.Fatalf("scan = %+v, want nothing past the window", scan)
	}
}

// TestRunTickAsksOperatorForUnresolvedVerdict is the behaviour #368/#369 needed:
// a note that reads like a verdict but cannot be attributed raises exactly one
// question row — to the operator seat when the project has one, to
// human@kernel otherwise — and a second tick over the same note does not ask
// again.
func TestRunTickAsksOperatorForUnresolvedVerdict(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 13, 14, 0, 0, 0, time.UTC)
	st := newStore(t)
	pid := newProject(t, st)

	const itemNumber, changeRequestNumber = 63, 75
	if _, err := st.CreateDispatch(ctx, store.NewDispatch{
		ProjectID: pid, ItemNumber: itemNumber, Attempt: 1, Seat: "seat@rig",
		Branch: "feature/63-x", Worktree: "wt", BaseSHA: "base", CreatedAt: now,
	}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	fake := forge.NewFake(forge.KindGitHub)
	fake.ItemsValue = []forge.Item{{Number: itemNumber, Kind: forge.KindTask, State: forge.StateOpen, Title: "Sixty-three"}}
	fake.RequestsValue = []forge.ChangeRequest{{
		Number: changeRequestNumber, HeadSHA: "d00f0dd", Mergeable: true,
		Body: "Closes #63", DocsOnly: true,
	}}
	// The note says ACCEPT but names no commit: a question, not a merge.
	fake.NotesValue = []forge.Comment{{
		ID: "n1", Body: "QA ACCEPT", CreatedAt: "2026-11-13T13:00:00Z",
	}}

	queue := &queueStub{item: &rig.QueueItem{ID: "qitem-1", State: "pending"}, created: &rig.CreateRequest{}}
	project := store.ProjectRow{ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", ConcurrencyCap: 2, Enabled: true}
	in := TickInput{
		Store: st, Project: project, Forge: fake, Rig: queue, Git: &fakeGit{},
		Gate: openGate(), Apply: true, Items: fake.ItemsValue, CRs: fake.RequestsValue,
		Now: func() time.Time { return now }, WorktreeRoot: t.TempDir(),
	}
	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Merges) != 0 {
		t.Fatalf("merges = %+v, want none: the note names no commit", report.Merges)
	}
	if queue.createCalls != 1 {
		t.Fatalf("queue create calls = %d, want one question", queue.createCalls)
	}
	if queue.created.Destination != "human@kernel" {
		t.Fatalf("destination = %q, want human@kernel without a relay", queue.created.Destination)
	}

	// The same note on the next tick is the same question: the ledger dedupes it.
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if queue.createCalls != 1 {
		t.Fatalf("queue create calls = %d after two ticks, want one", queue.createCalls)
	}
}
