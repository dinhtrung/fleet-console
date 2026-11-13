package act

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/decide"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// TestRunTickRefusesToMergeOnUnattributableVerdict is the end-to-end promise:
// CaptureFromForge returning the refusal makes RunTick skip Merge for that item,
// so a withdrawal the reviewer mistyped cannot merge on an earlier accept.
func TestRunTickRefusesToMergeOnUnattributableVerdict(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fleet.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer st.Close()
	projectID, err := st.CreateProject(ctx, store.ProjectInput{
		Key: "p", Name: "P", ForgeKind: "github", ForgeProject: "o/r", Rig: "r", ConcurrencyCap: 1,
	})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	dispatchID, err := st.CreateDispatch(ctx, store.NewDispatch{
		ProjectID: projectID, ItemNumber: 5, Attempt: 1, Seat: "s",
		Branch: "b", Worktree: "/tmp/w", BaseSHA: "x", CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	const head = "abc1234def5678"
	// An earlier, genuine accept is already on the record.
	if _, err := st.AddVerdict(ctx, store.NewVerdict{
		DispatchID: dispatchID, SHA: head, Decision: "accept", Source: "note 1", At: now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("seed verdict: %v", err)
	}

	fake := &forge.Fake{
		KindValue: forge.KindGitHub,
		RequestsValue: []forge.ChangeRequest{{
			Number: 9, State: "open", HeadSHA: head, Mergeable: true,
			Body: "Closes #5", DocsOnly: true,
		}},
		NotesValue: []forge.Comment{
			{ID: "2", CreatedAt: "2026-10-30T11:30:00Z", Body: "REJECT abc12"},
			{ID: "1", CreatedAt: "2026-10-30T10:00:00Z", Body: "ACCEPT " + head},
		},
	}
	report, err := RunTick(ctx, TickInput{
		Store: st, Project: store.ProjectRow{
			ID: projectID, Key: "p", ForgeKind: "github", ForgeProject: "o/r",
			Rig: "r", ConcurrencyCap: 1, Enabled: true,
		},
		Forge: fake, Rig: &queueStub{}, Git: &fakeGit{}, Gate: openGate(), Apply: true,
		CRs: fake.RequestsValue, Plan: decide.Plan{}, Now: func() time.Time { return now },
		WorktreeRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Merges) != 0 {
		t.Fatalf("merges = %d, want none: a truncated rejection must not merge", len(report.Merges))
	}

	// The refusal path is what actually runs; prove it names the note.
	actions := &Actions{Store: st, Now: func() time.Time { return now }}
	_, capErr := actions.CaptureVerdict(ctx, dispatchID, 5, head, fake.NotesValue)
	var refusal *UnattributableVerdictError
	if !errors.As(capErr, &refusal) {
		t.Fatalf("capture = %v, want an UnattributableVerdictError", capErr)
	}
	if refusal.NoteID != "2" || !strings.Contains(capErr.Error(), "does not parse") {
		t.Fatalf("refusal = %v, want it to name note 2 and the unparseable sha", capErr)
	}
}

// decorationCapture runs one note through the real capture path at head
// 3f2a1b9c: a fresh store per call, because storeVerdict dedupes on
// (sha, decision) and the table reuses the same sha across cases.
func decorationCapture(t *testing.T, line string) (*CapturedVerdict, error) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 11, 0, 0, 0, time.UTC)
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fleet.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	projectID, err := st.CreateProject(ctx, store.ProjectInput{
		Key: "p", Name: "P", ForgeKind: "github", ForgeProject: "o/r", Rig: "r", ConcurrencyCap: 1,
	})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	dispatchID, err := st.CreateDispatch(ctx, store.NewDispatch{
		ProjectID: projectID, ItemNumber: 5, Attempt: 1, Seat: "s",
		Branch: "b", Worktree: "/tmp/w", BaseSHA: "x", CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	actions := &Actions{Store: st, ProjectID: projectID, Now: func() time.Time { return now }}
	return actions.CaptureVerdict(ctx, dispatchID, 5, "3f2a1b9c",
		[]forge.Comment{{ID: "1", CreatedAt: now.Format(time.RFC3339), Body: line}})
}

// TestStripDecoration is the normalisation itself, line for line: what each of
// the nine measured shapes reduces to, and what prose reduces to — itself.
// Trailing decoration stays, because the note regex matches the prefix.
func TestStripDecoration(t *testing.T) {
	cases := [][2]string{
		{"ACCEPT 3f2a1b9c", "ACCEPT 3f2a1b9c"},
		{"**ACCEPT 3f2a1b9c**", "ACCEPT 3f2a1b9c**"},
		{"## Verdict: ACCEPT 3f2a1b9c", "ACCEPT 3f2a1b9c"},
		{"`ACCEPT 3f2a1b9c`", "ACCEPT 3f2a1b9c`"},
		{"> ACCEPT 3f2a1b9c", "ACCEPT 3f2a1b9c"},
		{"- ACCEPT 3f2a1b9c", "ACCEPT 3f2a1b9c"},
		{"ACCEPT: 3f2a1b9c", "ACCEPT 3f2a1b9c"},
		{"ACCEPT — 3f2a1b9c", "ACCEPT 3f2a1b9c"},
		{"ACCEPT", "ACCEPT"},
		{"Not ready to ACCEPT 3f2a1b9c until CI is green", "Not ready to ACCEPT 3f2a1b9c until CI is green"},
		{"ACCEPTED 3f2a1b9c", "ACCEPTED 3f2a1b9c"},
		{"- **REJECT: aaaa111**", "REJECT aaaa111**"},
	}
	for _, tc := range cases {
		if got := stripDecoration(tc[0]); got != tc[1] {
			t.Errorf("stripDecoration(%q) = %q, want %q", tc[0], got, tc[1])
		}
	}
}

// TestCaptureVerdictDecoration is the brief's table over the nine lines
// measured in epic #263, each asserting capture or refusal through the real
// CaptureVerdict, plus the two prose sentences that must stay prose (#264).
func TestCaptureVerdictDecoration(t *testing.T) {
	const head = "3f2a1b9c"
	cases := []struct {
		name string
		line string
		want string // accept | refused | none
	}{
		{"plain", "ACCEPT " + head, "accept"},
		{"bold", "**ACCEPT " + head + "**", "accept"},
		{"heading with a label", "## Verdict: ACCEPT " + head, "accept"},
		{"code span", "`ACCEPT " + head + "`", "accept"},
		{"quote", "> ACCEPT " + head, "accept"},
		{"list bullet", "- ACCEPT " + head, "accept"},
		{"colon separator", "ACCEPT: " + head, "accept"},
		{"em dash separator", "ACCEPT — " + head, "accept"},
		{"token with no sha", "ACCEPT", "refused"},
		{"prose mentions the token", "Not ready to ACCEPT " + head + " until CI is green", "none"},
		{"past tense is not the token", "ACCEPTED " + head, "none"},
		// D2 with decoration on: the sha rule is unchanged, so a decorated
		// rejection of an older head still refuses.
		{"decorated rejection of an older head", "**REJECT aaaa111**", "refused"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verdict, err := decorationCapture(t, tc.line)
			switch tc.want {
			case "accept":
				if err != nil {
					t.Fatalf("capture: %v", err)
				}
				if verdict == nil {
					t.Fatalf("verdict = nil, want accept captured")
				}
				if verdict.Decision != "accept" {
					t.Fatalf("decision = %s, want accept", verdict.Decision)
				}
				if verdict.SHA != head {
					t.Fatalf("sha = %s, want %s — the head under review", verdict.SHA, head)
				}
			case "refused":
				if !errors.Is(err, ErrUnattributableVerdict) {
					t.Fatalf("err = %v, want ErrUnattributableVerdict", err)
				}
				if verdict != nil {
					t.Fatalf("verdict = %+v, want none", verdict)
				}
			case "none":
				if err != nil {
					t.Fatalf("err = %v, want none — prose stays prose", err)
				}
				if verdict != nil {
					t.Fatalf("verdict = %+v, want none", verdict)
				}
			}
		})
	}
}

// captureAt runs notes through the real capture path at an arbitrary head: a
// fresh store per call, because storeVerdict dedupes on (sha, decision). It
// returns the stored rows too, so a test can assert what was written, not only
// what came back.
func captureAt(t *testing.T, head string, notes []forge.Comment) (*CapturedVerdict, []store.VerdictRow, error) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fleet.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	projectID, err := st.CreateProject(ctx, store.ProjectInput{
		Key: "p", Name: "P", ForgeKind: "github", ForgeProject: "o/r", Rig: "r", ConcurrencyCap: 1,
	})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	dispatchID, err := st.CreateDispatch(ctx, store.NewDispatch{
		ProjectID: projectID, ItemNumber: 5, Attempt: 1, Seat: "s",
		Branch: "b", Worktree: "/tmp/w", BaseSHA: "x", CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	actions := &Actions{Store: st, ProjectID: projectID, Now: func() time.Time { return now }}
	verdict, err := actions.CaptureVerdict(ctx, dispatchID, 5, head, notes)
	rows, rowErr := st.Verdicts(ctx, dispatchID)
	if rowErr != nil {
		t.Fatalf("verdicts: %v", rowErr)
	}
	return verdict, rows, err
}

func TestSameSHA(t *testing.T) {
	const fullHead = "3f2a1b9c4d5e6f708192a3b4c5d6e7f8091a2b3c"
	cases := []struct {
		name string
		note string
		head string
		want bool
	}{
		{"identical full shas", fullHead, fullHead, true},
		{"note is a 7-char prefix of a 40-char head", "3f2a1b9", fullHead, true},
		{"note is a mid-length prefix", "3f2a1b9c4d5e", fullHead, true},
		{"upper case matches", "7F76634", "7f76634def5678901234abcdef56789012abcd", true},
		{"head abbreviated, note full", fullHead[:9], fullHead, true},
		{"a 6-char sha is not an abbreviation", "3f2a1b", fullHead, false},
		{"a 6-char head cannot match", "3f2a1b9", "3f2a1b", false},
		{"another commit's sha", "ffffffff", "3f2a1b9c4d5e6f708192a3b4c5d6e7f8091a2b3c", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameSHA(tc.note, tc.head); got != tc.want {
				t.Fatalf("sameSHA(%q, %q) = %t, want %t", tc.note, tc.head, got, tc.want)
			}
		})
	}
}

// TestCaptureAcceptsAbbreviatedHead: a reviewer writes git's own abbreviation,
// and the stored row carries the full head so the merge gate's exact comparison
// keeps working unchanged.
func TestCaptureAcceptsAbbreviatedHead(t *testing.T) {
	const head = "3f2a1b9c4d5e6f708192a3b4c5d6e7f8091a2b3c"
	for _, name := range []string{"lower case", "upper case"} {
		t.Run(name, func(t *testing.T) {
			note := "ACCEPT 3f2a1b9"
			if name == "upper case" {
				note = "ACCEPT 3F2A1B9"
			}
			verdict, rows, err := captureAt(t, head, []forge.Comment{
				{ID: "1", CreatedAt: "2026-11-12T12:00:00Z", Body: note},
			})
			if err != nil || verdict == nil {
				t.Fatalf("capture = (%+v, %v), want the abbreviated accept captured", verdict, err)
			}
			if len(rows) != 1 {
				t.Fatalf("verdict rows = %d, want one", len(rows))
			}
			if rows[0].SHA != head {
				t.Fatalf("stored sha = %s, want the full 40-char head %s", rows[0].SHA, head)
			}
		})
	}
}

// TestCaptureRejectsAcceptForAnotherSHA: the newest verdict-shaped note is an
// accept for a sha that does not name the head. It ends the scan with the typed
// error — an older ACCEPT at the head must not resurface — and stores nothing.
func TestCaptureRejectsAcceptForAnotherSHA(t *testing.T) {
	const head = "abc1234def5678"
	verdict, rows, err := captureAt(t, head, []forge.Comment{
		{ID: "2", CreatedAt: "2026-11-12T12:30:00Z", Body: "ACCEPT ffffffff"},
		{ID: "1", CreatedAt: "2026-11-12T12:00:00Z", Body: "ACCEPT " + head},
	})
	var unattributed *UnattributedAcceptError
	if !errors.As(err, &unattributed) {
		t.Fatalf("err = %v, want an UnattributedAcceptError", err)
	}
	if unattributed.Written != "ffffffff" || unattributed.Head != head || unattributed.NoteID != "2" {
		t.Fatalf("error = %+v, want written=ffffffff head=%s note=2", unattributed, head)
	}
	if got := unattributed.Error(); got != "ACCEPT ffffffff does not name head "+head+" (note 2)" {
		t.Fatalf("Error() = %q", got)
	}
	if verdict != nil || len(rows) != 0 {
		t.Fatalf("verdict = %+v rows = %+v, want nothing stored", verdict, rows)
	}
}

// TestCaptureStillErrorsOnRejectForAnotherSHA: the rejection path is
// deliberately stricter than the accept — it ends the scan and stores nothing.
// Its refusal is now its own typed fact (epic #309 §1.1) while staying in the
// ErrUnattributableVerdict family, so every existing caller keeps its meaning.
func TestCaptureStillErrorsOnRejectForAnotherSHA(t *testing.T) {
	const head = "abc1234def5678"
	_, rows, err := captureAt(t, head, []forge.Comment{
		{ID: "2", CreatedAt: "2026-11-12T12:30:00Z", Body: "REJECT ffffffff"},
		{ID: "1", CreatedAt: "2026-11-12T12:00:00Z", Body: "ACCEPT " + head},
	})
	var rejected *UnattributedRejectError
	if !errors.As(err, &rejected) {
		t.Fatalf("err = %v, want an UnattributedRejectError", err)
	}
	if rejected.Written != "ffffffff" || rejected.Head != head || rejected.NoteID != "2" {
		t.Fatalf("error = %+v, want written=ffffffff head=%s note=2", rejected, head)
	}
	if !errors.Is(err, ErrUnattributableVerdict) {
		t.Fatalf("err = %v, want it in the ErrUnattributableVerdict family", err)
	}
	if len(rows) != 0 {
		t.Fatalf("verdict rows = %+v, want none", rows)
	}
}

// dateRe matches a date- or timestamp-shaped token, so the wording test can
// assert the refusal does not state or imply a commit time.
var dateRe = regexp.MustCompile(`\b20\d\d[-/]\d\d|\d\d:\d\d(:\d\d)?|\bT\d\d\b`)

// TestUnattributableRejectText pins the refusal's wording (#312): the note is
// older than the head, both shas are named, and the sentence says what
// unblocks the merge — a verdict naming the head. It must not state or imply
// a commit time (stale-verdict-visibility spec), so the text carries no
// timestamp; the note's CreatedAt field stays unread for the wording.
func TestUnattributableRejectText(t *testing.T) {
	const head = "5690552c1e8f5a6d7b8c9d0e1f2a3b4c5d6e7f80"
	_, rows, err := captureAt(t, head, []forge.Comment{
		{ID: "37482", CreatedAt: "2026-11-12T09:00:00Z", Body: "REJECT a746312"},
	})
	var refusal *UnattributedRejectError
	if !errors.As(err, &refusal) {
		t.Fatalf("err = %v, want an UnattributedRejectError — the typed fact #311 added", err)
	}
	if refusal.NoteID != "37482" || refusal.Written != "a746312" || refusal.Head != head {
		t.Fatalf("refusal = %+v, want note 37482, written a746312, head %s", refusal, head)
	}
	// The sentence T3 (#312) fixes: the order, both shas, and the unblocking
	// action. No quote of the reviewer's line: T2's error carries no Line field,
	// and the note id names the note instead.
	want := fmt.Sprintf(
		"the note is older than the head: this REJECT names a746312, the head is %s; a verdict naming %s unblocks the merge (note 37482)",
		head, head)
	if got := err.Error(); got != want {
		t.Fatalf("err =\n  %s\nwant\n  %s", got, want)
	}
	// No commit time: the interface carries none, and the wording must not
	// imply one. A date-shaped token in the text would claim knowledge the
	// forge read does not have.
	if dateRe.MatchString(err.Error()) {
		t.Fatalf("err = %v, want no timestamp in the refusal", err)
	}
	if len(rows) != 0 {
		t.Fatalf("verdict rows = %+v, want none", rows)
	}
}

// TestCaptureKeepsTheNewestVerdict: newest-first still decides. An older reject
// at the head does not override a newer accept at the same head.
func TestCaptureKeepsTheNewestVerdict(t *testing.T) {
	const head = "abc1234def5678"
	verdict, rows, err := captureAt(t, head, []forge.Comment{
		{ID: "2", CreatedAt: "2026-11-12T12:30:00Z", Body: "ACCEPT " + head + " (CI green)"},
		{ID: "1", CreatedAt: "2026-11-12T12:00:00Z", Body: "REJECT " + head},
	})
	if err != nil || verdict == nil {
		t.Fatalf("capture = (%+v, %v), want the newer accept", verdict, err)
	}
	if verdict.Decision != "accept" {
		t.Fatalf("decision = %s, want accept", verdict.Decision)
	}
	if len(rows) != 1 || rows[0].Decision != "accept" {
		t.Fatalf("rows = %+v, want one accept row", rows)
	}
}

// TestUnattributableRejectIdentity pins the two error identities apart
// (epic #309 §1.1): a REJECT naming another sha is an UnattributedRejectError,
// a first line whose sha does not parse stays an UnattributableVerdictError,
// and both answer errors.Is(ErrUnattributableVerdict) — the family identity
// every existing caller relies on. The caller distinguishes them with
// errors.As, never by parsing Error().
func TestUnattributableRejectIdentity(t *testing.T) {
	const head = "abc1234def5678"
	_, _, rejectErr := captureAt(t, head, []forge.Comment{
		{ID: "2", CreatedAt: "2026-11-12T12:30:00Z", Body: "REJECT ffffffff"},
		{ID: "1", CreatedAt: "2026-11-12T12:00:00Z", Body: "ACCEPT " + head},
	})
	var rejected *UnattributedRejectError
	if !errors.As(rejectErr, &rejected) {
		t.Fatalf("reject = %v, want an UnattributedRejectError", rejectErr)
	}
	var family *UnattributableVerdictError
	if errors.As(rejectErr, &family) {
		t.Fatalf("reject classified as the parse-failure type %v, want the types apart", rejectErr)
	}
	if !errors.Is(rejectErr, ErrUnattributableVerdict) {
		t.Fatalf("reject = %v, want the family identity", rejectErr)
	}

	_, _, parseErr := captureAt(t, head, []forge.Comment{
		{ID: "2", CreatedAt: "2026-11-12T12:30:00Z", Body: "REJECT"},
	})
	if !errors.As(parseErr, &family) {
		t.Fatalf("parse failure = %v, want an UnattributableVerdictError", parseErr)
	}
	if errors.As(parseErr, &rejected) {
		t.Fatalf("parse failure classified as a rejection %v, want the types apart", parseErr)
	}
	if !errors.Is(parseErr, ErrUnattributableVerdict) {
		t.Fatalf("parse failure = %v, want the family identity", parseErr)
	}
}
