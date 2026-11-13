package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
	"github.com/gofiber/fiber/v3"
)

func TestBoardReportsStaleWithLastSyncAndCachedItems(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fleet.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	projectID, err := st.CreateProject(ctx, store.ProjectInput{
		Key: "app", Name: "app", ForgeKind: "github", ForgeProject: "dinhtrung/fleet-console",
		Rig: "fleet-console", ConcurrencyCap: 2, Enabled: true,
	})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	at := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	if err := st.ReplaceItems(ctx, projectID, []store.ItemRecord{{
		Number: 7, Kind: "task", State: "open", Title: "One", Labels: "", BlockedByJSON: "[]", URL: "u", UpdatedAt: at,
	}}); err != nil {
		t.Fatalf("items: %v", err)
	}
	if err := st.SetForgeStamp(ctx, projectID, at); err != nil {
		t.Fatalf("stamp: %v", err)
	}
	if err := st.PutSnapshot(ctx, projectID, "status", map[string]any{"forge_stale": true, "forge_error": "gh: 401"}, at); err != nil {
		t.Fatalf("status: %v", err)
	}

	app, err := New(Config{Token: "test-token", Store: st, ConfigPath: "/tmp/fake-config.yaml"})
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/board", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer test-token")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	var board struct {
		Stale    bool   `json:"stale"`
		SyncedAt string `json:"synced_at"`
		Projects []struct {
			Key   string `json:"key"`
			Items []struct {
				Number int `json:"number"`
			} `json:"items"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(body, &board); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if !board.Stale {
		t.Fatalf("board = %s, want stale", body)
	}
	if board.SyncedAt != "2026-10-29T10:00:00Z" {
		t.Fatalf("synced_at = %q, want the last successful tick", board.SyncedAt)
	}
	if len(board.Projects) != 1 || len(board.Projects[0].Items) != 1 || board.Projects[0].Items[0].Number != 7 {
		t.Fatalf("cached items were lost: %s", body)
	}
}

type decisionRunner func(args []string) ([]byte, error)

func (f decisionRunner) Run(_ context.Context, args ...string) ([]byte, error) { return f(args) }

type decisionsResponse struct {
	Decisions []struct {
		ID        string `json:"id"`
		State     string `json:"state"`
		Questions []struct {
			ID       string `json:"id"`
			Question string `json:"question"`
			Options  []struct {
				ID          string `json:"id"`
				Label       string `json:"label"`
				Recommended bool   `json:"recommended"`
			} `json:"options"`
		} `json:"questions"`
	} `json:"decisions"`
}

func decisionsApp(t *testing.T, list, show string) *fiber.App {
	t.Helper()
	runner := decisionRunner(func(args []string) ([]byte, error) {
		line := strings.Join(args, " ")
		switch {
		case strings.HasPrefix(line, "queue list"):
			return []byte(list), nil
		case strings.HasPrefix(line, "queue show"):
			return []byte(show), nil
		}
		return nil, fmt.Errorf("unexpected call: %s", line)
	})
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "fleet.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	app, err := New(Config{Token: "test-token", Store: st, ConfigPath: "/tmp/fake-config.yaml", Rig: rig.NewAdapter(runner)})
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	return app
}

func getDecisions(t *testing.T, app *fiber.App) decisionsResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/decisions", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer test-token")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	var out decisionsResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return out
}

func TestDecisionsListsPendingHumanRow(t *testing.T) {
	list := `[{"qitemId":"q1","sourceSession":"a","destinationSession":"human@kernel","state":"pending","pickup":{"state":"unclaimed"}}]`
	show := `{"qitemId":"q1","sourceSession":"a","destinationSession":"human@kernel","state":"pending","pickup":{"state":"unclaimed"},"waiting":{"owner":"human@kernel","state":"pending"}}`
	got := getDecisions(t, decisionsApp(t, list, show))
	if len(got.Decisions) != 1 || got.Decisions[0].ID != "q1" {
		t.Fatalf("decisions = %+v, want the pending human row", got.Decisions)
	}
}

func TestDecisionsListsBlockedOnHumanRow(t *testing.T) {
	list := `[{"qitemId":"q2","sourceSession":"a","destinationSession":"bot@rig","state":"blocked","pickup":{"state":"parked"}}]`
	show := `{"qitemId":"q2","sourceSession":"a","destinationSession":"bot@rig","state":"blocked","pickup":{"state":"parked"},"blockedOn":"human@kernel"}`
	got := getDecisions(t, decisionsApp(t, list, show))
	if len(got.Decisions) != 1 || got.Decisions[0].ID != "q2" {
		t.Fatalf("decisions = %+v, want the blocked-on-human row", got.Decisions)
	}
}

func TestDecisionsUsesRowQuestions(t *testing.T) {
	list := `[{"qitemId":"q3","sourceSession":"a","destinationSession":"human@kernel","state":"pending","pickup":{"state":"unclaimed"}}]`
	show := `{"qitemId":"q3","sourceSession":"a","destinationSession":"human@kernel","state":"pending","pickup":{"state":"unclaimed"},"humanQuestions":[{"id":"q1","question":"What now?","options":[{"id":"rebase","label":"Rebase"},{"id":"redispatch","label":"Re-dispatch"},{"id":"close","label":"Close as superseded","recommended":true}]}]}`
	got := getDecisions(t, decisionsApp(t, list, show))
	if len(got.Decisions) != 1 || len(got.Decisions[0].Questions) != 1 {
		t.Fatalf("decisions = %+v, want one question", got.Decisions)
	}
	options := got.Decisions[0].Questions[0].Options
	if len(options) != 3 || options[2].Label != "Close as superseded" || !options[2].Recommended {
		t.Fatalf("options = %+v, want the row's three options", options)
	}
	for _, option := range options {
		if option.Label == "Approve" || option.Label == "Reject" {
			t.Fatalf("invented option %q survived", option.Label)
		}
	}
}

func TestDecisionsSkipsPendingBotRow(t *testing.T) {
	list := `[{"qitemId":"q4","sourceSession":"a","destinationSession":"bot@rig","state":"pending","pickup":{"state":"unclaimed"}}]`
	show := `{"qitemId":"q4","sourceSession":"a","destinationSession":"bot@rig","state":"pending","pickup":{"state":"unclaimed"}}`
	if got := getDecisions(t, decisionsApp(t, list, show)); len(got.Decisions) != 0 {
		t.Fatalf("decisions = %+v, want no pending bot row", got.Decisions)
	}
}

// TestBoardMovesQueuedToBuildingAfterDispatch: the act loop writes a dispatch
// row; the board reads that row as building without any external timer.
func TestBoardMovesQueuedToBuildingAfterDispatch(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fleet.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	projectID, err := st.CreateProject(ctx, store.ProjectInput{
		Key: "app", Name: "app", ForgeKind: "github", ForgeProject: "dinhtrung/fleet-console",
		Rig: "fleet-console", ConcurrencyCap: 2, Enabled: true,
	})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	at := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	if err := st.ReplaceItems(ctx, projectID, []store.ItemRecord{{
		Number: 7, Kind: "task", State: "open", Title: "One", Labels: "", BlockedByJSON: "[]",
		URL: "u", UpdatedAt: at,
	}}); err != nil {
		t.Fatalf("items: %v", err)
	}
	if _, err := st.CreateDispatch(ctx, store.NewDispatch{
		ProjectID: projectID, ItemNumber: 7, Attempt: 1, Seat: "dev-build-2@fleet-console",
		Branch: "feature/7-one", Worktree: "wt", BaseSHA: "base", CreatedAt: at,
	}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	app, err := New(Config{
		Token: "test-token", Store: st, ConfigPath: "/tmp/fake-config.yaml",
		Compat: func(context.Context) rig.Report { return rig.Report{OK: true, Status: rig.StatusOK} },
	})
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/board", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer test-token")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	var board struct {
		Projects []struct {
			Items []struct {
				Number int    `json:"number"`
				State  string `json:"state"`
				Seat   string `json:"seat"`
			} `json:"items"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(body, &board); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if len(board.Projects) != 1 || len(board.Projects[0].Items) != 1 {
		t.Fatalf("board = %s, want one item", body)
	}
	item := board.Projects[0].Items[0]
	if item.State != "building" || item.Seat != "dev-build-2@fleet-console" {
		t.Fatalf("item = %+v, want building on the dispatch's seat", item)
	}
}

// TestBoardListsUnansweredQuestions (#377): a question the ledger still holds
// shows on its project's board row with everything the operator needs to
// answer it, a row the queue says is done drops out, and a project with none
// answers with an empty list rather than a null.
func TestBoardListsUnansweredQuestions(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fleet.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	projectID, err := st.CreateProject(ctx, store.ProjectInput{
		Key: "app", Name: "app", ForgeKind: "github", ForgeProject: "dinhtrung/fleet-console",
		Rig: "fleet-console", ConcurrencyCap: 2, Enabled: true,
	})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	quietID, err := st.CreateProject(ctx, store.ProjectInput{
		Key: "quiet", Name: "quiet", ForgeKind: "github", ForgeProject: "o/quiet",
		Rig: "fleet-console", ConcurrencyCap: 1, Enabled: true,
	})
	if err != nil || quietID == 0 {
		t.Fatalf("second project: %v", err)
	}
	waiting := "app #63: note n1 on change request #75 cannot be read as a verdict (names no commit); asked operator-agent@kernel; note=n1; head=aaa111; row qitem-open"
	answered := "app #64: note n2 on change request #76 cannot be read as a verdict (names no commit); asked human@kernel; note=n2; head=bbb222; row qitem-done"
	for _, event := range []store.Event{
		{Actor: "act", Kind: "decision.verdict_question", Subject: "#63", Detail: waiting},
		{Actor: "act", Kind: "decision.verdict_question", Subject: "#64", Detail: answered},
	} {
		if _, err := st.AppendEvent(ctx, event); err != nil {
			t.Fatalf("event: %v", err)
		}
	}
	now := time.Now().UTC()
	if err := st.PutSnapshot(ctx, projectID, "rig_queue", []map[string]string{
		{"id": "qitem-open", "state": "pending", "pickup_state": "unclaimed"},
		{"id": "qitem-done", "state": "done", "pickup_state": "terminal"},
	}, now); err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	app, err := New(Config{Token: "test-token", Store: st, ConfigPath: "/tmp/fake-config.yaml"})
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/board", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer test-token")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	var board struct {
		Projects []struct {
			Key       string `json:"key"`
			Questions []struct {
				Item          int    `json:"item"`
				ChangeRequest int    `json:"change_request"`
				NoteID        string `json:"note_id"`
				Head          string `json:"head"`
				Destination   string `json:"destination"`
				Waiting       string `json:"waiting"`
				QItemID       string `json:"qitem"`
			} `json:"questions"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(body, &board); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	byKey := map[string]int{}
	for index, project := range board.Projects {
		byKey[project.Key] = index
	}
	appRow := board.Projects[byKey["app"]]
	if len(appRow.Questions) != 1 {
		t.Fatalf("questions = %+v, want the one unanswered question", appRow.Questions)
	}
	q := appRow.Questions[0]
	if q.Item != 63 || q.ChangeRequest != 75 || q.NoteID != "n1" || q.Head != "aaa111" ||
		q.Destination != "operator-agent@kernel" || q.QItemID != "qitem-open" || q.Waiting == "" {
		t.Fatalf("question = %+v, want item, cr, note, head, destination, waiting and row", q)
	}
	quietRow := board.Projects[byKey["quiet"]]
	if quietRow.Questions == nil || len(quietRow.Questions) != 0 {
		t.Fatalf("quiet questions = %+v, want an empty list", quietRow.Questions)
	}
}
