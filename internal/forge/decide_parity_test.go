package forge_test

import (
	"reflect"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/decide"
	"github.com/dinhtrung/fleet-console/internal/forge"
)

// TestSameDecideForBothForgeKinds is the spec scenario "The same rule on two
// forges": one normalized task graph, encoded with each forge's link form, must
// produce the same intents from the shared decide loop.
// TestMergeConflictWordsAgree pins the plan's conflict predicate to the
// gate's: for every merge state word, decide.CRState.MergeConflict and
// forge.ChangeRequest.MergeConflict answer the same, so the plan and the
// merge gate cannot disagree about which heads need a repair (epic #341,
// design D3).
func TestMergeConflictWordsAgree(t *testing.T) {
	for _, word := range []string{"", "dirty", "cannot_be_merged", "DIRTY", "unknown", "checking", "clean", "blocked", "unstable"} {
		got := decide.CRState{MergeState: word}.MergeConflict()
		want := forge.ChangeRequest{MergeState: word}.MergeConflict()
		if got != want {
			t.Fatalf("MergeConflict(%q): decide = %v, forge = %v, want the word lists to agree", word, got, want)
		}
	}
}

func TestSameDecideForBothForgeKinds(t *testing.T) {
	parent := 1
	baseItems := []forge.Item{
		{Number: 1, Kind: forge.KindEpic, State: forge.StateOpen, Title: "epic"},
		{Number: 2, Kind: forge.KindTask, State: forge.StateOpen, Title: "first", Parent: &parent},
		{Number: 3, Kind: forge.KindTask, State: forge.StateOpen, Title: "second", Parent: &parent},
	}
	githubItems := append([]forge.Item(nil), baseItems...)
	githubItems[2].BlockedBy = []forge.Link{{Number: 2, State: forge.StateOpen}}

	gitlabItems := append([]forge.Item(nil), baseItems...)
	gitlabItems[2].Related = []forge.Link{{Number: 2, State: forge.StateOpen}}
	gitlabItems[1].Related = []forge.Link{{Number: 3, State: forge.StateOpen}}

	githubPlan := decide.Decide(snapshotFor(forge.KindGitHub, githubItems))
	gitlabPlan := decide.Decide(snapshotFor(forge.KindGitLab, gitlabItems))

	if !reflect.DeepEqual(githubPlan, gitlabPlan) {
		t.Fatalf("plans differ:\nGitHub: %+v\nGitLab: %+v", githubPlan, gitlabPlan)
	}
	if len(githubPlan.Dispatches) != 1 || githubPlan.Dispatches[0].Number != 2 {
		t.Fatalf("dispatches = %+v, want only #2", githubPlan.Dispatches)
	}
	reasons := map[int]decide.Reason{}
	for _, skip := range githubPlan.Skips {
		reasons[skip.Number] = skip.Reason
	}
	if reasons[1] != decide.ReasonEpic || reasons[3] != decide.ReasonBlocked {
		t.Fatalf("reasons = %v, want epic #1 and blocked #3", reasons)
	}
}

func snapshotFor(kind forge.Kind, items []forge.Item) decide.Snapshot {
	snapshot := decide.Snapshot{
		Project:  decide.Project{Key: "parity", Forge: decide.Forge(kind), Enabled: true},
		Builders: []string{"seat-1", "seat-2"},
		ForgeOK:  true,
	}
	for _, item := range items {
		converted := decide.Item{
			Number: item.Number, Kind: decide.ItemKind(item.Kind), State: item.State,
			Title: item.Title, Labels: item.Labels, Parent: item.Parent,
		}
		for _, link := range item.BlockedBy {
			converted.BlockedBy = append(converted.BlockedBy, decide.Link{Number: link.Number, State: link.State})
		}
		for _, link := range item.Related {
			converted.Related = append(converted.Related, decide.Link{Number: link.Number, State: link.State})
		}
		snapshot.Items = append(snapshot.Items, converted)
	}
	return snapshot
}
