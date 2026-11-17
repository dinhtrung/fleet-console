package act

import (
	"context"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// TestRunTickPausedProjectDoesNothing pins the pause guard (pause-stops-forge,
// design D1): a paused or disabled project's tick is a pure no-op. The guard
// sits before the gate and before the first forge read, so verdict capture, the
// merge watchdog, notes and rolls all stay off the forge — a paused project
// that merged its in-flight change requests would be sending exactly the
// traffic the pause forbids.
func TestRunTickPausedProjectDoesNothing(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

	for _, test := range []struct {
		name    string
		onHold  bool
		enabled bool
	}{
		{name: "onhold", onHold: true, enabled: true},
		{name: "disabled", onHold: false, enabled: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			st := newStore(t)
			pid := newProject(t, st)
			if _, err := st.CreateDispatch(ctx, store.NewDispatch{
				ProjectID: pid, ItemNumber: 5, Attempt: 1, Seat: "seat@rig",
				Branch: "feature/5-x", Worktree: "wt", BaseSHA: "base", CreatedAt: now,
			}); err != nil {
				t.Fatalf("dispatch: %v", err)
			}

			fake := forge.NewFake(forge.KindGitHub)
			fake.ItemsValue = []forge.Item{{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Five"}}
			fake.RequestsValue = []forge.ChangeRequest{{
				Number: 9, HeadSHA: "abc1234", Mergeable: true, Body: "Closes #5", DocsOnly: true,
			}}
			fake.NotesValue = []forge.Comment{{
				ID: "n1", Body: "ACCEPT abc1234", CreatedAt: "2026-10-29T11:00:00Z",
			}}
			queue := &queueStub{item: &rig.QueueItem{ID: "qitem-1", State: "pending"}, created: &rig.CreateRequest{}}
			git := &fakeGit{}
			project := store.ProjectRow{
				ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig",
				ConcurrencyCap: 2, Enabled: test.enabled, OnHold: test.onHold,
			}
			in := TickInput{
				Store: st, Project: project, Forge: fake, Rig: queue, Git: git,
				Gate: openGate(), Apply: true, Items: fake.ItemsValue, CRs: fake.RequestsValue,
				Rows: []rig.QueueRow{{ID: "qitem-1", Destination: "seat@rig", State: "pending"}}, Plan: dispatchPlan(5, "seat@rig"),
				WorktreeRoot: t.TempDir(), Now: func() time.Time { return now },
			}

			report, err := RunTick(ctx, in)
			if err != nil {
				t.Fatalf("tick: %v", err)
			}
			if len(report.Dispatches) != 0 || len(report.Merges) != 0 || len(report.Verdicts) != 0 {
				t.Fatalf("report = %+v, want no dispatch, merge or verdict", report)
			}
			if len(fake.Writes) != 0 {
				t.Fatalf("paused tick wrote to the forge: %+v", fake.Writes)
			}
			if len(fake.CRNotesAsked) != 0 || len(fake.NotesAsked) != 0 {
				t.Fatalf("paused tick read notes: cr=%v item=%v", fake.CRNotesAsked, fake.NotesAsked)
			}
			if queue.createCalls != 0 {
				t.Fatalf("paused tick created %d queue rows", queue.createCalls)
			}
			events, err := st.RecentEvents(ctx, 5)
			if err != nil {
				t.Fatalf("events: %v", err)
			}
			if len(events) != 0 {
				t.Fatalf("paused tick wrote events: %+v", events)
			}
		})
	}
}
