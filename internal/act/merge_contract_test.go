package act

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"strings"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// ledgerFailure returns a store whose event table has been dropped, so every
// `a.event` call fails for real. A ledger failure is otherwise unreachable in a
// test, and a fake would prove only that the fake was called.
func ledgerFailure(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fleet.db")
	st, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`DROP TABLE event`); err != nil {
		t.Fatalf("drop event: %v", err)
	}
	return st
}

// mergeFixture is one dispatch with a mergeable change request at a matching head.
func mergeFixture(t *testing.T, db *store.Store) (project store.ProjectRow, dispatchID int64, request forge.ChangeRequest) {
	t.Helper()
	ctx := context.Background()
	pid, err := db.CreateProject(ctx, store.ProjectInput{
		Key: "app", Name: "app", ForgeKind: "github", ForgeProject: "o/r",
		Rig: "rig", ConcurrencyCap: 2, Enabled: true,
	})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	dispatchID, err = db.CreateDispatch(ctx, store.NewDispatch{
		ProjectID: pid, ItemNumber: 5, Attempt: 1, Seat: "seat@rig",
		Branch: "feature/5", Worktree: "wt", BaseSHA: "base", State: "dispatched",
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	const head = "abc1234"
	if _, err := db.AddVerdict(ctx, store.NewVerdict{
		DispatchID: dispatchID, SHA: head, Decision: "accept", Source: "test",
	}); err != nil {
		t.Fatalf("verdict: %v", err)
	}
	row, err := db.ProjectByKey(ctx, "app")
	if err != nil || row == nil {
		t.Fatalf("project read: %v", err)
	}
	project = *row
	return project, dispatchID, forge.ChangeRequest{
		Number: 9, State: "open", HeadSHA: head, Mergeable: true, Body: "Closes #5",
		Checks: []forge.Check{{Name: "go", Conclusion: "success"}},
	}
}

// TestMergeRefusesWhenTheLedgerFailsBeforeTheMerge is the contract's first half:
// a ledger failure before the merge still refuses, because the pull request is
// untouched and the next tick retries.
func TestMergeRefusesWhenTheLedgerFailsBeforeTheMerge(t *testing.T) {
	db := ledgerFailure(t)
	_, dispatchID, request := mergeFixture(t, db)
	draft := request
	draft.Draft = true

	fake := &forge.Fake{RequestsValue: []forge.ChangeRequest{draft}}
	a := &Actions{Store: db, Forge: fake}

	got, err := a.Merge(context.Background(), dispatchID, 5)
	if err == nil {
		t.Fatalf("merge returned nil error with a dead ledger; result = %+v", got)
	}
	if got != nil {
		t.Fatalf("result = %+v, want nil before the merge lands", got)
	}
	// The refusal must come before the merge: the forge was never asked to merge.
	if hasWrite(fake.Writes, "merge", draft.Number, "") {
		t.Fatalf("writes = %+v, want no merge attempted", fake.Writes)
	}
	// The flip itself did happen; refusing here is why that is still recoverable.
	if !hasWrite(fake.Writes, "ready", draft.Number, "") {
		t.Fatalf("writes = %+v, want the draft flip recorded before the refusal", fake.Writes)
	}
}

// TestMergeReportsGapsAfterTheMergeLanded is the contract's second half: once
// the merge has landed, a dead ledger must NOT fail the call, because the return
// value describes the merge. The failures are reported instead.
func TestMergeReportsGapsAfterTheMergeLanded(t *testing.T) {
	db := ledgerFailure(t)
	_, dispatchID, request := mergeFixture(t, db)

	fake := &forge.Fake{RequestsValue: []forge.ChangeRequest{request}}
	a := &Actions{Store: db, Forge: fake}

	got, err := a.Merge(context.Background(), dispatchID, 5)
	if err != nil {
		t.Fatalf("merge error = %v, want nil: the merge landed and must not be reported as failed", err)
	}
	if got == nil || !got.Merged {
		t.Fatalf("result = %+v, want Merged", got)
	}
	if !hasWrite(fake.Writes, "merge", request.Number, request.HeadSHA) {
		t.Fatalf("writes = %+v, want the merge at the verdict sha", fake.Writes)
	}
	if len(got.AuditGaps) == 0 {
		t.Fatal("AuditGaps is empty: a dead ledger produced no reported gap")
	}
}

// TestTickNamesAuditGaps: the caller must say so, or a gap is silent again one
// layer up. The merge is still reported as a merge.
func TestTickNamesAuditGaps(t *testing.T) {
	db := ledgerFailure(t)
	project, _, request := mergeFixture(t, db)
	fake := &forge.Fake{RequestsValue: []forge.ChangeRequest{request}}
	report, err := RunTick(context.Background(), TickInput{
		Store: db, Project: project, Forge: fake, Rig: &queueStub{}, Git: &fakeGit{},
		Gate: openGate(), Apply: true, Rows: nil, CRs: []forge.ChangeRequest{request},
		Items: []forge.Item{{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Five"}},
	})
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Merges) != 1 {
		t.Fatalf("merges = %+v, want the merge recorded despite the dead ledger", report.Merges)
	}
	found := false
	for _, skipped := range report.Skipped {
		if strings.Contains(skipped, "audit gap") {
			found = true
		}
	}
	if !found {
		t.Fatalf("skipped = %+v, want an audit-gap line", report.Skipped)
	}
}

// TestMergeReportsGapsWhenAPostMergeForgeWriteFails closes the boundary the
// ledger tests left open: Finalize runs after Forge.Merge, so a CloseItem or
// SetMarker failure there must be a gap too. Measured before the change: err
// true, merged true, no gaps — a landed merge reported as waiting.
func TestMergeReportsGapsWhenAPostMergeForgeWriteFails(t *testing.T) {
	db := newStore(t)
	_, dispatchID, request := mergeFixture(t, db)
	fake := &forge.Fake{
		RequestsValue: []forge.ChangeRequest{request},
		// Item 5 is open, so Finalize attempts CloseItem.
		ItemsValue: []forge.Item{{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Five"}},
		CloseErr:   errors.New("forge: close refused"),
	}
	a := &Actions{Store: db, Forge: fake}

	got, err := a.Merge(context.Background(), dispatchID, 5)
	if err != nil {
		t.Fatalf("error = %v, want nil: the merge landed and a post-merge forge write failed", err)
	}
	if got == nil || !got.Merged {
		t.Fatalf("result = %+v, want Merged", got)
	}
	if !hasWrite(fake.Writes, "merge", request.Number, request.HeadSHA) {
		t.Fatalf("writes = %+v, want the merge at the verdict sha", fake.Writes)
	}
	var named bool
	for _, g := range got.AuditGaps {
		if strings.Contains(g, "post-merge finalize") {
			named = true
		}
	}
	if !named {
		t.Fatalf("AuditGaps = %v, want the finalize failure named", got.AuditGaps)
	}
}
