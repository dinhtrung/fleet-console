package act

import (
	"context"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// TestUnknownMergeabilityWaitsWithoutRepair is epic #341 §1 (design D2): a
// read taken while the forge was still computing mergeability is waiting, not
// a refusal — no NotMergeableError, no decision.merge_repair row, no blocked
// merge. The first tick whose read carries a computed word takes the normal
// path: `dirty` refuses with the forge's own word and raises exactly one
// repair decision.
func TestUnknownMergeabilityWaitsWithoutRepair(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	st := newStore(t)
	pid := newProject(t, st)

	dispatchID, err := st.CreateDispatch(ctx, store.NewDispatch{
		ProjectID: pid, ItemNumber: 5, Attempt: 1, Seat: "seat@rig",
		Branch: "feature/5-x", Worktree: "wt", BaseSHA: "base", CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if _, err := st.AddVerdict(ctx, store.NewVerdict{
		DispatchID: dispatchID, SHA: "abc1234", Decision: "accept", Source: "note 1", At: now,
	}); err != nil {
		t.Fatalf("verdict: %v", err)
	}

	fake := forge.NewFake(forge.KindGitHub)
	fake.ItemsValue = []forge.Item{{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Five"}}
	// The word the row's incident carried: mergeability null on both reads —
	// the forge has not scored the pull request yet. An empty MergeState is
	// what today's refetch produces for that window.
	fake.RequestsValue = []forge.ChangeRequest{{
		Number: 9, HeadSHA: "abc1234", Mergeable: false, MergeState: "",
		Branch: "feature/5-x", Body: "Closes #5", DocsOnly: true,
	}}
	queue := &queueStub{item: &rig.QueueItem{ID: "qitem-repair", State: "pending"}}
	project := store.ProjectRow{
		ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", Enabled: true,
	}
	in := TickInput{
		Store: st, Project: project, Forge: fake, Rig: queue, Git: &fakeGit{},
		Gate: openGate(), Apply: true, Items: fake.ItemsValue, CRs: fake.RequestsValue,
		WorktreeRoot: t.TempDir(), Now: func() time.Time { return now },
	}

	// Tick during the unknown window: one grep-able waiting line and nothing
	// else — no merge, no refusal, no repair, no page.
	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("unknown tick: %v", err)
	}
	if len(report.Merges) != 0 || len(report.BlockedMerges) != 0 {
		t.Fatalf("merges=%+v blocked=%+v, want the tick to wait, not act", report.Merges, report.BlockedMerges)
	}
	if !hasSkip(report, "#5 merge waiting: the forge has not computed mergeability") {
		t.Fatalf("skipped = %v, want the waiting line", report.Skipped)
	}
	if queue.createCalls != 0 {
		t.Fatalf("queue creates = %d, want no repair row for a missing answer", queue.createCalls)
	}
	events, _ := st.RecentEvents(ctx, 20)
	for _, event := range events {
		if event.Kind == "decision.merge_repair" {
			t.Fatalf("event = %+v, want no repair raised while the forge has not answered", event)
		}
	}
	if hasWrite(fake.Writes, "merge", 9, "") {
		t.Fatalf("merged a head the forge never scored: %+v", fake.Writes)
	}

	// The computed answer arrives: `dirty` refuses with the forge's word and
	// raises exactly one repair decision.
	fake.RequestsValue[0].MergeState = "dirty"
	report, err = RunTick(ctx, in)
	if err != nil {
		t.Fatalf("computed tick: %v", err)
	}
	if len(report.Merges) != 0 {
		t.Fatalf("merges = %+v, want a dirty head refused", report.Merges)
	}
	if !hasSkip(report, "not mergeable (dirty)") {
		t.Fatalf("skipped = %v, want the refusal to quote the forge's word", report.Skipped)
	}
	if queue.createCalls != 1 {
		t.Fatalf("queue creates = %d, want exactly one repair row once the answer is computed", queue.createCalls)
	}
	if len(report.BlockedMerges) != 1 {
		t.Fatalf("blocked = %+v, want the computed conflict paged", report.BlockedMerges)
	}
}
