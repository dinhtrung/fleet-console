package act

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// ensuredEpicBranch reports whether any write ensured a branch.
func ensuredEpicBranch(writes []forge.Write) bool {
	for _, write := range writes {
		if write.Op == "ensure_branch" {
			return true
		}
	}
	return false
}

// epicBranchFixtureName is slug("Epic forty eight") under epic/<n>- (#484, design D2).
const epicBranchFixtureName = "epic/21-epic-forty-eight"

func epicParent(number int, labels ...string) *forge.Item {
	return &forge.Item{Number: number, Kind: forge.KindEpic, State: forge.StateOpen, Title: "Epic forty eight", Labels: labels}
}

func epicChild(number, parent int) forge.Item {
	parentNumber := parent
	return forge.Item{Number: number, Kind: forge.KindTask, Title: "Child work", Body: "body", Parent: &parentNumber}
}

func epicActions(st *store.Store, git Git, fake *forge.Fake, epicBranches bool) *Actions {
	stub := &queueStub{item: &rig.QueueItem{ID: "qitem-epic", State: "pending"}, created: &rig.CreateRequest{}}
	return &Actions{Store: st, Forge: fake, Rig: stub, Git: git, EpicBranches: epicBranches}
}

func readBrief(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read brief: %v", err)
	}
	return string(raw)
}

// TestDispatchEpicChildCutsFromEpicHead (#484, task 4.1/4.2): the first child
// ensures the epic branch from the default head and cuts from it; a later child
// cuts from the epic branch's head, and the brief names the base.
func TestDispatchEpicChildCutsFromEpicHead(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	git := &fakeGit{refSHAs: map[string]string{"origin/" + epicBranchFixtureName: "default-head"}}
	fake := forge.NewFake(forge.KindGitHub)
	actions := epicActions(st, git, fake, true)
	root := t.TempDir()

	first, err := actions.Dispatch(ctx, DispatchInput{
		ProjectID: pid, ProjectKey: "app", Item: epicChild(22, 21), Parent: epicParent(21),
		Seat: "seat@rig", WorktreeRoot: root,
	})
	if err != nil {
		t.Fatalf("first dispatch: %v", err)
	}
	// The branch was ensured from the default head and the worktree cut there.
	if !hasWrite(fake.Writes, "ensure_branch", 0, epicBranchFixtureName+" from "+"base-sha") {
		t.Fatalf("ensure calls = %+v, want the epic branch from the default head", fake.Writes)
	}
	if first.BaseSHA != "default-head" || git.bases[0] != "default-head" {
		t.Fatalf("first base = %q (worktree %q), want the epic head (== default head on creation)",
			first.BaseSHA, git.bases[0])
	}
	brief := readBrief(t, first.BriefPath)
	if !strings.Contains(brief, "PR base: "+epicBranchFixtureName) {
		t.Fatalf("the brief does not name the epic base:\n%s", brief)
	}
	t.Logf("first child brief (Publish head):\n%s", brief[strings.Index(brief, "## Publish"):strings.Index(brief, "## The work")])

	// The first child's work merged into the epic branch: the next child must cut
	// from that head, not the default.
	git.refSHAs["origin/"+epicBranchFixtureName] = "epic-head-2"
	second, err := actions.Dispatch(ctx, DispatchInput{
		ProjectID: pid, ProjectKey: "app", Item: epicChild(23, 21), Parent: epicParent(21),
		Seat: "seat@rig", WorktreeRoot: root,
	})
	if err != nil {
		t.Fatalf("second dispatch: %v", err)
	}
	if second.BaseSHA != "epic-head-2" || git.bases[len(git.bases)-1] != "epic-head-2" {
		t.Fatalf("second base = %q (worktree %q), want the epic head", second.BaseSHA, git.bases[len(git.bases)-1])
	}
}

// TestDispatchEpicDirectKeepsDefaultBase (#484, design D1): the per-epic escape
// keeps its children on the default branch, and no branch is ensured.
func TestDispatchEpicDirectKeepsDefaultBase(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	git := &fakeGit{}
	fake := forge.NewFake(forge.KindGitHub)
	actions := epicActions(st, git, fake, true)

	result, err := actions.Dispatch(ctx, DispatchInput{
		ProjectID: pid, ProjectKey: "app", Item: epicChild(22, 21), Parent: epicParent(21, "epic::direct"),
		Seat: "seat@rig", WorktreeRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if ensuredEpicBranch(fake.Writes) {
		t.Fatalf("epic::direct ensured a branch: %+v", fake.Writes)
	}
	if len(git.refsAsked) != 0 || result.BaseSHA != "base-sha" {
		t.Fatalf("epic::direct did not keep the default base: refs=%v base=%q", git.refsAsked, result.BaseSHA)
	}
	if strings.Contains(readBrief(t, result.BriefPath), "PR base:") {
		t.Fatal("an epic::direct child's brief names a base")
	}
}

// TestDispatchEpicBranchesOffKeepsDefaultBase (#484): a project that did not opt
// in behaves exactly as before.
func TestDispatchEpicBranchesOffKeepsDefaultBase(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	git := &fakeGit{}
	fake := forge.NewFake(forge.KindGitHub)
	actions := epicActions(st, git, fake, false)

	result, err := actions.Dispatch(ctx, DispatchInput{
		ProjectID: pid, ProjectKey: "app", Item: epicChild(22, 21), Parent: epicParent(21),
		Seat: "seat@rig", WorktreeRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if ensuredEpicBranch(fake.Writes) || len(git.refsAsked) != 0 || result.BaseSHA != "base-sha" {
		t.Fatalf("epic_branches=false did not keep the default base: writes=%+v refs=%v base=%q",
			fake.Writes, git.refsAsked, result.BaseSHA)
	}
	brief := readBrief(t, result.BriefPath)
	if strings.Contains(brief, "PR base:") {
		t.Fatalf("a non-epic brief names a base:\n%s", brief)
	}
	t.Logf("non-epic brief (Publish head):\n%s", brief[strings.Index(brief, "## Publish"):strings.Index(brief, "## The work")])
}

// TestDispatchRefusesWhenEpicBranchCannotBeCreated (#484, design D6): the forge
// failure is returned naming the branch and the response, and no row, worktree
// or fallback to the default branch happens.
func TestDispatchRefusesWhenEpicBranchCannotBeCreated(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	git := &fakeGit{}
	fake := forge.NewFake(forge.KindGitHub)
	fake.BranchErr = errors.New("HTTP 403: Resource not accessible by personal access token")
	actions := epicActions(st, git, fake, true)

	_, err := actions.Dispatch(ctx, DispatchInput{
		ProjectID: pid, ProjectKey: "app", Item: epicChild(22, 21), Parent: epicParent(21),
		Seat: "seat@rig", WorktreeRoot: t.TempDir(),
	})
	if err == nil {
		t.Fatal("a failed EnsureBranch must refuse the dispatch")
	}
	for _, want := range []string{"epic branch " + epicBranchFixtureName + " could not be created", "Resource not accessible"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not carry %q", err.Error(), want)
		}
	}
	if rows, _ := st.Dispatches(ctx, pid, 22); len(rows) != 0 {
		t.Fatalf("a refused dispatch wrote a row: %+v", rows)
	}
	if git.worktrees != 0 {
		t.Fatalf("a refused dispatch cut a worktree")
	}
	t.Logf("creation refusal: %v", err)
}

// TestDispatchRefusesWhenEpicBranchCannotBeRead (#484, design D6): the branch
// the forge said it ensured cannot be resolved locally, so the dispatch refuses
// with a diagnosis instead of recreating it from the default head.
func TestDispatchRefusesWhenEpicBranchCannotBeRead(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	git := &fakeGit{refErr: errors.New("git rev-parse origin/" + epicBranchFixtureName + ": exit status 128: unknown revision")}
	fake := forge.NewFake(forge.KindGitHub)
	actions := epicActions(st, git, fake, true)

	_, err := actions.Dispatch(ctx, DispatchInput{
		ProjectID: pid, ProjectKey: "app", Item: epicChild(22, 21), Parent: epicParent(21),
		Seat: "seat@rig", WorktreeRoot: t.TempDir(),
	})
	if err == nil {
		t.Fatal("an unreadable epic branch must refuse the dispatch")
	}
	for _, want := range []string{"epic branch " + epicBranchFixtureName + " could not be read after ensuring it", "refusing to recreate"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not carry %q", err.Error(), want)
		}
	}
	if rows, _ := st.Dispatches(ctx, pid, 22); len(rows) != 0 {
		t.Fatalf("a refused dispatch wrote a row: %+v", rows)
	}
	if git.worktrees != 0 {
		t.Fatalf("a refused dispatch cut a worktree")
	}
	t.Logf("read refusal: %v", err)
}
