package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

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
