package act

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// seedEpicGate builds one epic-enabled project with an epic (#21), its child
// (#22) and a standalone task (#30), all open.
func seedEpicGate(t *testing.T) (*store.Store, store.ProjectRow) {
	t.Helper()
	ctx := context.Background()
	db := newStore(t)
	pid, err := db.CreateProject(ctx, store.ProjectInput{
		Key: "epicapp", Name: "epicapp", ForgeKind: "github", ForgeProject: "o/r",
		Rig: "rig", ConcurrencyCap: 2, Enabled: true, DefaultBranch: "main",
	})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	parent := 21
	if err := db.ReplaceItems(ctx, pid, []store.ItemRecord{
		{Number: 21, Kind: string(forge.KindEpic), State: forge.StateOpen, Title: "Epic forty eight"},
		{Number: 22, Kind: string(forge.KindTask), State: forge.StateOpen, Title: "Child work", Parent: &parent},
		{Number: 30, Kind: string(forge.KindTask), State: forge.StateOpen, Title: "Standalone"},
	}); err != nil {
		t.Fatalf("items: %v", err)
	}
	row, err := db.ProjectByKey(ctx, "epicapp")
	if err != nil || row == nil {
		t.Fatalf("project read: %v", err)
	}
	return db, *row
}

// acceptedDispatch creates a dispatch and an ACCEPT verdict at head for one item.
func acceptedDispatch(t *testing.T, db *store.Store, pid int64, item int, head string) int64 {
	t.Helper()
	ctx := context.Background()
	dispatchID, err := db.CreateDispatch(ctx, store.NewDispatch{
		ProjectID: pid, ItemNumber: item, Attempt: 1, Seat: "seat@rig",
		Branch: fmt.Sprintf("feature/%d-x", item), Worktree: "wt", BaseSHA: "base", State: "dispatched",
	})
	if err != nil {
		t.Fatalf("dispatch %d: %v", item, err)
	}
	if _, err := db.AddVerdict(ctx, store.NewVerdict{
		DispatchID: dispatchID, SHA: head, Decision: "accept", Source: "test",
	}); err != nil {
		t.Fatalf("verdict %d: %v", item, err)
	}
	return dispatchID
}

// gateCR is a mergeable, green change request closing item.
func gateCR(number, item int, branch, base, head string) forge.ChangeRequest {
	return forge.ChangeRequest{
		Number: number, State: "open", Branch: branch, Base: base, HeadSHA: head,
		Mergeable: true, Body: fmt.Sprintf("Closes #%d", item),
		Checks: []forge.Check{{Name: "go", Conclusion: "success"}},
	}
}

func mergeWrote(writes []forge.Write, number int) bool {
	for _, write := range writes {
		if write.Op == "merge" && write.Number == number {
			return true
		}
	}
	return false
}

func commentOn(writes []forge.Write, number int) string {
	for _, write := range writes {
		if write.Op == "comment" && write.Number == number {
			return write.Value
		}
	}
	return ""
}

// TestMergeRefusesAChildWhoseBaseIsNotItsEpicBranch (#489, design D4): the
// accepted child targets main under an opted-in epic, so the merge is refused
// with the wrong_base reason and a note telling the operator to retarget.
func TestMergeRefusesAChildWhoseBaseIsNotItsEpicBranch(t *testing.T) {
	ctx := context.Background()
	db, project := seedEpicGate(t)
	dispatchID := acceptedDispatch(t, db, project.ID, 22, "abc1234")
	fake := &forge.Fake{RequestsValue: []forge.ChangeRequest{gateCR(90, 22, "feature/22-child-work", "main", "abc1234")}}
	actions := &Actions{Store: db, Forge: fake, ProjectID: project.ID, EpicBranches: true, DefaultBranch: "main"}

	_, err := actions.Merge(ctx, dispatchID, 22)
	if err == nil {
		t.Fatal("a child targeting main under an opted-in epic must not merge")
	}
	const want = "wrong_base: change request targets main, expected epic/21-…"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not carry %q", err.Error(), want)
	}
	if mergeWrote(fake.Writes, 90) {
		t.Fatal("the refused change request was merged")
	}
	note := commentOn(fake.Writes, 22)
	if !strings.Contains(note, "Retarget") || !strings.Contains(note, "epic/21-…") {
		t.Fatalf("the item note does not tell the operator to retarget: %q", note)
	}
	t.Logf("wrong-base refusal: %v\nnote: %s", err, note)
}

// TestMergeAllowsAChildOnItsEpicBranch (#489): the same child, correctly based,
// merges as today.
func TestMergeAllowsAChildOnItsEpicBranch(t *testing.T) {
	ctx := context.Background()
	db, project := seedEpicGate(t)
	dispatchID := acceptedDispatch(t, db, project.ID, 22, "abc1234")
	fake := &forge.Fake{RequestsValue: []forge.ChangeRequest{gateCR(90, 22, "feature/22-child-work", "epic/21-epic-forty-eight", "abc1234")}}
	actions := &Actions{Store: db, Forge: fake, ProjectID: project.ID, EpicBranches: true, DefaultBranch: "main"}

	result, err := actions.Merge(ctx, dispatchID, 22)
	if err != nil {
		t.Fatalf("a child on its epic branch must merge: %v", err)
	}
	if result == nil || !result.Merged || !mergeWrote(fake.Writes, 90) {
		t.Fatalf("merge did not land: result=%+v writes=%+v", result, fake.Writes)
	}
}

// TestMergeDefaultBaseInsideAnEpicProject (#489): an item that is not a child of
// an epic must still target the project's default branch.
func TestMergeDefaultBaseInsideAnEpicProject(t *testing.T) {
	ctx := context.Background()
	db, project := seedEpicGate(t)
	dispatchID := acceptedDispatch(t, db, project.ID, 30, "def5678")
	fake := &forge.Fake{RequestsValue: []forge.ChangeRequest{gateCR(92, 30, "feature/30-standalone", "release", "def5678")}}
	actions := &Actions{Store: db, Forge: fake, ProjectID: project.ID, EpicBranches: true, DefaultBranch: "main"}

	_, err := actions.Merge(ctx, dispatchID, 30)
	if err == nil || !strings.Contains(err.Error(), "wrong_base: change request targets release, expected main") {
		t.Fatalf("a standalone item off the default branch must refuse: %v", err)
	}
}

// TestGateNeverMergesTheEpicDelivery (#489, design D4): a head branch named
// epic/<n>- is the epic's own delivery; the gate refuses it on both paths.
func TestGateNeverMergesTheEpicDelivery(t *testing.T) {
	ctx := context.Background()
	db, project := seedEpicGate(t)

	// The dispatch path.
	dispatchID := acceptedDispatch(t, db, project.ID, 22, "abc1234")
	fake := &forge.Fake{RequestsValue: []forge.ChangeRequest{gateCR(91, 22, "epic/21-epic-forty-eight", "main", "abc1234")}}
	actions := &Actions{Store: db, Forge: fake, ProjectID: project.ID, EpicBranches: true, DefaultBranch: "main"}
	_, err := actions.Merge(ctx, dispatchID, 22)
	if err == nil {
		t.Fatal("the gate merged an epic-branch head")
	}
	for _, want := range []string{"epic delivery on epic/21-epic-forty-eight", "the gate never merges an epic"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not carry %q", err.Error(), want)
		}
	}
	if mergeWrote(fake.Writes, 91) {
		t.Fatal("the epic delivery was merged")
	}
	t.Logf("epic-head refusal (dispatch path): %v", err)

	// The adoption path (T6's epic CR has no dispatch).
	adopted := &forge.Fake{}
	adoptActions := &Actions{Store: db, Forge: adopted, ProjectID: project.ID, EpicBranches: true, DefaultBranch: "main"}
	_, err = adoptActions.MergeAdopted(ctx, project.ID, 21, gateCR(93, 21, "epic/21-epic-forty-eight", "main", "aaa9999"))
	if err == nil || !strings.Contains(err.Error(), "epic delivery on epic/21-epic-forty-eight") {
		t.Fatalf("the adoption path merged an epic-branch head: %v", err)
	}
	if mergeWrote(adopted.Writes, 93) {
		t.Fatal("the epic delivery was adopted and merged")
	}
}

// TestBaseCheckStaysOffWhenTheProjectDidNotOptIn (#489): a project without the
// option behaves exactly as before — no base check, whatever the forge reported.
func TestBaseCheckStaysOffWhenTheProjectDidNotOptIn(t *testing.T) {
	ctx := context.Background()
	db := newStore(t)
	project, dispatchID, request := mergeFixture(t, db)
	if request.Base != "" {
		t.Fatalf("fixture base = %q, want empty to prove the check is off", request.Base)
	}
	fake := &forge.Fake{RequestsValue: []forge.ChangeRequest{request}}
	actions := &Actions{Store: db, Forge: fake, ProjectID: project.ID, EpicBranches: false}

	result, err := actions.Merge(ctx, dispatchID, 5)
	if err != nil {
		t.Fatalf("a toggle-off project must merge as today: %v", err)
	}
	if result == nil || !result.Merged {
		t.Fatalf("merge did not land: %+v", result)
	}
}
