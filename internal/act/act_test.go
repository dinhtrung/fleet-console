package act

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "fleet.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func newProject(t *testing.T, st *store.Store) int64 {
	t.Helper()
	id, err := st.CreateProject(context.Background(), store.ProjectInput{
		Key: "app", Name: "app", ForgeKind: "github", ForgeProject: "o/r",
		Rig: "rig", ConcurrencyCap: 2, Enabled: true,
	})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	return id
}

type fakeGit struct {
	fetched   int
	worktrees int
	reused    bool
	err       error
	// worktreePaths and branches record what the dispatcher asked for, so a test can assert the
	// worktree layout and the branch without running git.
	worktreePaths []string
	branches      []string
}

func (g *fakeGit) Fetch(context.Context) error { g.fetched++; return g.err }
func (g *fakeGit) BaseSHA(context.Context) (string, error) {
	if g.err != nil {
		return "", g.err
	}
	return "base-sha", nil
}
func (g *fakeGit) WorktreeAddOrReuse(_ context.Context, worktree, branch, _ string) (bool, error) {
	g.worktrees++
	g.worktreePaths = append(g.worktreePaths, worktree)
	g.branches = append(g.branches, branch)
	return g.reused, g.err
}

func TestDispatchCreatesOneRowAndReusesIt(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	git := &fakeGit{}
	fake := forge.NewFake(forge.KindGitHub)
	rigStub := &queueStub{item: &rig.QueueItem{ID: "qitem-1", State: "pending"}, created: &rig.CreateRequest{}}
	actions := &Actions{Store: st, Forge: fake, Rig: rigStub, Git: git}
	root := t.TempDir()
	in := DispatchInput{
		ProjectID: pid, ProjectKey: "app", Item: forge.Item{Number: 5, Kind: forge.KindTask, Title: "Pool store", Body: "body"},
		Seat: "seat@rig", WorktreeRoot: root,
	}
	first, err := actions.Dispatch(ctx, in)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if first.QItemID != "qitem-1" || first.Branch != "feature/5-pool-store" {
		t.Fatalf("result = %+v", first)
	}
	if rigStub.created.Source != "human@kernel" || rigStub.created.Destination != "seat@rig" {
		t.Fatalf("create request = %+v", rigStub.created)
	}
	if _, err := os.Stat(first.BriefPath); err != nil {
		t.Fatalf("brief: %v", err)
	}
	if !hasWrite(fake.Writes, "marker", 5, "on") {
		t.Fatalf("marker write missing: %+v", fake.Writes)
	}
	rows, _ := st.Dispatches(ctx, pid, 5)
	if len(rows) != 1 || rows[0].QItemID != "qitem-1" {
		t.Fatalf("dispatch rows = %+v", rows)
	}

	second, err := actions.Dispatch(ctx, in)
	if err != nil {
		t.Fatalf("second dispatch: %v", err)
	}
	if !second.NoOp || second.QItemID != "qitem-1" {
		t.Fatalf("second = %+v, want a no-op naming qitem-1", second)
	}
	if rigStub.createCalls != 1 || git.worktrees != 1 {
		t.Fatalf("second dispatch wrote again: create=%d worktrees=%d", rigStub.createCalls, git.worktrees)
	}
}

func hasWrite(writes []forge.Write, op string, number int, value string) bool {
	for _, write := range writes {
		if write.Op == op && write.Number == number && write.Value == value {
			return true
		}
	}
	return false
}

func TestRowActionsArgvAndEvents(t *testing.T) {
	const reply = `{"qitemId":"q1","sourceSession":"a","destinationSession":"seat@rig","state":"in-progress","pickup":{"state":"working"}}`
	cases := []struct {
		name   string
		want   string
		action func(*Actions) error
		event  string
	}{
		{"resolve", "queue resolve q1 --decision yes --json", func(a *Actions) error {
			_, err := a.ResolveRow(context.Background(), "q1", "yes")
			return err
		}, "row.resolve"},
		{"handoff", "queue handoff q1 --to seat@rig --json --note n", func(a *Actions) error {
			_, err := a.HandoffRow(context.Background(), "q1", rig.HandoffRequest{To: "seat@rig", Note: "n"})
			return err
		}, "row.handoff"},
		{"superseded", "queue update q1 --json --state canceled --closure-reason superseded --closure-target q2", func(a *Actions) error {
			_, err := a.CloseSuperseded(context.Background(), "q1", "q2")
			return err
		}, "row.superseded"},
		{"renudge", "send seat@rig hello", func(a *Actions) error {
			return a.Renudge(context.Background(), "q1", "seat@rig", "hello")
		}, "row.renudge"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			st := newStore(t)
			var calls []string
			runner := fakeRunner(func(args ...string) ([]byte, error) {
				calls = append(calls, strings.Join(args, " "))
				return []byte(reply), nil
			})
			actions := &Actions{Store: st, Rig: rig.NewAdapter(runner)}
			if err := test.action(actions); err != nil {
				t.Fatalf("action: %v", err)
			}
			if len(calls) != 1 || calls[0] != test.want {
				t.Fatalf("argv = %v, want %q", calls, test.want)
			}
			events, _ := st.RecentEvents(context.Background(), 1)
			if len(events) != 1 || events[0].Kind != test.event {
				t.Fatalf("events = %+v, want %s", events, test.event)
			}
		})
	}
}

func TestRowActionFailureWritesNoEvent(t *testing.T) {
	st := newStore(t)
	runner := fakeRunner(func(args ...string) ([]byte, error) { return nil, fmt.Errorf("exit status 1") })
	actions := &Actions{Store: st, Rig: rig.NewAdapter(runner)}
	if _, err := actions.ResolveRow(context.Background(), "q1", "yes"); err == nil {
		t.Fatal("failed resolve: want error")
	}
	events, _ := st.RecentEvents(context.Background(), 5)
	if len(events) != 0 {
		t.Fatalf("events = %+v, want none on failure", events)
	}
}

func TestVerdictCapture(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		head    string
		notes   []forge.Comment
		want    string
		wantErr bool
	}{
		{"accept", "abc1234", []forge.Comment{{ID: "1", Body: "ACCEPT abc1234", CreatedAt: "2026-10-29T10:00:00Z"}}, "accept", false},
		{"later reject vetoes", "abc1234", []forge.Comment{
			{ID: "2", Body: "REJECT abc1234", CreatedAt: "2026-10-29T11:00:00Z"},
			{ID: "1", Body: "ACCEPT abc1234", CreatedAt: "2026-10-29T10:00:00Z"},
		}, "reject", false},
		// The legitimate half of the old "older sha ignored" case: a verdict about
		// a superseded head does not override one about this head. It holds because
		// notes are read newest-first and the accept for this head is newer, not
		// because the other-head note is skipped on sight.
		{"older other-head reject does not veto a newer accept", "abc1234", []forge.Comment{
			{ID: "2", Body: "ACCEPT abc1234", CreatedAt: "2026-10-29T11:00:00Z"},
			{ID: "1", Body: "REJECT deadbee", CreatedAt: "2026-10-29T10:00:00Z"},
		}, "accept", false},
		// The defect this contract fixes: a rejection that cannot be tied to the
		// head stops the merge instead of falling through to an older accept.
		{"truncated sha in a rejection blocks", "abc1234", []forge.Comment{
			{ID: "2", Body: "REJECT abc12", CreatedAt: "2026-10-29T11:00:00Z"},
			{ID: "1", Body: "ACCEPT abc1234", CreatedAt: "2026-10-29T10:00:00Z"},
		}, "", true},
		{"rejection with no sha blocks", "abc1234", []forge.Comment{
			{ID: "2", Body: "REJECT", CreatedAt: "2026-10-29T11:00:00Z"},
			{ID: "1", Body: "ACCEPT abc1234", CreatedAt: "2026-10-29T10:00:00Z"},
		}, "", true},
		{"rejection of another sha blocks", "abc1234", []forge.Comment{
			{ID: "2", Body: "REJECT ffffffff", CreatedAt: "2026-10-29T11:00:00Z"},
			{ID: "1", Body: "ACCEPT abc1234", CreatedAt: "2026-10-29T10:00:00Z"},
		}, "", true},
		// Prose that mentions a verdict is not one, in either direction.
		{"prose accept is not a verdict", "abc1234", []forge.Comment{
			{ID: "2", Body: "Not ready to ACCEPT abc1234 until CI is green.", CreatedAt: "2026-10-29T11:00:00Z"},
			{ID: "1", Body: "REJECT abc1234", CreatedAt: "2026-10-29T10:00:00Z"},
		}, "reject", false},
		{"prose reject is not a verdict", "abc1234", []forge.Comment{
			{ID: "2", Body: "I nearly wrote REJECT abc1234 here, but it passes.", CreatedAt: "2026-10-29T11:00:00Z"},
			{ID: "1", Body: "ACCEPT abc1234", CreatedAt: "2026-10-29T10:00:00Z"},
		}, "accept", false},
		// A verdict may carry a reason after the sha; that is how people write.
		{"verdict with a trailing reason", "abc1234", []forge.Comment{
			{ID: "1", Body: "ACCEPT abc1234 (PR #78, #66)", CreatedAt: "2026-10-29T10:00:00Z"},
		}, "accept", false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			st := newStore(t)
			dispatchID, err := st.CreateDispatch(ctx, store.NewDispatch{ProjectID: 1, ItemNumber: 5, Attempt: 1, Seat: "s", Branch: "b", Worktree: "w", BaseSHA: "x", CreatedAt: now})
			if err != nil {
				t.Fatalf("dispatch: %v", err)
			}
			actions := &Actions{Store: st, Now: func() time.Time { return now }}
			captured, err := actions.CaptureVerdict(ctx, dispatchID, 5, test.head, test.notes)
			if test.wantErr {
				if !errors.Is(err, ErrUnattributableVerdict) {
					t.Fatalf("err = %v, want ErrUnattributableVerdict", err)
				}
				if captured != nil {
					t.Fatalf("captured = %+v, want none on a refusal", captured)
				}
				if verdicts, _ := st.Verdicts(ctx, dispatchID); len(verdicts) != 0 {
					t.Fatalf("verdicts = %+v, want nothing stored on a refusal", verdicts)
				}
				return
			}
			if err != nil {
				t.Fatalf("capture: %v", err)
			}
			if captured == nil || captured.Decision != test.want {
				t.Fatalf("captured = %+v, want %s", captured, test.want)
			}
			verdicts, _ := st.Verdicts(ctx, dispatchID)
			if len(verdicts) != 1 || verdicts[0].Decision != test.want {
				t.Fatalf("verdicts = %+v", verdicts)
			}
		})
	}
}

func TestMergeRefusals(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	green := forge.ChangeRequest{Number: 9, State: "open", HeadSHA: "abc1234", Mergeable: true, Body: "Closes #5", DocsOnly: true}
	cases := []struct {
		name    string
		verdict *store.NewVerdict
		request forge.ChangeRequest
		wantErr string
	}{
		{"no verdict", nil, green, "no ACCEPT verdict"},
		{"reject", &store.NewVerdict{SHA: "abc1234", Decision: "reject"}, green, "newest verdict"},
		{"sha mismatch", &store.NewVerdict{SHA: "deadbee", Decision: "accept"}, green, "does not match head"},
		{"red checks", &store.NewVerdict{SHA: "abc1234", Decision: "accept"}, func() forge.ChangeRequest {
			cr := green
			cr.DocsOnly = false
			cr.Checks = []forge.Check{{Name: "go", Status: "completed", Conclusion: "failure"}}
			return cr
		}(), "checks are not green"},
		{"not mergeable", &store.NewVerdict{SHA: "abc1234", Decision: "accept"}, func() forge.ChangeRequest {
			cr := green
			cr.Mergeable = false
			return cr
		}(), "not mergeable"},
		{"body does not close", &store.NewVerdict{SHA: "abc1234", Decision: "accept"}, func() forge.ChangeRequest {
			cr := green
			cr.ClosesItem = 5
			cr.Body = "Refs #5"
			return cr
		}(), "body does not close"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			st := newStore(t)
			dispatchID, err := st.CreateDispatch(ctx, store.NewDispatch{ProjectID: 1, ItemNumber: 5, Attempt: 1, Seat: "s", Branch: "b", Worktree: "w", BaseSHA: "x", CreatedAt: now})
			if err != nil {
				t.Fatalf("dispatch: %v", err)
			}
			if test.verdict != nil {
				test.verdict.DispatchID = dispatchID
				if _, err := st.AddVerdict(ctx, *test.verdict); err != nil {
					t.Fatalf("verdict: %v", err)
				}
			}
			fake := forge.NewFake(forge.KindGitHub)
			fake.ItemsValue = []forge.Item{{Number: 5, Kind: forge.KindTask, State: forge.StateOpen}}
			fake.RequestsValue = []forge.ChangeRequest{test.request}
			actions := &Actions{Store: st, Forge: fake, Now: func() time.Time { return now }}
			if _, err := actions.Merge(ctx, dispatchID, 5); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error = %v, want %q", err, test.wantErr)
			}
			if len(fake.Writes) != 0 {
				t.Fatalf("refused merge wrote %+v", fake.Writes)
			}
		})
	}
}

func TestMergeDocsOnlyEndToEnd(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	dispatchID, err := st.CreateDispatch(ctx, store.NewDispatch{ProjectID: 1, ItemNumber: 5, Attempt: 1, Seat: "s", Branch: "b", Worktree: "w", BaseSHA: "x", CreatedAt: now})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if _, err := st.AddVerdict(ctx, store.NewVerdict{DispatchID: dispatchID, SHA: "abc1234", Decision: "accept", Source: "note 1", At: now}); err != nil {
		t.Fatalf("verdict: %v", err)
	}
	fake := forge.NewFake(forge.KindGitHub)
	fake.ItemsValue = []forge.Item{
		{Number: 5, Kind: forge.KindTask, State: forge.StateOpen},
		{Number: 6, Kind: forge.KindTask, State: forge.StateOpen, BlockedBy: []forge.Link{{Number: 5, State: forge.StateOpen}}},
	}
	fake.RequestsValue = []forge.ChangeRequest{{Number: 9, State: "open", HeadSHA: "abc1234", Mergeable: true, Body: "Closes #5", DocsOnly: true}}
	actions := &Actions{Store: st, Forge: fake, Now: func() time.Time { return now }}
	result, err := actions.Merge(ctx, dispatchID, 5)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !result.Merged || result.SHA != "abc1234" || !result.ClosedByApp || !result.MarkerCleared || result.NextUnblocked != 6 {
		t.Fatalf("result = %+v", result)
	}
	if !hasWrite(fake.Writes, "merge", 9, "abc1234") {
		t.Fatalf("merge write missing: %+v", fake.Writes)
	}
	if !hasWrite(fake.Writes, "close", 5, "") || !hasWrite(fake.Writes, "marker", 5, "off") {
		t.Fatalf("post-merge writes = %+v", fake.Writes)
	}
	dispatch, _ := st.Dispatch(ctx, dispatchID)
	if dispatch.EndReason != "merged" {
		t.Fatalf("dispatch = %+v", dispatch)
	}
	events, _ := st.RecentEvents(ctx, 20)
	kinds := map[string]bool{}
	for _, event := range events {
		kinds[event.Kind] = true
	}
	for _, kind := range []string{"merge", "dispatch.merged", "item.close", "marker.clear", "item.unblocked"} {
		if !kinds[kind] {
			t.Fatalf("missing event %s in %+v", kind, events)
		}
	}
}

func TestFinalizeRefsClosesItemWithReason(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	fake := forge.NewFake(forge.KindGitHub)
	fake.ItemsValue = []forge.Item{{Number: 5, Kind: forge.KindTask, State: forge.StateOpen}}
	actions := &Actions{Store: st, Forge: fake}
	result, err := actions.Finalize(ctx, 0, 5, forge.ChangeRequest{Number: 9, Body: "Refs #5"}, nil)
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if !result.ClosedByApp || !hasWrite(fake.Writes, "close", 5, "") {
		t.Fatalf("result = %+v writes=%+v", result, fake.Writes)
	}
	events, _ := st.RecentEvents(ctx, 5)
	found := false
	for _, event := range events {
		if event.Kind == "item.close" && strings.Contains(event.Detail, "only referenced #5 (Refs)") {
			found = true
		}
	}
	if !found {
		t.Fatalf("events = %+v, want the Refs reason", events)
	}
}

type fakeRunner func(args ...string) ([]byte, error)

func (f fakeRunner) Run(_ context.Context, args ...string) ([]byte, error) { return f(args...) }

type queueStub struct {
	item        *rig.QueueItem
	created     *rig.CreateRequest
	// creates records every create in order, for the tests that raise more
	// than one row in one tick (#266). Optional: nil skips the recording.
	creates     *[]rig.CreateRequest
	createCalls int
	sendCalls   int
	rows        []rig.QueueRow
	shows       map[string]*rig.QueueItem
}

func (q *queueStub) QueueList(context.Context) ([]rig.QueueRow, error) {
	return q.rows, nil
}

func (q *queueStub) QueueShow(_ context.Context, id string) (*rig.QueueItem, error) {
	if item, ok := q.shows[id]; ok {
		return item, nil
	}
	return &rig.QueueItem{ID: id}, nil
}

func (q *queueStub) QueueCreate(_ context.Context, req rig.CreateRequest) (*rig.QueueItem, error) {
	q.createCalls++
	if q.created != nil {
		*q.created = req
	}
	if q.creates != nil {
		*q.creates = append(*q.creates, req)
	}
	if q.item == nil {
		return &rig.QueueItem{ID: "qitem-stub", State: "pending"}, nil
	}
	return q.item, nil
}
func (q *queueStub) QueueResolve(context.Context, string, string) (*rig.QueueItem, error) {
	return q.item, nil
}
func (q *queueStub) QueueHandoff(context.Context, string, rig.HandoffRequest) (*rig.QueueItem, error) {
	return q.item, nil
}
func (q *queueStub) QueueUpdate(context.Context, string, rig.UpdateRequest) (*rig.QueueItem, error) {
	return q.item, nil
}
func (q *queueStub) Send(context.Context, string, string) error { q.sendCalls++; return nil }

func TestTwoProjectsDispatchIsolatedWorktrees(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	appID := newProject(t, st)
	docsID, err := st.CreateProject(ctx, store.ProjectInput{
		Key: "docs", Name: "docs", ForgeKind: "github", ForgeProject: "o/docs",
		Rig: "docs-rig", ConcurrencyCap: 1, Enabled: true,
	})
	if err != nil {
		t.Fatalf("docs project: %v", err)
	}
	git := &fakeGit{}
	fake := forge.NewFake(forge.KindGitHub)
	queue := &queueStub{item: &rig.QueueItem{ID: "qitem-1", State: "pending"}, created: &rig.CreateRequest{}}
	actions := &Actions{Store: st, Forge: fake, Rig: queue, Git: git}
	root := t.TempDir()
	first, err := actions.Dispatch(ctx, DispatchInput{
		ProjectID: appID, ProjectKey: "app", Item: forge.Item{Number: 1, Kind: forge.KindTask, Title: "One"},
		Seat: "seat@rig", WorktreeRoot: root,
	})
	if err != nil {
		t.Fatalf("app dispatch: %v", err)
	}
	second, err := actions.Dispatch(ctx, DispatchInput{
		ProjectID: docsID, ProjectKey: "docs", Item: forge.Item{Number: 1, Kind: forge.KindTask, Title: "One"},
		Seat: "seat@rig", WorktreeRoot: root,
	})
	if err != nil {
		t.Fatalf("docs dispatch: %v", err)
	}
	if first.Worktree == second.Worktree {
		t.Fatalf("projects share a worktree: %s", first.Worktree)
	}
	if !strings.HasSuffix(first.Worktree, "wt-app-1") || !strings.HasSuffix(second.Worktree, "wt-docs-1") {
		t.Fatalf("worktrees = %s / %s", first.Worktree, second.Worktree)
	}
	appRows, _ := st.Dispatches(ctx, appID, 1)
	docsRows, _ := st.Dispatches(ctx, docsID, 1)
	if len(appRows) != 1 || len(docsRows) != 1 || appRows[0].ID == docsRows[0].ID {
		t.Fatalf("dispatch rows collided: app=%+v docs=%+v", appRows, docsRows)
	}
}
