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

	app, err := New(Config{Token: "test-token", Store: st})
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
	app, err := New(Config{Token: "test-token", Store: st, Rig: rig.NewAdapter(runner)})
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
