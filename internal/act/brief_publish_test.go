package act

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
)

// TestBriefPublishContract: the dispatch brief's ## Publish states the closing
// rule (the merge gate finds the item only through "Closes #<item>" in the
// body) and the partial-attempt move (hand off with what remains instead of
// opening a change request that only references the item), and the finished
// case keeps its one-draft-PR, no-merge, hand-off ask verbatim (#315, epic
// #309 T6). Rendered through Dispatch for a fixture item — the same file the
// builder receives.
func TestBriefPublishContract(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	git := &fakeGit{}
	fake := forge.NewFake(forge.KindGitHub)
	rigStub := &queueStub{item: &rig.QueueItem{ID: "qitem-1", State: "pending"}, created: &rig.CreateRequest{}}
	actions := &Actions{Store: st, Forge: fake, Rig: rigStub, Git: git}
	in := DispatchInput{
		ProjectID: pid, ProjectKey: "tsim",
		Item: forge.Item{Number: 448, Kind: forge.KindTask, Title: "Scenarios", Body: "The web scenario entity title hides the name."},
		Seat: "dev-build@fleet-console-rig", WorktreeRoot: t.TempDir(),
	}
	result, err := actions.Dispatch(ctx, in)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	raw, err := os.ReadFile(result.BriefPath)
	if err != nil {
		t.Fatalf("brief: %v", err)
	}
	brief := string(raw)

	// ## Publish is the last-but-one section; take it verbatim.
	start := strings.Index(brief, "## Publish")
	end := strings.Index(brief, "## The work")
	if start < 0 || end < 0 || end < start {
		t.Fatalf("brief has no Publish section: %q", brief)
	}
	publish := squished(brief[start:end])

	// The closing rule: the gate reads "Closes #<item>" only. Matched on
	// whitespace-collapsed text, so a rewrap of the template cannot break the
	// contract assertions.
	for _, missing := range []struct{ what, sentence string }{
		{"the closing rule", `The merge gate finds the item only through "Closes #<item>" in the pull request's body`},
		{"the invisible-to-the-gate clause", "a body that only references the item (Refs, Part of, a title mention) is invisible to it"},
		{"the never-read consequence", "and that change request is never merged, undrafted or recorded"},
		{"the partial-attempt sentence", "If this attempt cannot finish the item, do not open a change request that only references it"},
		{"the hand-off instruction", "the candidate sha, the commands and exactly what remains"},
		{"the split outcome", "the item can be split and each part gets its own item and its own"},
	} {
		if !strings.Contains(publish, missing.sentence) {
			t.Fatalf("## Publish misses %s:\n%s", missing.what, publish)
		}
	}
	// The finished case is unchanged: one draft pull request, Closes #<item>,
	// no merge, hand off with the sha, the commands and the limits.
	if !strings.Contains(publish, `"Closes #448". Do not merge. Hand off with the candidate sha, the commands you ran and what you could not check.`) {
		t.Fatalf("## Publish weakened the finished case:\n%s", publish)
	}
	// No other template section changed: the rule and the partial move live in
	// ## Publish alone, and the header block is intact.
	if !strings.Contains(squished(brief), "The merge gate finds the item only through") ||
		strings.Contains(brief[:start], "merge gate") {
		t.Fatalf("the closing rule is missing from or leaked past ## Publish:\n%s", brief)
	}
	if !strings.HasPrefix(brief, "# tsim #448: Scenarios\n\nRig: ") {
		t.Fatalf("brief header changed:\n%s", brief)
	}
}

// squished collapses all whitespace runs to single spaces, so a contract
// assertion on a rendered sentence survives the template's line wrapping.
func squished(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
