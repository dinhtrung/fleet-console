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

// whyHarness is the board over one project, used for the why-column cases.
type whyHarness struct {
	app *fiber.App
	st  *store.Store
	pid int64
}

func newWhyHarness(t *testing.T) *whyHarness {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fleet.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	pid, err := st.CreateProject(ctx, store.ProjectInput{
		Key: "app", Name: "app", ForgeKind: "github", ForgeProject: "o/r",
		Rig: "rig", ConcurrencyCap: 2, Enabled: true,
	})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	at := time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC)
	if err := st.ReplaceItems(ctx, pid, []store.ItemRecord{{
		Number: 7, Kind: "task", State: "open", Title: "Seven", Labels: "",
		BlockedByJSON: "[]", URL: "u", UpdatedAt: at,
	}}); err != nil {
		t.Fatalf("items: %v", err)
	}
	app, err := New(Config{
		Token: testToken, Store: st,
		Compat: func(context.Context) rig.Report { return rig.Report{OK: true, Status: rig.StatusOK} },
		Now:    func() time.Time { return at.Add(2 * time.Minute) },
	})
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	return &whyHarness{app: app, st: st, pid: pid}
}

// boardItemWhy reads the one item's why-column facts off /api/board.
func (h *whyHarness) boardItemWhy(t *testing.T) (planned bool, reason, age string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/board", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer "+testToken)
	resp, err := h.app.Test(req)
	if err != nil {
		t.Fatalf("board: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var board struct {
		Projects []struct {
			Key     string `json:"key"`
			Planned bool   `json:"planned"`
			Items   []struct {
				Number     int    `json:"number"`
				PlanReason string `json:"plan_reason"`
				PlanAge    string `json:"plan_age"`
			} `json:"items"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(body, &board); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	for _, project := range board.Projects {
		if project.Key != "app" {
			continue
		}
		for _, item := range project.Items {
			if item.Number == 7 {
				return project.Planned, item.PlanReason, item.PlanAge
			}
		}
	}
	t.Fatalf("no item 7 in %s", body)
	return false, "", ""
}

// TestWhyColumnReadsTheRecordedPlanReason: the item's why-column is the last
// plan's own words, with its detail and age (#429, reading #428's table).
func TestWhyColumnReadsTheRecordedPlanReason(t *testing.T) {
	ctx := context.Background()
	h := newWhyHarness(t)
	if err := h.st.ReplacePlanReasons(ctx, h.pid, []store.PlanReason{{
		ItemNumber: 7, Reason: "no_free_builder",
		Detail: "every admitted seat is taken (1 waiting on a human)",
		Head:   "aaaa1111111111111111111111111111111a",
		TickAt: time.Date(2026, 11, 14, 1, 1, 0, 0, time.UTC),
	}}); err != nil {
		t.Fatalf("plan reasons: %v", err)
	}

	planned, reason, age := h.boardItemWhy(t)
	if !planned {
		t.Fatalf("planned = false, want true after a reason write")
	}
	if reason != "no_free_builder — every admitted seat is taken (1 waiting on a human)" {
		t.Fatalf("plan_reason = %q, want reason and detail", reason)
	}
	// The harness clock is at+2m; the record is at+1m.
	if age != "1m" {
		t.Fatalf("plan_age = %q, want the record's age", age)
	}
}

// TestWhyColumnWithoutARecord: a project that has been planned but carries no
// reason for this item says nothing about it — the column is not blank because
// the state-derived reason is still there, and `planned` is true so the surface
// does not claim no plan has run.
func TestWhyColumnWithoutARecord(t *testing.T) {
	ctx := context.Background()
	h := newWhyHarness(t)
	// A tick that skipped nothing: the status block exists, the plan_reason
	// table is empty.
	if err := h.st.PutSnapshot(ctx, h.pid, "status", map[string]any{}, time.Date(2026, 11, 14, 1, 1, 0, 0, time.UTC)); err != nil {
		t.Fatalf("status: %v", err)
	}

	planned, reason, age := h.boardItemWhy(t)
	if !planned {
		t.Fatalf("planned = false, want true once a tick ran")
	}
	if reason != "" || age != "" {
		t.Fatalf("plan reason = %q age = %q, want none recorded", reason, age)
	}
}

// TestWhyColumnBeforeAnyTick: with no tick since start the table is empty for a
// different reason, and `planned` says so — the surface then writes "no plan has
// run since start" instead of a blank cell.
func TestWhyColumnBeforeAnyTick(t *testing.T) {
	h := newWhyHarness(t)
	planned, reason, _ := h.boardItemWhy(t)
	if planned {
		t.Fatalf("planned = true, want false before any tick")
	}
	if reason != "" {
		t.Fatalf("plan reason = %q, want none", reason)
	}
}
