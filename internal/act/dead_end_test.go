package act

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/decide"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

const (
	deadHeadForAct     = "aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111"
	deadBranchForAct   = "feature/2-live-check"
	deadResolutionText = "blocked: needs a deployed instance"
)

// deadEndPlan is the plan the server builds for a dead end (#526): the newest
// dispatch ended on a branch with no change request, origin still reports that
// head, so the ready walk skips it and the plan asks the operator what to do.
func deadEndPlan(now time.Time) decide.Plan {
	ended := time.Date(2026, 11, 18, 4, 0, 0, 0, time.UTC)
	s := decide.Snapshot{
		Project:  decide.Project{Key: "app", Forge: decide.ForgeGitHub, Enabled: true},
		Items:    []decide.Item{{Number: 2, Kind: decide.KindTask, State: decide.StateOpen, Title: "Live check"}},
		Builders: []string{"build@rig"},
		Branches: []decide.Branch{{Name: deadBranchForAct, Item: 2, Head: deadHeadForAct}},
		Dispatches: []decide.DispatchState{{
			Item: 2, Seat: "build@rig", Branch: deadBranchForAct, QItemID: "qitem-2",
			Attempt: 1, CreatedAt: ended.Add(-time.Hour), EndedAt: ended,
			EndReason: "superseded", EndHead: deadHeadForAct,
		}},
		Rows: []decide.Row{{
			QItemID: "qitem-2", State: "done", Destination: "build@rig",
			Resolution: deadResolutionText,
		}},
		ForgeOK: true,
	}
	return decide.PlanTick(s, now)
}

// TestDeadEndQuestionRaisedOnce is the second acceptance: the plan's dead_end
// intent becomes exactly one operator row, its brief carries the stopped head
// and the three answers, and the second tick with the same state adds nothing.
func TestDeadEndQuestionRaisedOnce(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 4, 5, 0, 0, time.UTC)
	st := newStore(t)
	pid := newProject(t, st)
	queue := &queueStub{creates: &[]rig.CreateRequest{}}
	project := store.ProjectRow{ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", Enabled: true}
	plan := deadEndPlan(now)

	in := TickInput{
		Store: st, Project: project, Forge: forge.NewFake(forge.KindGitHub), Rig: queue, Git: &fakeGit{},
		Gate: openGate(), Apply: true, Plan: plan, WorktreeRoot: t.TempDir(),
		Now: func() time.Time { return now },
	}

	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("first tick: %v", err)
	}
	creates := *queue.creates
	if len(creates) != 1 {
		t.Fatalf("creates = %d, want exactly one operator row", len(creates))
	}
	if creates[0].Destination != "human@kernel" {
		t.Fatalf("row destination = %s, want human@kernel", creates[0].Destination)
	}
	body, err := os.ReadFile(creates[0].BodyFile)
	if err != nil {
		t.Fatalf("read brief: %v", err)
	}
	for _, want := range []string{
		"the last dispatch ended without a change request on " + deadBranchForAct + "@" + deadHeadForAct,
		"Stopped at head: " + deadHeadForAct,
		"release (dispatch it again)",
		deadResolutionText,
	} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("brief = %q, want it to carry %q", string(body), want)
		}
	}
	exists, err := st.HasEvent(ctx, "decision.dead_end", "#2", "head="+deadHeadForAct+";")
	if err != nil || !exists {
		t.Fatalf("HasEvent = %v, %v; want the question on the ledger", exists, err)
	}

	// The same plan again: the question stands, nothing new.
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if len(*queue.creates) != 1 {
		t.Fatalf("after second tick creates = %d, want the deduped question to add nothing", len(*queue.creates))
	}
}
