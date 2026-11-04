package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/store"
	"github.com/gofiber/fiber/v3"
)

// An item that was never dispatched carries no pr: label, and the drawer used to
// conclude "no change request" while the pull request was open. The forge
// snapshot is the authority; this pins that the drawer reads it.
func TestItemShowsSnapshotChangeRequestWithoutLabel(t *testing.T) {
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
	at := time.Date(2026, 11, 4, 6, 0, 0, 0, time.UTC)
	if err := st.ReplaceItems(ctx, projectID, []store.ItemRecord{{
		Number: 7, Kind: "task", State: "open", Title: "Adopted", Labels: "p1", BlockedByJSON: "[]",
		URL: "u", UpdatedAt: at,
	}}); err != nil {
		t.Fatalf("items: %v", err)
	}
	if err := st.PutSnapshot(ctx, projectID, "forge", map[string]any{
		"change_requests": []map[string]any{{
			"number": 12, "closes_item": 7, "head_sha": "abc1234", "merged": false,
			"checks": []map[string]any{{"name": "ci", "conclusion": "success"}},
		}},
	}, at); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	app, err := New(Config{Token: "test-token", Store: st, ConfigPath: "/tmp/fake-config.yaml"})
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/items/7", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer test-token")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	var body struct {
		Change struct {
			Number int `json:"number"`
			Checks []struct {
				Name       string `json:"name"`
				Conclusion string `json:"conclusion"`
			} `json:"checks"`
		} `json:"change_request"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Change.Number != 12 {
		t.Fatalf("change request %d, want 12", body.Change.Number)
	}
	if len(body.Change.Checks) != 1 || body.Change.Checks[0].Conclusion != "success" {
		t.Fatalf("checks %+v", body.Change.Checks)
	}
}
