package act

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/decide"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// TestMergeRepair covers #251: an accepted change request that cannot merge
// because its head conflicts with the default branch must raise exactly one
// repair decision per (item, head sha) instead of skipping forever.
func TestMergeRepair(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	st := newStore(t)
	pid := newProject(t, st)

	// One dispatch, one verdict: accept at head abc1234.
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

	// The change request is accepted but not mergeable, and the forge names why.
	fake := forge.NewFake(forge.KindGitHub)
	fake.ItemsValue = []forge.Item{{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Five"}}
	fake.RequestsValue = []forge.ChangeRequest{{
		Number: 9, HeadSHA: "abc1234", Mergeable: false, MergeState: "dirty",
		Branch: "feature/5-x", Body: "Closes #5", DocsOnly: true,
	}}
	queue := &queueStub{item: &rig.QueueItem{ID: "qitem-repair", State: "pending"}}
	raised := []DecisionRaised{}
	project := store.ProjectRow{
		ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", Enabled: true,
	}
	in := TickInput{
		Store: st, Project: project, Forge: fake, Rig: queue, Git: &fakeGit{},
		Gate: openGate(), Apply: true, Items: fake.ItemsValue, CRs: fake.RequestsValue,
		WorktreeRoot: t.TempDir(), Now: func() time.Time { return now },
		OnDecision: func(d DecisionRaised) { raised = append(raised, d) },
	}

	// Tick 1: the merge is refused, the skip names the forge's reason, and one
	// repair row exists.
	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	if len(report.Merges) != 0 {
		t.Fatalf("merges = %+v, want none", report.Merges)
	}
	if !hasSkip(report, "not mergeable (dirty)") {
		skipped := report.Skipped
		t.Fatalf("skipped = %v, want the skip to name the mergeability reason", skipped)
	}
	if queue.createCalls != 1 {
		t.Fatalf("queue creates = %d, want exactly one repair row", queue.createCalls)
	}
	if len(report.Decisions) != 1 || report.Decisions[0] != "qitem-repair" {
		t.Fatalf("decisions = %v, want the repair row", report.Decisions)
	}
	if len(raised) != 1 {
		t.Fatalf("OnDecision fired %d times, want once", len(raised))
	}
	// The audit log carries the repair under the act actor.
	events, _ := st.RecentEvents(ctx, 20)
	repairKind := 0
	for _, event := range events {
		if event.Kind == "decision.merge_repair" {
			repairKind++
		}
	}
	if repairKind != 1 {
		t.Fatalf("decision.merge_repair events = %d, want 1 in %+v", repairKind, events)
	}
	// No merge write happened.
	if hasWrite(fake.Writes, "merge", 9, "") {
		t.Fatalf("merged a conflicting head: %+v", fake.Writes)
	}

	// Tick 2 at the same head: no duplicate row.
	report, err = RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	if queue.createCalls != 1 {
		t.Fatalf("queue creates after tick 2 = %d, want still 1", queue.createCalls)
	}
	if len(report.Decisions) != 0 {
		t.Fatalf("decisions on tick 2 = %v, want none", report.Decisions)
	}

	// Tick 3: the head moved (rebased). The verdict no longer matches, so the
	// merge still refuses — but on the sha mismatch, not the conflict.
	fake.RequestsValue[0].HeadSHA = "def5678"
	fake.RequestsValue[0].Mergeable = true
	fake.RequestsValue[0].MergeState = "clean"
	report, err = RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick 3: %v", err)
	}
	if queue.createCalls != 1 {
		t.Fatalf("rebase must not raise a second repair before a new verdict: %d", queue.createCalls)
	}

	// Tick 4: a new ACCEPT at the rebased head. The merge goes through and the
	// state clears.
	fake.NotesValue = []forge.Comment{{
		ID: "n2", Body: "ACCEPT def5678", CreatedAt: "2026-11-11T13:00:00Z",
	}}
	report, err = RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick 4: %v", err)
	}
	if len(report.Merges) != 1 || !report.Merges[0].Merged {
		t.Fatalf("tick 4 merges = %+v skipped = %v, want the rebased head merged", report.Merges, report.Skipped)
	}
	if !hasWrite(fake.Writes, "merge", 9, "def5678") {
		t.Fatalf("writes = %+v, want the merge at the rebased sha", fake.Writes)
	}
}

// TestMergeRepairGitLabNamesReason pins the skip line on the GitLab vocabulary:
// cannot_be_merged is the detailed_merge_status the fleet-console production
// journal showed for the four parked change requests.
func TestMergeRepairGitLabNamesReason(t *testing.T) {
	failure := &NotMergeableError{
		Number: 660, Item: 433, SHA: "abc1234", Branch: "feature/433-x",
		Reason: "cannot_be_merged",
	}
	if got := failure.Error(); got != "refusing merge: change request #660 is not mergeable (cannot_be_merged)" {
		t.Fatalf("error = %q", got)
	}
	cr := forge.ChangeRequest{Mergeable: false, MergeState: "cannot_be_merged"}
	if !cr.MergeConflict() {
		t.Fatal("cannot_be_merged is not recognized as a conflict")
	}
	// An unknown word is not claimed as a conflict.
	other := forge.ChangeRequest{Mergeable: false, MergeState: "ci_stale"}
	if other.MergeConflict() {
		t.Fatal("ci_stale is not a conflict")
	}
}

// TestPlanRepairIntentRaisesTheSameRepairRow: the plan routes a conflicting
// head to the merge-repair ask (epic #341, design D3), and the act executor
// raises it through RaiseMergeRepair — the row, brief, evidence and
// (item, head) ledger key are the merge gate's own, byte for byte. The second
// tick at the same head is a ledger NoOp, never a second row.
func TestPlanRepairIntentRaisesTheSameRepairRow(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	st := newStore(t)
	pid := newProject(t, st)

	fake := forge.NewFake(forge.KindGitHub)
	fake.ItemsValue = []forge.Item{{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Five"}}
	fake.RequestsValue = []forge.ChangeRequest{{
		Number: 9, HeadSHA: "abc1234", Mergeable: false, MergeState: "dirty",
		Branch: "feature/5-x", Body: "Closes #5", DocsOnly: true,
	}}
	queue := &queueStub{item: &rig.QueueItem{ID: "qitem-repair", State: "pending"}, created: &rig.CreateRequest{}}
	raised := []DecisionRaised{}
	project := store.ProjectRow{
		ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", Enabled: true,
	}
	// The plan is computed by the same decide pass the server runs: a
	// conflicting head with no verdict produces a decision.merge_repair intent,
	// which the tick must execute once.
	plan := decide.PlanTick(decide.Snapshot{
		Project: decide.Project{Key: "app", Forge: decide.ForgeGitHub, Enabled: true},
		Items:   []decide.Item{{Number: 5, Kind: decide.KindTask, State: decide.StateOpen, Title: "Five"}},
		CRs:     []decide.CRState{{Number: 9, Item: 5, State: "open", HeadSHA: "abc1234", MergeState: "dirty"}},
		ForgeOK: true,
	}, now)
	repair := 0
	for _, intent := range plan.Intents {
		if intent.Event.Kind == "decision.merge_repair" {
			repair++
		}
	}
	if repair != 1 {
		t.Fatalf("plan = %+v, want one decision.merge_repair intent", plan.Intents)
	}
	in := TickInput{
		Store: st, Project: project, Forge: fake, Rig: queue, Git: &fakeGit{},
		Gate: openGate(), Apply: true, Items: fake.ItemsValue, CRs: fake.RequestsValue,
		Plan: plan, WorktreeRoot: t.TempDir(), Now: func() time.Time { return now },
		OnDecision: func(d DecisionRaised) { raised = append(raised, d) },
	}

	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	if len(report.Merges) != 0 {
		t.Fatalf("merges = %+v, want none — the plan asked for a repair, not a merge", report.Merges)
	}
	if queue.createCalls != 1 {
		t.Fatalf("queue creates = %d, want exactly one repair row", queue.createCalls)
	}
	if len(report.Decisions) != 1 || report.Decisions[0] != "qitem-repair" {
		t.Fatalf("decisions = %v, want the repair row", report.Decisions)
	}
	if len(raised) != 1 {
		t.Fatalf("OnDecision fired %d times, want once", len(raised))
	}
	created := queue.created
	if created.Destination != "human@kernel" {
		t.Fatalf("destination = %q, want the human's repair row", created.Destination)
	}
	if created.Summary != "#5: accepted change request #9 cannot merge (dirty)" {
		t.Fatalf("summary = %q, want RaiseMergeRepair's own summary byte for byte", created.Summary)
	}
	if !strings.Contains(created.EvidenceRef, "merge-repair-app-5-") {
		t.Fatalf("evidence_ref = %q, want the merge-repair brief name", created.EvidenceRef)
	}
	events, _ := st.RecentEvents(ctx, 20)
	found := ""
	for _, event := range events {
		if event.Kind == "decision.merge_repair" {
			found = event.Detail
		}
	}
	want := "app #5: change request #9 carries ACCEPT at abc1234 but cannot merge (dirty). Rework the branch onto the current default head, or defer the item?; row qitem-repair"
	if found != want {
		t.Fatalf("event = %q, want RaiseMergeRepair's own ledger line byte for byte", found)
	}

	// Tick 2, same head: the plan asks again and the ledger answers NoOp — one
	// row, one event, no second brief.
	report, err = RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	if queue.createCalls != 1 || len(report.Decisions) != 0 {
		t.Fatalf("tick 2: creates=%d decisions=%v, want the ledger to hold the row at one", queue.createCalls, report.Decisions)
	}
}
func TestMergeRepairNotesANewHeadOnTheOpenRow(t *testing.T) {
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
	fake.RequestsValue = []forge.ChangeRequest{{
		Number: 9, HeadSHA: "abc1234", Mergeable: false, MergeState: "dirty",
		Branch: "feature/5-x", Body: "Closes #5", DocsOnly: true,
	}}
	queue := &queueStub{
		item:  &rig.QueueItem{ID: "qitem-repair", State: "pending"},
		shows: map[string]*rig.QueueItem{"qitem-repair": {ID: "qitem-repair", State: "pending"}},
	}
	project := store.ProjectRow{
		ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", Enabled: true,
	}
	in := TickInput{
		Store: st, Project: project, Forge: fake, Rig: queue, Git: &fakeGit{},
		Gate: openGate(), Apply: true, Items: fake.ItemsValue, CRs: fake.RequestsValue,
		WorktreeRoot: t.TempDir(), Now: func() time.Time { return now },
	}

	// Tick 1: the raise at abc1234.
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	if queue.createCalls != 1 {
		t.Fatalf("tick 1 creates = %d, want the one repair row", queue.createCalls)
	}

	// Tick 2: the head moved to def5678 and still conflicts, with a fresh
	// ACCEPT. The open row gains a note; no second row is created and no new
	// ask is paged.
	fake.RequestsValue[0].HeadSHA = "def5678"
	fake.NotesValue = []forge.Comment{{
		ID: "n2", Body: "ACCEPT def5678", CreatedAt: "2026-11-11T13:00:00Z",
	}}
	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	if queue.createCalls != 1 {
		t.Fatalf("a new head raised a second row: creates = %d", queue.createCalls)
	}
	if queue.updateCalls != 1 {
		t.Fatalf("update calls = %d, want the one note on the open row", queue.updateCalls)
	}
	if len(queue.updates) != 1 || !strings.Contains(queue.updates[0].Note, "def5678") {
		t.Fatalf("note = %+v, want it to name the new head", queue.updates)
	}
	if len(report.Decisions) != 0 {
		t.Fatalf("decisions = %v, want none: the open row is already the ask", report.Decisions)
	}
	// The ledger names the new head on the same row.
	events, _ := st.RecentEvents(ctx, 20)
	repairs := 0
	for _, event := range events {
		if event.Kind == "decision.merge_repair" {
			repairs++
			if !strings.Contains(event.Detail, "row qitem-repair") {
				t.Fatalf("event detail %q does not name the open row", event.Detail)
			}
		}
	}
	if repairs != 2 {
		t.Fatalf("decision.merge_repair events = %d, want one per head", repairs)
	}

	// Tick 3 at the same head: the ledger already carries it.
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick 3: %v", err)
	}
	if queue.createCalls != 1 || queue.updateCalls != 1 {
		t.Fatalf("same head rewrote the row: creates = %d updates = %d", queue.createCalls, queue.updateCalls)
	}
}

// TestMergeRepairResolvesWhenTheItemMerges pins #340's second promise: the
// tick that lands the head the row conflicts with resolves the row through
// ResolveRow, with the fact that ended it.
func TestMergeRepairResolvesWhenTheItemMerges(t *testing.T) {
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
	fake.RequestsValue = []forge.ChangeRequest{{
		Number: 9, HeadSHA: "abc1234", Mergeable: false, MergeState: "dirty",
		Branch: "feature/5-x", Body: "Closes #5", DocsOnly: true,
	}}
	queue := &queueStub{
		item:  &rig.QueueItem{ID: "qitem-repair", State: "pending"},
		shows: map[string]*rig.QueueItem{"qitem-repair": {ID: "qitem-repair", State: "pending"}},
	}
	project := store.ProjectRow{
		ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", Enabled: true,
	}
	in := TickInput{
		Store: st, Project: project, Forge: fake, Rig: queue, Git: &fakeGit{},
		Gate: openGate(), Apply: true, Items: fake.ItemsValue, CRs: fake.RequestsValue,
		WorktreeRoot: t.TempDir(), Now: func() time.Time { return now },
	}
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick 1: %v", err)
	}

	// Tick 2: the rebased head merges, and the same tick resolves the row.
	fake.RequestsValue[0].HeadSHA = "def5678"
	fake.RequestsValue[0].Mergeable = true
	fake.RequestsValue[0].MergeState = "clean"
	fake.NotesValue = []forge.Comment{{
		ID: "n2", Body: "ACCEPT def5678", CreatedAt: "2026-11-11T13:00:00Z",
	}}
	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	if len(report.Merges) != 1 || !report.Merges[0].Merged {
		t.Fatalf("merges = %+v, want the rebased head merged", report.Merges)
	}
	if len(queue.resolves) != 1 {
		t.Fatalf("resolves = %v, want the row resolved once", queue.resolves)
	}
	if !strings.Contains(queue.resolves[0], "merged") || !strings.Contains(queue.resolves[0], "def5678") {
		t.Fatalf("resolution = %q, want it to name the merged head", queue.resolves[0])
	}
	if !hasSkip(report, "repair resolved") {
		t.Fatalf("skipped = %v, want the resolution line", report.Skipped)
	}
	events, _ := st.RecentEvents(ctx, 20)
	resolved := false
	for _, event := range events {
		if event.Kind == "row.resolve" && event.Subject == "qitem-repair" {
			resolved = true
		}
	}
	if !resolved {
		t.Fatalf("events = %+v, want the row.resolve event", events)
	}
}

// TestMergeRepairRaisesAgainAfterResolution pins #340's fourth promise: a
// resolved row does not silence the next conflict — a later head raises
// exactly one new row, while the (item, head) key still prevents a re-raise
// at a head the ledger already carries.
func TestMergeRepairRaisesAgainAfterResolution(t *testing.T) {
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
	fake.RequestsValue = []forge.ChangeRequest{{
		Number: 9, HeadSHA: "abc1234", Mergeable: false, MergeState: "dirty",
		Branch: "feature/5-x", Body: "Closes #5", DocsOnly: true,
	}}
	queue := &queueStub{
		item:  &rig.QueueItem{ID: "qitem-repair", State: "pending"},
		shows: map[string]*rig.QueueItem{"qitem-repair": {ID: "qitem-repair", State: "pending"}},
	}
	project := store.ProjectRow{
		ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", Enabled: true,
	}
	in := TickInput{
		Store: st, Project: project, Forge: fake, Rig: queue, Git: &fakeGit{},
		Gate: openGate(), Apply: true, Items: fake.ItemsValue, CRs: fake.RequestsValue,
		WorktreeRoot: t.TempDir(), Now: func() time.Time { return now },
	}
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick 1: %v", err)
	}

	// Tick 2: the head merges and the row resolves.
	fake.RequestsValue[0].HeadSHA = "def5678"
	fake.RequestsValue[0].Mergeable = true
	fake.RequestsValue[0].MergeState = "clean"
	fake.NotesValue = []forge.Comment{{
		ID: "n2", Body: "ACCEPT def5678", CreatedAt: "2026-11-11T13:00:00Z",
	}}
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	if len(queue.resolves) != 1 {
		t.Fatalf("resolves = %v, want the row resolved", queue.resolves)
	}
	// The row is now terminal, the way the resolved queue would show it.
	queue.shows["qitem-repair"].State = "done"

	// Tick 3: the same change request conflicts again at a later head. One
	// new row — and the new head's ledger entry lives on that new row.
	fake.RequestsValue[0].HeadSHA = "999aaaa"
	fake.RequestsValue[0].Mergeable = false
	fake.RequestsValue[0].MergeState = "dirty"
	fake.NotesValue = []forge.Comment{{
		ID: "n3", Body: "ACCEPT 999aaaa", CreatedAt: "2026-11-11T14:00:00Z",
	}}
	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick 3: %v", err)
	}
	if queue.createCalls != 2 {
		t.Fatalf("creates = %d, want the new conflict to raise exactly one new row", queue.createCalls)
	}
	if len(report.Decisions) != 1 {
		t.Fatalf("decisions = %v, want the new repair row", report.Decisions)
	}
	// The same head again raises nothing: the (item, head) key holds.
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick 4: %v", err)
	}
	if queue.createCalls != 2 {
		t.Fatalf("same head raised again: creates = %d", queue.createCalls)
	}
}

// TestMergeRepairReconcileFailureKeepsTheTick pins #340's third promise: a
// rig that refuses the note or the resolve never fails the tick — the skip
// line carries the refusal and the next tick retries.
func TestMergeRepairReconcileFailureKeepsTheTick(t *testing.T) {
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
	fake.RequestsValue = []forge.ChangeRequest{{
		Number: 9, HeadSHA: "abc1234", Mergeable: false, MergeState: "dirty",
		Branch: "feature/5-x", Body: "Closes #5", DocsOnly: true,
	}}
	queue := &queueStub{
		item:  &rig.QueueItem{ID: "qitem-repair", State: "pending"},
		shows: map[string]*rig.QueueItem{"qitem-repair": {ID: "qitem-repair", State: "pending"}},
	}
	project := store.ProjectRow{
		ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", Enabled: true,
	}
	in := TickInput{
		Store: st, Project: project, Forge: fake, Rig: queue, Git: &fakeGit{},
		Gate: openGate(), Apply: true, Items: fake.ItemsValue, CRs: fake.RequestsValue,
		WorktreeRoot: t.TempDir(), Now: func() time.Time { return now },
	}
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick 1: %v", err)
	}

	// The note on the open row is refused: the tick survives with a skip
	// line, no second row, and no event for the new head — the next tick
	// retries the note.
	queue.updateErr = errors.New("queue refused the note")
	fake.RequestsValue[0].HeadSHA = "def5678"
	fake.NotesValue = []forge.Comment{{
		ID: "n2", Body: "ACCEPT def5678", CreatedAt: "2026-11-11T13:00:00Z",
	}}
	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick 2 (note refused) failed the tick: %v", err)
	}
	if !hasSkip(report, "merge repair") {
		t.Fatalf("skipped = %v, want the refusal carried", report.Skipped)
	}
	if queue.createCalls != 1 {
		t.Fatalf("creates = %d, want no second row on a refused note", queue.createCalls)
	}
	events, _ := st.RecentEvents(ctx, 20)
	for _, event := range events {
		if event.Kind == "decision.merge_repair" && strings.Contains(event.Detail, "def5678") {
			t.Fatalf("the refused note still wrote the new head's event: %+v", event)
		}
	}
	queue.updateErr = nil
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("tick 3 (note retried): %v", err)
	}
	if queue.updateCalls != 2 {
		t.Fatalf("update calls = %d, want the note retried", queue.updateCalls)
	}

	// The resolve is refused: the tick survives, and the retry lands.
	queue.resolveErr = errors.New("queue refused the resolve")
	fake.RequestsValue[0].HeadSHA = "def5678"
	fake.RequestsValue[0].Mergeable = true
	fake.RequestsValue[0].MergeState = "clean"
	report, err = RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick 4 (resolve refused) failed the tick: %v", err)
	}
	if !hasSkip(report, "merge repair reconcile") {
		t.Fatalf("skipped = %v, want the resolve refusal carried", report.Skipped)
	}
	queue.resolveErr = nil
	report, err = RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick 5 (resolve retried): %v", err)
	}
	if len(queue.resolves) != 2 {
		t.Fatalf("resolves = %v, want the resolve retried once", queue.resolves)
	}
	if !hasSkip(report, "repair resolved") {
		t.Fatalf("skipped = %v, want the resolution line on the retry", report.Skipped)
	}
}
