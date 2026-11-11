package act

import (
	"context"
	"errors"
	"path/filepath"
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
