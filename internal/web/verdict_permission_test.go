package web

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
	"github.com/gofiber/fiber/v3"
)

// permCommentForge answers the verdict note with a forge.PermissionError
// (#258) while reads go through, so the board's own write is refused for
// permission (#260).
type permCommentForge struct {
	*forge.Fake
	commentErr error
	readyErr   error
}

func (f *permCommentForge) Comment(ctx context.Context, number int, body string) error {
	if f.commentErr != nil {
		return f.commentErr
	}
	return f.Fake.Comment(ctx, number, body)
}

func (f *permCommentForge) Ready(ctx context.Context, number int) error {
	if f.readyErr != nil {
		return f.readyErr
	}
	return f.Fake.Ready(ctx, number)
}

var _ forge.Forge = (*permCommentForge)(nil)

// verdictPermissionFixture is the verdict route over a forge that refuses the
// note for permission and a rig that records its queue creates.
func verdictPermissionFixture(t *testing.T, refusal error) (*fiber.App, *[]string, *store.Store) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fleet.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	projectID, err := st.CreateProject(ctx, store.ProjectInput{
		Key: "app", Name: "app", ForgeKind: "github", ForgeProject: "dinhtrung/fleet-console",
		Rig: "fleet-console", ConcurrencyCap: 2, Enabled: true,
	})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	at := time.Date(2026, 11, 4, 5, 0, 0, 0, time.UTC)
	if err := st.ReplaceItems(ctx, projectID, []store.ItemRecord{{
		Number: 7, Kind: "task", State: "open", Title: "Human verdict", Labels: "", BlockedByJSON: "[]",
		URL: "u", UpdatedAt: at,
	}}); err != nil {
		t.Fatalf("items: %v", err)
	}
	if _, err := st.CreateDispatch(ctx, store.NewDispatch{
		ProjectID: projectID, ItemNumber: 7, Attempt: 1, Seat: "dev-build-1@fleet-console",
		Branch: "feature/7-human-verdict", Worktree: "/tmp/wt", BaseSHA: "base", State: "active", CreatedAt: at,
	}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	fake := forge.NewFake(forge.KindGitHub)
	fake.RequestsValue = []forge.ChangeRequest{{
		Number: 12, State: forge.StateOpen, HeadSHA: "abc1234", Draft: true, Mergeable: true,
		ClosesItem: 7, Checks: []forge.Check{{Name: "ci", Conclusion: "success"}},
	}}
	var calls []string
	runner := decisionRunner(func(args []string) ([]byte, error) {
		line := strings.Join(args, " ")
		calls = append(calls, line)
		if strings.HasPrefix(line, "queue create") {
			return []byte(`{"qitemId":"q1","destinationSession":"human@kernel","state":"pending","pickup":{"state":"unclaimed"}}`), nil
		}
		return nil, fmt.Errorf("unexpected call: %s", line)
	})
	wrapped := &permCommentForge{Fake: fake, commentErr: refusal}
	app, err := New(Config{
		Token: "test-token", Store: st, ConfigPath: "/tmp/fake-config.yaml",
		Rig: rig.NewAdapter(runner),
		ForgeFor: func(context.Context, store.ProjectRow) (forge.Forge, error) { return wrapped, nil },
	})
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	return app, &calls, st
}

// TestVerdictPermissionRefusalAnswers502AndRaises is the brief's web evidence:
// a PermissionError on the note answers 502 and raises the operator question —
// one queue create to human@kernel, deduped on the same event ledger the act
// loop uses (#260).
func TestVerdictPermissionRefusalAnswers502AndRaises(t *testing.T) {
	refusal := &forge.PermissionError{Operation: "comment", Forge: "github",
		Detail: "gh: HTTP 403: Resource not accessible by personal access token"}
	app, calls, st := verdictPermissionFixture(t, refusal)

	status, _ := postVerdict(t, app, `{"verdict":"accept","reason":"approved"}`)
	if status != fiber.StatusBadGateway {
		t.Fatalf("status = %d, want 502 — the note did not land", status)
	}
	if len(*calls) != 1 || !strings.HasPrefix((*calls)[0], "queue create") {
		t.Fatalf("rig calls = %+v, want one queue create", *calls)
	}
	if !strings.Contains((*calls)[0], "human@kernel") {
		t.Fatalf("queue create argv = %q, want the row aimed at the human", (*calls)[0])
	}
	exists, err := st.HasEvent(context.Background(), "forge.permission_denied", "#7",
		"attempt=1 operation=comment")
	if err != nil || !exists {
		t.Fatalf("HasEvent = %v, %v; want one forge.permission_denied event", exists, err)
	}
	// The same refusal judged again is the same question: no second row.
	status, _ = postVerdict(t, app, `{"verdict":"accept","reason":"approved again"}`)
	if status != fiber.StatusBadGateway {
		t.Fatalf("second status = %d, want 502", status)
	}
	if len(*calls) != 1 {
		t.Fatalf("rig calls = %d, want the second refusal deduped", len(*calls))
	}
}

// TestVerdictTransientRefusalRaisesNothing is the other direction: a 500 from
// the note answers 502 and raises nothing.
func TestVerdictTransientRefusalRaisesNothing(t *testing.T) {
	app, calls, st := verdictPermissionFixture(t, fmt.Errorf("gh: HTTP 500: temporary failure"))

	status, _ := postVerdict(t, app, `{"verdict":"accept","reason":"approved"}`)
	if status != fiber.StatusBadGateway {
		t.Fatalf("status = %d, want 502", status)
	}
	if len(*calls) != 0 {
		t.Fatalf("rig calls = %+v, want none — a transient error is not an obligation", *calls)
	}
	exists, err := st.HasEvent(context.Background(), "forge.permission_denied", "#7", "attempt=1")
	if err != nil || exists {
		t.Fatalf("HasEvent = %v, %v; want no permission event", exists, err)
	}
}
