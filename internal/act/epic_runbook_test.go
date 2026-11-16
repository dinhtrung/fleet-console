package act

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/config"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/registry"
	"github.com/dinhtrung/fleet-console/internal/rig"
)

// TestEpicBranchesEndToEndTrace (#491, T7 evidence): one trace from a real
// config.yaml with epic_branches on, through the apply path the boot uses, to the
// dispatch base and brief, and on to the epic change request the operator merges.
// The forge and git are doubles, as the issue allows; the config, the project row
// and the brief are real.
func TestEpicBranchesEndToEndTrace(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := fmt.Sprintf(`listen: 127.0.0.1:0
projects:
  - key: runbook
    name: Runbook
    forge: github
    repo: org/runbook
    rigs: [rig-a]
    concurrency_cap: 1
    epic_branches: true
    paths:
      main: %s
      worktrees: %s
`, filepath.Join(dir, "checkout"), filepath.Join(dir, "worktrees"))
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	eff, err := config.Load(config.Options{FlagPath: cfgPath, Home: dir, Cwd: dir})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !eff.EpicBranchesFor("runbook") {
		t.Fatal("config.Load did not resolve epic_branches: true")
	}

	db := newStore(t)
	if _, err := registry.ApplyOne(ctx, db, eff.Projects[0], false); err != nil {
		t.Fatalf("apply: %v", err)
	}
	row, err := db.ProjectByKey(ctx, "runbook")
	if err != nil || row == nil {
		t.Fatalf("project row: %v", err)
	}
	if !row.EpicBranches {
		t.Fatal("the project row did not carry epic_branches")
	}
	t.Logf("config: epic_branches=true for %q; project row epic_branches=%v", row.Key, row.EpicBranches)

	git := &fakeGit{
		matchingBranches: []string{"origin/epic/21-epic-forty-eight"},
		refSHAs:          map[string]string{"origin/epic/21-epic-forty-eight": "base-sha"},
	}
	fake := &forge.Fake{}
	stub := &queueStub{item: &rig.QueueItem{ID: "qitem-runbook", State: "pending"}, created: &rig.CreateRequest{}}
	actions := &Actions{
		Store: db, Forge: fake, Rig: stub, Git: git, ProjectID: row.ID, ProjectKey: row.Key,
		EpicBranches: row.EpicBranches, DefaultBranch: row.DefaultBranch,
	}

	parent := 21
	dispatched, err := actions.Dispatch(ctx, DispatchInput{
		ProjectID: row.ID, ProjectKey: row.Key, Item: epicChild(22, 21), Parent: epicParent(21),
		Seat: "seat@rig", WorktreeRoot: filepath.Join(dir, "worktrees"),
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	brief := readBrief(t, dispatched.BriefPath)
	baseLine := ""
	for _, line := range strings.Split(brief, "\n") {
		if strings.HasPrefix(line, "PR base: ") {
			baseLine = strings.TrimSpace(line)
		}
	}
	if dispatched.BaseSHA != "base-sha" || baseLine != "PR base: epic/21-epic-forty-eight" {
		t.Fatalf("dispatch base = %q, brief base = %q", dispatched.BaseSHA, baseLine)
	}
	t.Logf("dispatch: task branch %s cut from Base sha %s", dispatched.Branch, dispatched.BaseSHA)
	t.Logf("brief: %q", baseLine)

	// The children finish; the epic is delivered on its own branch, one commit on
	// from the default.
	git.refSHAs["origin/epic/21-epic-forty-eight"] = "epic-head"
	git.ancestors = map[string]bool{"base-sha epic-head": true} // up to date, no drift
	fake.SubIssuesValue = map[int][]forge.Item{21: {
		{Number: 22, Kind: forge.KindTask, State: forge.StateClosed, Title: "First child", Parent: &parent},
		{Number: 23, Kind: forge.KindTask, State: forge.StateClosed, Title: "Second child", Parent: &parent},
	}}
	in := TickInput{
		Project:      *row,
		Items:        []forge.Item{{Number: 21, Kind: forge.KindEpic, State: forge.StateOpen, Title: "Epic forty eight"}},
		CRs:          []forge.ChangeRequest{{Number: 70, State: "merged", Merged: true, Branch: "feature/22-first-child", Body: "Closes #22"}},
		WorktreeRoot: filepath.Join(dir, "worktrees"),
	}
	report := &TickReport{}
	actions.completeEpic(ctx, in, report, map[int]bool{}, 21, nil)
	if len(report.Completions) != 1 || len(fake.RequestsValue) != 1 {
		t.Fatalf("completion = %+v, requests = %+v", report.Completions, fake.RequestsValue)
	}
	delivery := fake.RequestsValue[0]
	if delivery.Branch != "epic/21-epic-forty-eight" || delivery.Base != "main" || delivery.Draft {
		t.Fatalf("delivery = %+v", delivery)
	}
	for _, want := range []string{"### Merged\n- #22 First child", "### Not merged\n- #23 Second child", "Closes #21"} {
		if !strings.Contains(delivery.Body, want) {
			t.Fatalf("epic body misses %q:\n%s", want, delivery.Body)
		}
	}
	t.Logf("epic change request: #%d %q base %s draft=%v", delivery.Number, delivery.Title, delivery.Base, delivery.Draft)
	t.Logf("epic body:\n%s", delivery.Body)
}
