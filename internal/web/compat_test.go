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

	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
	"github.com/gofiber/fiber/v3"
)

const blockedReason = `queue list: required field "pickup_state" missing or empty`

func blockedCompat(context.Context) rig.Report {
	return rig.Report{OK: false, Status: rig.StatusBlocked, Error: blockedReason}
}

// TestCompatReportsBlocked: a failed probe is `blocked`, not a report that
// merely logs and lets the caller guess.
func TestCompatReportsBlocked(t *testing.T) {
	app, err := New(Config{Token: "test-token", Compat: blockedCompat})
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/compat", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer test-token")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	var report struct {
		OK     bool   `json:"ok"`
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if report.OK {
		t.Fatalf("ok = true on a failed probe: %s", body)
	}
	if report.Status != rig.StatusBlocked {
		t.Fatalf("status = %q, want %q", report.Status, rig.StatusBlocked)
	}
	if report.Error != blockedReason {
		t.Fatalf("error = %q, want the named reason", report.Error)
	}
}

// TestBoardMarksProjectsCompatBlocked: the board reads compat-blocked per
// project instead of rendering the queue as empty or idle.
func TestBoardMarksProjectsCompatBlocked(t *testing.T) {
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

	app, err := New(Config{Token: "test-token", Store: st, Compat: blockedCompat})
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
		CompatBlocked bool `json:"compat_blocked"`
		Projects      []struct {
			Status string `json:"status"`
			Reason string `json:"reason"`
			Items  []struct {
				State  string `json:"state"`
				Reason string `json:"reason"`
			} `json:"items"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(body, &board); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if !board.CompatBlocked {
		t.Fatalf("compat_blocked = false: %s", body)
	}
	if len(board.Projects) != 1 {
		t.Fatalf("projects = %d, want 1", len(board.Projects))
	}
	project := board.Projects[0]
	if project.Status != "compat-blocked" || project.Reason != blockedReason {
		t.Fatalf("project status = %q reason = %q", project.Status, project.Reason)
	}
	if len(project.Items) != 1 || project.Items[0].State != "blocked" || project.Items[0].Reason != "compat-blocked" {
		t.Fatalf("item = %+v, want compat-blocked", project.Items)
	}
}
