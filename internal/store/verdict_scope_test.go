package store

import (
	"context"
	"testing"
	"time"
)

// TestVerdictsForProjectDoesNotLeakAdoptedVerdicts is the reviewer's finding on
// PR #140 at b42aed8: VerdictsForProject resolved the project through the item
// mirror, and an adopted verdict has no dispatch, so any *other* project holding
// an item with the same number matched too.
//
// Two projects both hold item 7. Alpha has an adopted ACCEPT at `head-a`. Beta
// has an adopted ACCEPT at `head-b`. Beta's read must see only beta's row —
// otherwise beta's board reports an ACCEPT for a sha that is not its head, which
// is the path that raises a decision.
func TestVerdictsForProjectDoesNotLeakAdoptedVerdicts(t *testing.T) {
	ctx := context.Background()
	st := upsertStore(t)
	at := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

	alpha, err := st.CreateProject(ctx, ProjectInput{
		Key: "alpha", Name: "alpha", ForgeKind: "github", ForgeProject: "o/alpha",
		Rig: "rig", ConcurrencyCap: 1, Enabled: true,
	})
	if err != nil {
		t.Fatalf("alpha: %v", err)
	}
	beta, err := st.CreateProject(ctx, ProjectInput{
		Key: "beta", Name: "beta", ForgeKind: "github", ForgeProject: "o/beta",
		Rig: "rig", ConcurrencyCap: 1, Enabled: true,
	})
	if err != nil {
		t.Fatalf("beta: %v", err)
	}

	// Both projects mirror an item numbered 7, which is what makes the item-number
	// join ambiguous.
	for _, project := range []int64{alpha, beta} {
		if err := st.ReplaceItems(ctx, project, []ItemRecord{{
			Number: 7, Kind: "task", State: "open", Title: "Seven", URL: "u", UpdatedAt: at,
		}}); err != nil {
			t.Fatalf("items for %d: %v", project, err)
		}
	}

	// Adopted verdicts: no dispatch, so the only thing that can tell them apart is
	// the project the row was written for.
	if _, err := st.AddVerdict(ctx, NewVerdict{
		ProjectID: alpha, ItemNumber: 7, SHA: "head-a", Decision: "accept", Source: "note a", At: at,
	}); err != nil {
		t.Fatalf("alpha verdict: %v", err)
	}
	if _, err := st.AddVerdict(ctx, NewVerdict{
		ProjectID: beta, ItemNumber: 7, SHA: "head-b", Decision: "accept", Source: "note b", At: at.Add(time.Minute),
	}); err != nil {
		t.Fatalf("beta verdict: %v", err)
	}

	verdicts, err := st.VerdictsForProject(ctx, beta)
	if err != nil {
		t.Fatalf("VerdictsForProject(beta): %v", err)
	}
	if len(verdicts) != 1 {
		t.Fatalf("beta verdicts = %+v, want exactly its own row", verdicts)
	}
	if verdicts[0].SHA != "head-b" {
		t.Fatalf("beta verdict sha = %q, want head-b (alpha's head-a leaked in)", verdicts[0].SHA)
	}

	// The same read for alpha must return alpha's row, so the fix is a scope and
	// not a filter that hides adopted verdicts entirely.
	alphaVerdicts, err := st.VerdictsForProject(ctx, alpha)
	if err != nil {
		t.Fatalf("VerdictsForProject(alpha): %v", err)
	}
	if len(alphaVerdicts) != 1 || alphaVerdicts[0].SHA != "head-a" {
		t.Fatalf("alpha verdicts = %+v, want exactly head-a", alphaVerdicts)
	}
}

// TestVerdictsForProjectKeepsDispatchedVerdicts guards the other half of the read:
// a verdict written for a dispatch must still appear for its project even when the
// dispatch no longer exists in the item mirror, because that is how the board and
// the mirror's accepted-sha set work today.
func TestVerdictsForProjectKeepsDispatchedVerdicts(t *testing.T) {
	ctx := context.Background()
	st := upsertStore(t)
	at := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

	id, err := st.CreateProject(ctx, ProjectInput{
		Key: "app", Name: "app", ForgeKind: "github", ForgeProject: "o/app",
		Rig: "rig", ConcurrencyCap: 1, Enabled: true,
	})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	dispatchID, err := st.CreateDispatch(ctx, NewDispatch{
		ProjectID: id, ItemNumber: 7, Attempt: 1, Seat: "s", Branch: "b", Worktree: "w", BaseSHA: "base", CreatedAt: at,
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	// A dispatched verdict written without ProjectID, which is how every existing
	// row and every seed helper writes it.
	if _, err := st.AddVerdict(ctx, NewVerdict{
		DispatchID: dispatchID, SHA: "abc123", Decision: "accept", Source: "review", At: at,
	}); err != nil {
		t.Fatalf("verdict: %v", err)
	}

	verdicts, err := st.VerdictsForProject(ctx, id)
	if err != nil {
		t.Fatalf("VerdictsForProject: %v", err)
	}
	if len(verdicts) != 1 || verdicts[0].SHA != "abc123" {
		t.Fatalf("verdicts = %+v, want the dispatched verdict found through its dispatch", verdicts)
	}
}
