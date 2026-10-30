package web

import (
	"context"
	"encoding/json"
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

// seedStaleVerdict builds one project/item whose newest verdict names an old sha
// while the open CR's head is new.
func seedStaleVerdict(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := context.Background()
	projectID, err := st.CreateProject(ctx, store.ProjectInput{
		Key: "app", Name: "app", ForgeKind: "github", ForgeProject: "o/r",
		Rig: "rig", ConcurrencyCap: 2, Enabled: true,
	})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	at := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	if err := st.ReplaceItems(ctx, projectID, []store.ItemRecord{{
		Number: 7, Kind: "task", State: "open", Title: "Seven", Labels: "", BlockedByJSON: "[]",
		URL: "u", UpdatedAt: at,
	}}); err != nil {
		t.Fatalf("items: %v", err)
	}
	dispatchID, err := st.CreateDispatch(ctx, store.NewDispatch{
		ProjectID: projectID, ItemNumber: 7, Attempt: 1, Seat: "dev-build-2@fleet-console",
		Branch: "feature/7-seven", Worktree: "wt", BaseSHA: "base", CreatedAt: at,
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if _, err := st.AddVerdict(ctx, store.NewVerdict{
		DispatchID: dispatchID, SHA: "oldsha1", Decision: "accept", Source: "note 1", At: at,
	}); err != nil {
		t.Fatalf("verdict: %v", err)
	}
	// The dispatch stays open while the CR is in review.
	if err := st.PutSnapshot(ctx, projectID, "forge", map[string]any{
		"change_requests": []map[string]any{
			{"number": 9, "closes_item": 7, "head_sha": "newsha1", "state": "open"},
		},
	}, at); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
}

func TestBoardShowsStaleVerdict(t *testing.T) {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "fleet.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer st.Close()
	seedStaleVerdict(t, st)
	app, err := New(Config{
		Token:      "test-token",
		ConfigPath: "/tmp/fake-config.yaml",
		Store:      st,
		Compat:     func(context.Context) rig.Report { return rig.Report{OK: true, Status: rig.StatusOK} },
	})
	if err != nil {
		t.Fatalf("app: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/board", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer test-token")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("board request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	var board struct {
		Projects []struct {
			Items []struct {
				Number int    `json:"number"`
				State  string `json:"state"`
				Reason string `json:"reason"`
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
	if item.State != "parked" || !strings.Contains(item.Reason, "verdict stale") {
		t.Fatalf("item = %+v, want parked/verdict stale", item)
	}
	if !strings.Contains(item.Reason, "oldsha1") || !strings.Contains(item.Reason, "newsha1") {
		t.Fatalf("reason = %q, want both shas", item.Reason)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/items/7?project=app", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer test-token")
	resp, err = app.Test(req)
	if err != nil {
		t.Fatalf("item request: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	var detail struct {
		VerdictStale bool   `json:"verdict_stale"`
		VerdictSHA   string `json:"verdict_sha"`
		HeadSHA      string `json:"head_sha"`
	}
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatalf("decode item %s: %v", body, err)
	}
	if !detail.VerdictStale || detail.VerdictSHA != "oldsha1" || detail.HeadSHA != "newsha1" {
		t.Fatalf("detail = %+v, want stale oldsha1 -> newsha1", detail)
	}
}
