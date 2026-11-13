package act

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/decide"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// malformedFixture builds the tick the board actually saw (#346): one open
// change request whose newest note is the decorated, sha-less verdict
// `## QA Verdict: ACCEPT ✅`. The store is fresh so event counts are exact;
// the caller owns closing it.
func malformedFixture(t *testing.T, note string) (*store.Store, *forge.Fake) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)

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
	if _, err := st.CreateDispatch(ctx, store.NewDispatch{
		ProjectID: projectID, ItemNumber: 5, Attempt: 1, Seat: "s",
		Branch: "b", Worktree: "/tmp/w", BaseSHA: "x", CreatedAt: now,
	}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("ctx: %v", err)
	}
	fake := &forge.Fake{
		KindValue: forge.KindGitHub,
		RequestsValue: []forge.ChangeRequest{{
			Number: 9, State: "open", HeadSHA: "abc1234def5678", Mergeable: true,
			Body: "Closes #5", DocsOnly: true,
		}},
		NotesValue: []forge.Comment{
			{ID: "2", CreatedAt: "2026-11-13T09:30:00Z", Body: note},
		},
	}
	return st, fake
}

// runMalformedTick is one act tick over the fixture, the shape
// TestRunTickRefusesToMergeOnUnattributableVerdict runs.
func runMalformedTick(t *testing.T, st *store.Store, fake *forge.Fake, now time.Time) TickReport {
	t.Helper()
	report, err := RunTick(context.Background(), TickInput{
		Store: st, Project: store.ProjectRow{
			ID: 1, Key: "p", ForgeKind: "github", ForgeProject: "o/r",
			Rig: "r", ConcurrencyCap: 1, Enabled: true,
		},
		Forge: fake, Rig: &queueStub{created: &rig.CreateRequest{}}, Git: &fakeGit{}, Gate: openGate(), Apply: true,
		CRs: fake.RequestsValue, Plan: decide.Plan{}, Now: func() time.Time { return now },
		WorktreeRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	return report
}

// malformedEvents counts the audit rows of one kind for item 5.
func malformedEvents(t *testing.T, st *store.Store, kind string) []store.EventRow {
	t.Helper()
	page, err := st.Events(context.Background(), store.EventFilter{Kind: kind, Limit: 50})
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	return page.Events
}

// TestMalformedVerdictRecordedOncePrintsOnce is the tick-one half: the note
// `## QA Verdict: ACCEPT ✅` is a real fact about the review, so the tick
// writes exactly one `verdict.malformed` row whose detail is the fixed
// vocabulary — `note=<id>; head=<sha>; line=<first line>` with the head from
// the change request, never from the note — and still prints the skip line,
// because this tick is the one that wrote the row.
func TestMalformedVerdictRecordedOncePrintsOnce(t *testing.T) {
	st, fake := malformedFixture(t, "## QA Verdict: ACCEPT ✅")
	report := runMalformedTick(t, st, fake, time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC))

	events := malformedEvents(t, st, "verdict.malformed")
	if len(events) != 1 {
		t.Fatalf("verdict.malformed rows = %d, want exactly one", len(events))
	}
	e := events[0]
	if e.Subject != "#5" || e.Actor != "act" {
		t.Fatalf("row = subject %q actor %q, want #5 by act", e.Subject, e.Actor)
	}
	const want = "note=2; head=abc1234def5678; line=## QA Verdict: ACCEPT ✅"
	if e.Detail != want {
		t.Fatalf("detail = %q, want %q", e.Detail, want)
	}

	// The first tick prints the line: silence is only for a note already on
	// the ledger, and this note was not.
	skipped := strings.Join(report.Skipped, "\n")
	if !strings.Contains(skipped, "#5 verdict:") || !strings.Contains(skipped, "note 2") {
		t.Fatalf("skipped = %q, want the #5 verdict line naming note 2", skipped)
	}
}

// TestMalformedVerdictSecondTickPrintsNothing is the once, not every tick:
// a second tick over the same note writes no second row and prints no line —
// report.Skipped is empty — because HasEvent finds `note=2;` and alreadyNoted
// turns the print off while the refusal keeps the merge blocked.
func TestMalformedVerdictSecondTickPrintsNothing(t *testing.T) {
	st, fake := malformedFixture(t, "## QA Verdict: ACCEPT ✅")
	first := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	runMalformedTick(t, st, fake, first)

	second := runMalformedTick(t, st, fake, first.Add(time.Minute))
	if n := len(malformedEvents(t, st, "verdict.malformed")); n != 1 {
		t.Fatalf("verdict.malformed rows after two ticks = %d, want one", n)
	}
	if len(second.Skipped) != 0 {
		t.Fatalf("second tick report.Skipped = %q, want nothing", second.Skipped)
	}
}

// TestUnattributedAcceptStaysUnattributed keeps the boundary of the new kind:
// a note that names a *different* sha is the #289 T2 case, not a malformed
// one — it still writes `verdict.unattributed` and no `verdict.malformed`
// row exists for the item.
func TestUnattributedAcceptStaysUnattributed(t *testing.T) {
	st, fake := malformedFixture(t, "ACCEPT deadbee0bad0")
	report := runMalformedTick(t, st, fake, time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC))

	if n := len(malformedEvents(t, st, "verdict.malformed")); n != 0 {
		t.Fatalf("verdict.malformed rows = %d, want none for an accept naming another sha", n)
	}
	events := malformedEvents(t, st, "verdict.unattributed")
	if len(events) != 1 {
		t.Fatalf("verdict.unattributed rows = %d, want one", len(events))
	}
	if !strings.Contains(events[0].Detail, "sha=deadbee0bad0;") {
		t.Fatalf("unattributed detail = %q, want the written sha", events[0].Detail)
	}
	if len(report.Skipped) == 0 {
		t.Fatalf("skipped = %q, want the #5 verdict line on the first tick", report.Skipped)
	}
}
