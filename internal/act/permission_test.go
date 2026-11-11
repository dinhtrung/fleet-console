package act

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// permissionForge is a fake that answers chosen writes with a
// forge.PermissionError (#258) while the rest go through: the refusal is the
// credential's, not the fake's, and the writes that do land stay recorded.
type permissionForge struct {
	*forge.Fake
	refusals map[string]error // op -> the error the write answers with
}

func (f *permissionForge) Comment(ctx context.Context, number int, body string) error {
	if err, ok := f.refusals["comment"]; ok {
		return err
	}
	return f.Fake.Comment(ctx, number, body)
}

func (f *permissionForge) Ready(ctx context.Context, number int) error {
	if err, ok := f.refusals["ready"]; ok {
		return err
	}
	return f.Fake.Ready(ctx, number)
}

func (f *permissionForge) Merge(ctx context.Context, number int, sha string) error {
	if err, ok := f.refusals["merge"]; ok {
		return err
	}
	return f.Fake.Merge(ctx, number, sha)
}

func (f *permissionForge) CloseItem(ctx context.Context, number int) error {
	if err, ok := f.refusals["close"]; ok {
		return err
	}
	return f.Fake.CloseItem(ctx, number)
}

var _ forge.Forge = (*permissionForge)(nil)

// refused is the forge's own sentence: the credential may not write.
func refused(op, detail string) error {
	return &forge.PermissionError{Operation: op, Forge: "github", Detail: detail}
}

func permissionFixture(t *testing.T, refusals map[string]error) (context.Context, *store.Store, *queueStub, *[]DecisionRaised, TickInput) {
	t.Helper()
	ctx, st, _, fake, in := watchdogFixture(t, false)
	fake.RequestsValue[0].Draft = true
	in.CRs = fake.RequestsValue
	queue := &queueStub{created: &rig.CreateRequest{}}
	var raised []DecisionRaised
	in.Rig = queue
	in.OnDecision = func(r DecisionRaised) { raised = append(raised, r) }
	in.Forge = &permissionForge{Fake: fake, refusals: refusals}
	return ctx, st, queue, &raised, in
}

// TestPermissionRefusalRaisesOperatorQuestion runs the brief's evidence table:
// a PermissionError on Ready, Merge and CloseItem answers one queue create
// each, the row's destination is human@kernel, one audit event of kind
// forge.permission_denied, and the journal line names the permission —
// `permission: ready: …` — rather than an anonymous error (#260).
func TestPermissionRefusalRaisesOperatorQuestion(t *testing.T) {
	// {name, refusals, want the operation the question names, merged}
	cases := []struct {
		name       string
		refusals   map[string]error
		wantOp     string
		wantMerged bool // close-refused: the merge lands, the item stays open
	}{
		{"ready refused", map[string]error{"ready": refused("ready", "gh: HTTP 403: Resource not accessible by personal access token")}, "ready", false},
		{"merge refused", map[string]error{"merge": refused("merge", "gh: HTTP 403: Resource not accessible by personal access token")}, "merge", false},
		{"close refused after the merge", map[string]error{"close": refused("close", "gh: HTTP 403: Resource not accessible by personal access token")}, "close", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, st, queue, raised, in := permissionFixture(t, tc.refusals)

			report, err := RunTick(ctx, in)
			if err != nil {
				t.Fatalf("tick: %v", err)
			}
			if queue.createCalls != 1 {
				t.Fatalf("queue creates = %d, want 1", queue.createCalls)
			}
			if created := queue.created; created.Destination != "human@kernel" || created.Source != "human@kernel" {
				t.Fatalf("row = %s from %s, want human@kernel both ways", created.Destination, created.Source)
			}
			if len(*raised) != 1 || (*raised)[0].Item != 5 {
				t.Fatalf("decisions = %+v, want one raise for item 5", *raised)
			}
			// The post-merge close rides no dispatch, so its attempt is 0; the
			// pre-merge writes carry the dispatch's attempt.
			attempt := 1
			if tc.wantMerged {
				attempt = 0
			}
			key := fmt.Sprintf("attempt=%d operation=%s", attempt, tc.wantOp)
			if exists, err := st.HasEvent(ctx, EventPermissionDenied, "#5", key); err != nil || !exists {
				t.Fatalf("HasEvent(%s) = %v, %v; want one forge.permission_denied event", key, exists, err)
			}
			want := "#5 permission: " + tc.wantOp + ": gh: HTTP 403:"
			var line string
			for _, skip := range report.Skipped {
				if strings.Contains(skip, want) {
					line = skip
				}
			}
			if line == "" {
				t.Fatalf("skipped = %+v, want a journal line %q", report.Skipped, want)
			}
			// Not a retry: a refused pre-merge write leaves the dispatch live,
			// so decide's plan never sees the refusal as progress. (The
			// close-refused case is post-merge: the dispatch ended with the
			// merge, the way it would have without the refusal.)
			dispatches, err := st.Dispatches(ctx, in.Project.ID, 5)
			if err != nil || len(dispatches) != 1 {
				t.Fatalf("dispatch = %+v, %v", dispatches, err)
			}
			if !tc.wantMerged && dispatches[0].EndedAt != nil {
				t.Fatalf("dispatch = %+v, want it left live — the refusal is not progress", dispatches[0])
			}
			if tc.wantMerged {
				if len(report.Merges) != 1 || !report.Merges[0].Merged {
					t.Fatalf("report = %+v, want the merge to still land", report)
				}
				if report.Merges[0].Permission == nil {
					t.Fatalf("result = %+v, want the refusal riding the merge", report.Merges[0])
				}
			} else if len(report.Merges) != 0 {
				t.Fatalf("report = %+v, want no merge recorded", report)
			}
			if report.BlockedMerges != nil {
				t.Fatalf("blocked merges = %+v, want none: the permission question is the one page", report.BlockedMerges)
			}
		})
	}
}

// TestPermissionRefusalRaisesOncePerKey runs the same refused tick twice: the
// second tick dedupes on the ledger — NoOp, zero new rows, one event — but the
// journal still keeps the permission line, because the write is still refused.
func TestPermissionRefusalRaisesOncePerKey(t *testing.T) {
	ctx, _, queue, raised, in := permissionFixture(t, map[string]error{
		"ready": refused("ready", "gh: HTTP 403: Resource not accessible by personal access token"),
	})
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("first tick: %v", err)
	}
	if queue.createCalls != 1 || len(*raised) != 1 {
		t.Fatalf("after first tick: creates=%d raised=%d, want 1/1", queue.createCalls, len(*raised))
	}
	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if queue.createCalls != 1 || len(*raised) != 1 {
		t.Fatalf("after second tick: creates=%d raised=%d, want NoOp — 1/1", queue.createCalls, len(*raised))
	}
	found := false
	for _, skip := range report.Skipped {
		if strings.Contains(skip, "#5 permission: ready:") {
			found = true
		}
	}
	if !found {
		t.Fatalf("second tick skipped = %+v, want the journal line kept", report.Skipped)
	}
}

// TestPermissionDifferentOperationRaisesNewQuestion proves the key is per
// operation: a fixed Ready followed by a refused Merge is a new question.
func TestPermissionDifferentOperationRaisesNewQuestion(t *testing.T) {
	ctx, _, queue, raised, in := permissionFixture(t, map[string]error{
		"ready": refused("ready", "gh: HTTP 403: first refusal"),
	})
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("first tick: %v", err)
	}
	if queue.createCalls != 1 {
		t.Fatalf("first tick creates = %d, want 1", queue.createCalls)
	}
	// The token now allows ready but not merge: a different operation at the
	// same attempt is a new key.
	in.Forge = &permissionForge{Fake: in.Forge.(*permissionForge).Fake, refusals: map[string]error{
		"merge": refused("merge", "gh: HTTP 403: second refusal"),
	}}
	if _, err := RunTick(ctx, in); err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if queue.createCalls != 2 || len(*raised) != 2 {
		t.Fatalf("creates=%d raised=%d, want one new question", queue.createCalls, len(*raised))
	}
}

// TestTransientRefusalIsNotAPermission proves the other direction of the
// brief's table: a 500 from the same call raises nothing — no queue create, no
// event — and the journal keeps its ordinary merge-wait line.
func TestTransientRefusalIsNotAPermission(t *testing.T) {
	ctx, st, queue, raised, in := permissionFixture(t, nil)
	in.Forge = &permissionForge{Fake: in.Forge.(*permissionForge).Fake, refusals: map[string]error{
		"ready": errors.New("gh: HTTP 500: temporary failure"),
	}}
	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if queue.createCalls != 0 || len(*raised) != 0 {
		t.Fatalf("creates=%d raised=%d, want none for a transient error", queue.createCalls, len(*raised))
	}
	exists, err := st.HasEvent(ctx, EventPermissionDenied, "#5", "attempt=1")
	if err != nil || exists {
		t.Fatalf("HasEvent = %v, %v; want no permission event", exists, err)
	}
	found := false
	for _, skip := range report.Skipped {
		if strings.Contains(skip, "#5 merge waiting:") && strings.Contains(skip, "could not be marked ready") {
			found = true
		}
	}
	if !found {
		t.Fatalf("skipped = %+v, want the ordinary merge-wait line", report.Skipped)
	}
}

// TestSweepCommentPermissionRefusal covers the epic path: the sweep's comment
// refused for permission raises the question with attempt 0 and journals
// `#<epic> permission: comment: …`.
func TestSweepCommentPermissionRefusal(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	epicNumber := 20
	st := newStore(t)
	pid := newProject(t, st)
	fake := forge.NewFake(forge.KindGitHub)
	fake.ItemsValue = []forge.Item{
		{Number: 20, Kind: forge.KindEpic, State: forge.StateOpen, Title: "The epic"},
		{Number: 21, Kind: forge.KindTask, State: forge.StateClosed, Title: "Child"},
	}
	fake.SubIssuesValue = map[int][]forge.Item{20: {{Number: 21, Kind: forge.KindTask, State: forge.StateClosed, Parent: &epicNumber}}}
	queue := &queueStub{created: &rig.CreateRequest{}}
	var raised []DecisionRaised
	project := store.ProjectRow{ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", ConcurrencyCap: 2, Enabled: true}
	in := TickInput{
		Store: st, Project: project, Forge: fake, Rig: queue, Git: &fakeGit{},
		Gate: openGate(), Apply: true, Items: fake.ItemsValue,
		WorktreeRoot: t.TempDir(), Now: func() time.Time { return now },
		OnDecision: func(r DecisionRaised) { raised = append(raised, r) },
	}
	in.Forge = &permissionForge{Fake: fake, refusals: map[string]error{
		"comment": refused("comment", "gh: HTTP 403: Resource not accessible by personal access token"),
	}}
	report, err := RunTick(ctx, in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if queue.createCalls != 1 {
		t.Fatalf("creates = %d, want 1", queue.createCalls)
	}
	if created := *queue.created; created.Destination != "human@kernel" {
		t.Fatalf("row = %s, want human@kernel", created.Destination)
	}
	if len(raised) != 1 || raised[0].Item != 20 {
		t.Fatalf("decisions = %+v, want one raise for the epic", raised)
	}
	exists, err := st.HasEvent(ctx, EventPermissionDenied, "#20", "attempt=0 operation=comment")
	if err != nil || !exists {
		t.Fatalf("HasEvent = %v, %v; want the epic's permission event", exists, err)
	}
	found := false
	for _, skip := range report.Skipped {
		if strings.Contains(skip, "#20 permission: comment: gh: HTTP 403:") {
			found = true
		}
	}
	if !found {
		t.Fatalf("skipped = %+v, want the permission journal line", report.Skipped)
	}
	// The epic stays open for the next tick, which retries by itself.
	items, err := fake.Items(ctx, forge.Timeout{})
	if err != nil || items[0].State != forge.StateOpen {
		t.Fatalf("epic = %+v, %v; want it left open", items[0], err)
	}
}

// TestRaisePermissionDecisionBrief carries the question's content contract:
// item and title, operation, the forge's own sentence, the change request and
// head sha, both token fixes, and the retry sentence.
func TestRaisePermissionDecisionBrief(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	queue := &queueStub{created: &rig.CreateRequest{}, item: &rig.QueueItem{ID: "qitem-perm", State: "pending"}}
	dir := t.TempDir()
	actions := &Actions{Store: st, Rig: queue, ProjectID: pid, ProjectKey: "app", WorktreeRoot: dir}
	sentence := "gh: HTTP 403: Resource not accessible by personal access token"
	refusal := func() *forge.PermissionError {
		return &forge.PermissionError{Operation: "ready", Forge: "github", Detail: sentence}
	}
	raise, err := actions.RaisePermissionDecision(ctx, PermissionInput{
		Item: 5, Title: "Five", Operation: "ready", Refusal: refusal(),
		Attempt: 1, CR: 9, SHA: "abc1234", ProjectKey: "app",
	})
	if err != nil {
		t.Fatalf("raise: %v", err)
	}
	if raise.NoOp {
		t.Fatalf("raise = NoOp, want a new question")
	}
	body, err := os.ReadFile(queue.created.BodyFile)
	if err != nil {
		t.Fatalf("brief: %v", err)
	}
	text := string(body)
	for _, want := range []string{
		"app #5 Five", "ready", "gh: HTTP 403: Resource not accessible",
		"#9 at abc1234", "gh auth refresh -s repo", "Issues: write", "Pull requests: write",
		"The fleet retries by itself on the next tick after the token is fixed",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("brief = %q, want it to carry %q", text, want)
		}
	}
	exists, err := st.HasEvent(ctx, EventPermissionDenied, "#5", "attempt=1 operation=ready")
	if err != nil || !exists {
		t.Fatalf("HasEvent = %v, %v; want the dedupe event", exists, err)
	}
	// The same failure again is a NoOp; a new attempt is a new question.
	noop, err := actions.RaisePermissionDecision(ctx, PermissionInput{
		Item: 5, Operation: "ready", Refusal: refusal(), Attempt: 1,
	})
	if err != nil || !noop.NoOp {
		t.Fatalf("second raise = %+v, %v; want NoOp", noop, err)
	}
	if queue.createCalls != 1 {
		t.Fatalf("creates = %d, want 1 — the deduped raise must not create a row", queue.createCalls)
	}
	newAttempt, err := actions.RaisePermissionDecision(ctx, PermissionInput{
		Item: 5, Operation: "ready", Refusal: refusal(), Attempt: 2,
	})
	if err != nil || newAttempt.NoOp {
		t.Fatalf("attempt-2 raise = %+v, %v; want one new question", newAttempt, err)
	}
	if queue.createCalls != 2 {
		t.Fatalf("creates = %d, want 2 — a new attempt raises again", queue.createCalls)
	}
}
