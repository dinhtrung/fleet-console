package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/store"
	"github.com/gofiber/fiber/v3"
)

// TestBoardShowsAManualTaskAsTheOwnersWork: the plan's `manual` reason is the
// one place the board learns an item is owner-only (#527), so the row says so
// in its own state — "manual (owner)" — and counts under its own bucket rather
// than reading as queued work nobody picked up.
func TestBoardShowsAManualTaskAsTheOwnersWork(t *testing.T) {
	ctx := context.Background()
	h := newWhyHarness(t)
	if err := h.st.ReplacePlanReasons(ctx, h.pid, []store.PlanReason{{
		ItemNumber: 7, Reason: "manual", Detail: "owner-only task",
		TickAt: time.Date(2026, 11, 14, 1, 1, 0, 0, time.UTC),
	}}); err != nil {
		t.Fatalf("plan reasons: %v", err)
	}

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
			Key    string `json:"key"`
			Counts struct {
				Manual int `json:"manual"`
			} `json:"counts"`
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
	for _, project := range board.Projects {
		if project.Key != "app" {
			continue
		}
		if project.Counts.Manual != 1 {
			t.Fatalf("counts.manual = %d, want 1: %+v", project.Counts.Manual, project.Counts)
		}
		for _, item := range project.Items {
			if item.Number != 7 {
				continue
			}
			if item.State != "manual" || item.Reason != "manual (owner)" {
				t.Fatalf("item = %+v, want state manual and reason manual (owner)", item)
			}
			return
		}
		t.Fatalf("no item 7 in %s", body)
	}
	t.Fatalf("no project app in %s", body)
}

// TestBoardKeepsTheProjectFactAboveManual: a paused project is the louder fact.
// The plan-reason table can lag a pause by a tick, so a stale `manual` reason
// must not repaint a paused row as the owner's live work.
func TestBoardKeepsTheProjectFactAboveManual(t *testing.T) {
	ctx := context.Background()
	h := newWhyHarness(t)
	if _, err := h.st.SetProjectOnHold(ctx, h.pid, true); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if err := h.st.ReplacePlanReasons(ctx, h.pid, []store.PlanReason{{
		ItemNumber: 7, Reason: "manual", Detail: "owner-only task",
		TickAt: time.Date(2026, 11, 14, 1, 1, 0, 0, time.UTC),
	}}); err != nil {
		t.Fatalf("plan reasons: %v", err)
	}

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
			Key   string `json:"key"`
			Items []struct {
				Number int    `json:"number"`
				State  string `json:"state"`
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
			if item.Number == 7 && item.State != "paused" {
				t.Fatalf("state = %q, want paused: the project pause outranks a stale reason", item.State)
			}
			return
		}
	}
	t.Fatalf("no item 7 in %s", body)
}
