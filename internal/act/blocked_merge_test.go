package act

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// TestTickReportsBlockedMerge covers #252: an accepted change request the
// merge gate refuses for a reason the next tick cannot fix is reported as an
// operator obligation on every tick it holds. The rate limit belongs to the
// notifier; the tick keeps stating the fact.
func TestTickReportsBlockedMerge(t *testing.T) {
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

	// The change request is accepted but not mergeable, and the forge names why.
	// The state must be a computed word (epic #341): an empty one now reads as
	// "the forge has not answered yet" and waits instead of refusing.
	fake := forge.NewFake(forge.KindGitHub)
	fake.ItemsValue = []forge.Item{{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Five"}}
	fake.RequestsValue = []forge.ChangeRequest{{
		Number: 9, HeadSHA: "abc1234", Mergeable: false, MergeState: "dirty",
		Branch: "feature/5-x", Body: "Closes #5", DocsOnly: true,
	}}
	project := store.ProjectRow{
		ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", Enabled: true,
	}
	in := TickInput{
		Store: st, Project: project, Forge: fake, Rig: &queueStub{}, Git: &fakeGit{},
		Gate: openGate(), Apply: true, Items: fake.ItemsValue, CRs: fake.RequestsValue,
		WorktreeRoot: t.TempDir(), Now: func() time.Time { return now },
	}

	for tick := 1; tick <= 2; tick++ {
		report, err := RunTick(ctx, in)
		if err != nil {
			t.Fatalf("tick %d: %v", tick, err)
		}
		if len(report.BlockedMerges) != 1 {
			t.Fatalf("tick %d blocked = %+v skipped = %v, want exactly one blocked merge",
				tick, report.BlockedMerges, report.Skipped)
		}
		blocked := report.BlockedMerges[0]
		if blocked.Item != 5 || blocked.CR != 9 || blocked.SHA != "abc1234" {
			t.Fatalf("blocked = %+v, want item 5, CR 9, head abc1234", blocked)
		}
		if !containsAll(blocked.Reason, "not mergeable") {
			t.Fatalf("reason = %q, want the refusal", blocked.Reason)
		}
		if !containsAll(blocked.Action, "rebase") {
			t.Fatalf("action = %q, want the rework expected", blocked.Action)
		}
	}
}

// TestBlockedMergeClassifiesRefusals pins the classifier against the merge
// gate's own words: the four ACCEPT-surviving refusals page, and the refusals
// that are ordinary waiting do not.
func TestBlockedMergeClassifiesRefusals(t *testing.T) {
	request := forge.ChangeRequest{Number: 9, HeadSHA: "abc1234", Branch: "feature/5-x"}
	cases := []struct {
		name    string
		err     error
		want    bool
		action  string // substring of the expected action; empty when !want
		present string // substring the reason must keep
	}{
		{"conflict", errors.New("refusing merge: change request #9 is not mergeable"),
			true, "rebase", "not mergeable"},
		{"red checks", errors.New("refusing merge: checks are not green: ci failed"),
			true, "re-run", "checks are not green"},
		{"stale verdict", errors.New("refusing merge: verdict sha abc1234 does not match head def5678"),
			true, "re-review", "does not match head"},
		{"undraftable", errors.New("refusing merge: change request #9 is a draft and could not be marked ready: boom"),
			true, "ready", "could not be marked ready"},
		// Ordinary waiting is not a stall: nothing to review yet, a body that
		// never closes the item, two branches that need a human choice anyway.
		{"awaiting review", errors.New("refusing merge: no ACCEPT verdict for dispatch 3"), false, "", ""},
		{"body mismatch", errors.New("refusing merge: body does not close #5"), false, "", ""},
		{"two branches", errors.New("refusing merge: live dispatch on feature/5-x and change request #9 is on feature/5-y"), false, "", ""},
	}
	for _, tc := range cases {
		blocked, ok := blockedMerge(5, request, tc.err)
		if ok != tc.want {
			t.Fatalf("%s: blocked = %v, want %v", tc.name, ok, tc.want)
		}
		if !tc.want {
			continue
		}
		if !containsAll(blocked.Action, tc.action) {
			t.Fatalf("%s: action = %q, want it to name %q", tc.name, blocked.Action, tc.action)
		}
		if !containsAll(blocked.Reason, tc.present) {
			t.Fatalf("%s: reason = %q, want it to keep %q", tc.name, blocked.Reason, tc.present)
		}
		if blocked.SHA != "abc1234" || blocked.CR != 9 || blocked.Item != 5 {
			t.Fatalf("%s: blocked = %+v, want the request's facts", tc.name, blocked)
		}
	}
	if _, ok := blockedMerge(5, request, nil); ok {
		t.Fatal("nil error classified as blocked")
	}
}

func containsAll(text string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(text, needle) {
			return false
		}
	}
	return true
}
