package act

import (
	"context"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// twoProjects is the configured state of the live box: two projects whose item
// numbers overlap. Item numbers are unique only within a project
// (store/schema.go: UNIQUE(project_id, number)), so every verdict read must name
// the project it is about.
func twoProjects(t *testing.T) (*store.Store, int64, int64) {
	t.Helper()
	ctx := context.Background()
	st := newStore(t)
	alpha, err := st.CreateProject(ctx, store.ProjectInput{
		Key: "alpha", Name: "alpha", ForgeKind: "github", ForgeProject: "o/alpha",
		Rig: "rig", ConcurrencyCap: 2, Enabled: true,
	})
	if err != nil {
		t.Fatalf("alpha: %v", err)
	}
	beta, err := st.CreateProject(ctx, store.ProjectInput{
		Key: "beta", Name: "beta", ForgeKind: "github", ForgeProject: "o/beta",
		Rig: "rig", ConcurrencyCap: 2, Enabled: true,
	})
	if err != nil {
		t.Fatalf("beta: %v", err)
	}
	return st, alpha, beta
}

// TestForeignVerdictSuppressesValidAdoption is the reviewer's probe (a): alpha's
// newer verdict for *its own* item 5 shadows beta's, so beta's valid adoption is
// refused and the skip line blames beta's head sha.
//
// The order matters and is the real one: beta captures its own ACCEPT first (as a
// rejected merge would leave it), then alpha records a newer row for the same item
// number. With the read unscoped, verdicts[0] is alpha's row.
func TestForeignVerdictSuppressesValidAdoption(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	st, alpha, beta := twoProjects(t)

	// Beta's own ACCEPT at the head under review, recorded first.
	if _, err := st.AddVerdict(ctx, store.NewVerdict{
		DispatchID: 0, ProjectID: beta, ItemNumber: 5, SHA: "bbbbbbb", Decision: "accept",
		Source: "note nB", At: now,
	}); err != nil {
		t.Fatalf("beta verdict: %v", err)
	}

	// Alpha records a newer verdict for its own item 5 after beta's, which is what
	// puts alpha's row first in an unscoped newest-first read.
	if _, err := st.AddVerdict(ctx, store.NewVerdict{
		DispatchID: 0, ProjectID: alpha, ItemNumber: 5, SHA: "aaaaaaa", Decision: "accept",
		Source: "note nA", At: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("alpha verdict: %v", err)
	}

	fake := forge.NewFake(forge.KindGitHub)
	fake.ItemsValue = []forge.Item{{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Five"}}
	fake.RequestsValue = []forge.ChangeRequest{{
		Number: 77, HeadSHA: "bbbbbbb", Branch: "feature/5-beta",
		Mergeable: true, Body: "Closes #5", DocsOnly: true,
	}}
	fake.NotesValue = []forge.Comment{{ID: "nB", Body: "ACCEPT bbbbbbb", CreatedAt: "2026-10-29T11:00:00Z"}}

	project := store.ProjectRow{
		ID: beta, Key: "beta", ForgeKind: "github", ForgeProject: "o/beta",
		Rig: "rig", ConcurrencyCap: 2, Enabled: true,
	}
	in := TickInput{
		Store: st, Project: project, Forge: fake, Rig: &queueStub{}, Git: &fakeGit{},
		Gate: openGate(), Apply: true, Items: fake.ItemsValue, CRs: fake.RequestsValue,
		Now: func() time.Time { return now },
	}

	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Merges) != 1 {
		t.Fatalf("beta did not adopt its own item: merges=%d skipped=%v", len(report.Merges), report.Skipped)
	}
	if !hasWrite(fake.Writes, "merge", 77, "bbbbbbb") {
		t.Fatalf("writes = %+v, want beta's merge at bbbbbbb", fake.Writes)
	}
}

// TestForeignVerdictSuppressesCapture is the reviewer's probe (b): the capture
// dedupe reads the unscoped table, finds alpha's row with the same sha+decision,
// returns "already stored", and beta's ACCEPT is never written — silently, while
// the merge still happens.
func TestForeignVerdictSuppressesCapture(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	st, alpha, beta := twoProjects(t)

	// Alpha's row carries the same sha and decision beta's reviewer is about to
	// post, which is what makes the dedupe believe the verdict is already stored.
	if _, err := st.AddVerdict(ctx, store.NewVerdict{
		DispatchID: 0, ProjectID: alpha, ItemNumber: 5, SHA: "abc1234", Decision: "accept",
		Source: "note nA", At: now,
	}); err != nil {
		t.Fatalf("alpha verdict: %v", err)
	}

	fake := forge.NewFake(forge.KindGitHub)
	fake.ItemsValue = []forge.Item{{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Five"}}
	fake.RequestsValue = []forge.ChangeRequest{{
		Number: 77, HeadSHA: "abc1234", Branch: "feature/5-beta",
		Mergeable: true, Body: "Closes #5", DocsOnly: true,
	}}
	fake.NotesValue = []forge.Comment{{ID: "nB", Body: "ACCEPT abc1234", CreatedAt: "2026-10-29T11:00:00Z"}}

	project := store.ProjectRow{
		ID: beta, Key: "beta", ForgeKind: "github", ForgeProject: "o/beta",
		Rig: "rig", ConcurrencyCap: 2, Enabled: true,
	}
	in := TickInput{
		Store: st, Project: project, Forge: fake, Rig: &queueStub{}, Git: &fakeGit{},
		Gate: openGate(), Apply: true, Items: fake.ItemsValue, CRs: fake.RequestsValue,
		Now: func() time.Time { return now },
	}

	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	// The spec: "A verdict is recorded whether or not the merge proceeds."
	if len(report.Verdicts) != 1 {
		t.Fatalf("verdicts captured = %d, want 1 (beta's own note); skipped=%v merges=%d",
			len(report.Verdicts), report.Skipped, len(report.Merges))
	}
	rows, err := st.VerdictsForItem(ctx, beta, 5)
	if err != nil {
		t.Fatalf("verdicts: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("beta verdict rows = %d, want 1", len(rows))
	}
	if rows[0].Source != "note nB" {
		t.Fatalf("beta verdict source = %q, want beta's own note", rows[0].Source)
	}
}
