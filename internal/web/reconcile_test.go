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

// TestBoardReconciledDispatches: D4 — an ended superseded/abandoned attempt no
// longer reads as building; a watchdog-external merge reads as merged.
func TestBoardReconciledDispatches(t *testing.T) {
	cases := []struct {
		reason string
		want   string
	}{
		{"superseded", "queued"},
		{"abandoned", "queued"},
		{"merged", "review"},
	}
	for _, test := range cases {
		t.Run(test.reason, func(t *testing.T) {
			st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "fleet.db"))
			if err != nil {
				t.Fatalf("store: %v", err)
			}
			defer st.Close()
			ctx := context.Background()
			pid, err := st.CreateProject(ctx, store.ProjectInput{
				Key: "app", Name: "app", ForgeKind: "github", ForgeProject: "o/r",
				Rig: "rig", ConcurrencyCap: 2, Enabled: true,
			})
			if err != nil {
				t.Fatalf("project: %v", err)
			}
			at := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
			if err := st.ReplaceItems(ctx, pid, []store.ItemRecord{{
				Number: 7, Kind: "task", State: "open", Title: "Seven", Labels: "",
				BlockedByJSON: "[]", URL: "u", UpdatedAt: at,
			}}); err != nil {
				t.Fatalf("items: %v", err)
			}
			dispatchID, err := st.CreateDispatch(ctx, store.NewDispatch{
				ProjectID: pid, ItemNumber: 7, Attempt: 1, Seat: "seat@rig",
				Branch: "b", Worktree: "w", BaseSHA: "base", QItemID: "qitem-7", CreatedAt: at,
			})
			if err != nil {
				t.Fatalf("dispatch: %v", err)
			}
			if err := st.EndDispatch(ctx, dispatchID, test.reason); err != nil {
				t.Fatalf("end dispatch: %v", err)
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
			if item.State != test.want {
				t.Fatalf("state = %q, want %q (%s)", item.State, test.want, body)
			}
			if test.reason == "merged" && item.Reason != "merged" {
				t.Fatalf("reason = %q, want merged", item.Reason)
			}
		})
	}
}
