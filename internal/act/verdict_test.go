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
