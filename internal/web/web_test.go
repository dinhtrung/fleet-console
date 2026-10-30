package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
	"github.com/gofiber/fiber/v3"
)

const testToken = "test-token"

func testApp(t *testing.T) *fiber.App {
	t.Helper()
	app, err := New(Config{
		Token:       testToken,
		ConfigPath:  "/tmp/fake-config.yaml",
		Compat:      func(context.Context) rig.Report {
			return rig.Report{OK: true, Version: "0.7.0", Floor: rig.Floor, VersionOK: true}
		},
	})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	return app
}

func do(t *testing.T, app *fiber.App, method, path, token, idempotencyKey string) (*http.Response, []byte) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set(fiber.HeaderAuthorization, "Bearer "+token)
	}
	if idempotencyKey != "" {
		req.Header.Set(idempotencyKeyHeader, idempotencyKey)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s %s: read body: %v", method, path, err)
	}
	return resp, body
}

func TestHealthIsOpen(t *testing.T) {
	resp, body := do(t, testApp(t), http.MethodGet, "/api/health", "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, body)
	}
}

func TestCompatRequiresBearer(t *testing.T) {
	app := testApp(t)

	if resp, _ := do(t, app, http.MethodGet, "/api/compat", "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("without token: status = %d, want 401", resp.StatusCode)
	}
	if resp, _ := do(t, app, http.MethodGet, "/api/compat", "wrong", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token: status = %d, want 401", resp.StatusCode)
	}
	resp, body := do(t, app, http.MethodGet, "/api/compat", testToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("with token: status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	var report rig.Report
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatalf("decode compat: %v", err)
	}
	if report.Version != "0.7.0" || report.Floor != rig.Floor {
		t.Fatalf("compat report = %+v", report)
	}
}

func TestRepeatedIdempotencyKeyReturnsFirstEventID(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fleet-console.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	app := testApp(t)
	var calls atomic.Int32
	app.Post("/api/test-events", func(c fiber.Ctx) error {
		calls.Add(1)
		id, err := st.AppendEvent(ctx, store.Event{Actor: "test", Kind: "test", Subject: "idempotency"})
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"event_id": id})
	})

	firstResp, firstBody := do(t, app, http.MethodPost, "/api/test-events", testToken, "key-1")
	secondResp, secondBody := do(t, app, http.MethodPost, "/api/test-events", testToken, "key-1")

	if firstResp.StatusCode != http.StatusCreated || secondResp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d then %d, want 201", firstResp.StatusCode, secondResp.StatusCode)
	}
	if string(firstBody) != string(secondBody) {
		t.Fatalf("replay body = %s, want %s", secondBody, firstBody)
	}
	var first, second struct {
		EventID int64 `json:"event_id"`
	}
	if err := json.Unmarshal(firstBody, &first); err != nil {
		t.Fatalf("decode first body: %v", err)
	}
	if err := json.Unmarshal(secondBody, &second); err != nil {
		t.Fatalf("decode second body: %v", err)
	}
	if first.EventID <= 0 || first.EventID != second.EventID {
		t.Fatalf("event ids = %d then %d, want the same positive id", first.EventID, second.EventID)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("handler ran %d times, want 1", got)
	}
}

func TestConcurrentIdempotencyKeysRunHandlerOnce(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fleet-console.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	app := testApp(t)
	var calls atomic.Int32
	app.Post("/api/test-events", func(c fiber.Ctx) error {
		calls.Add(1)
		id, err := st.AppendEvent(ctx, store.Event{Actor: "test", Kind: "test", Subject: "concurrent"})
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"event_id": id})
	})

	const workers = 4
	bodies := make([]string, workers)
	statuses := make([]int, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/api/test-events", nil)
			req.Header.Set(fiber.HeaderAuthorization, "Bearer "+testToken)
			req.Header.Set(idempotencyKeyHeader, "same-key")
			resp, err := app.Test(req)
			if err != nil {
				t.Errorf("concurrent request: %v", err)
				return
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Errorf("concurrent body: %v", err)
				return
			}
			statuses[i] = resp.StatusCode
			bodies[i] = string(body)
		}()
	}
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("handler ran %d times, want 1", got)
	}
	for i, body := range bodies {
		if statuses[i] != http.StatusCreated {
			t.Fatalf("request %d status = %d, want 201", i, statuses[i])
		}
		if body != bodies[0] {
			t.Fatalf("body %d = %s, want %s", i, body, bodies[0])
		}
	}
}

func TestMutatingRequestWithoutKeyIsRejected(t *testing.T) {
	app := testApp(t)
	app.Post("/api/test-events", func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusNoContent)
	})

	resp, body := do(t, app, http.MethodPost, "/api/test-events", testToken, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", resp.StatusCode, body)
	}
}
