package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/store"
	"github.com/gofiber/fiber/v3"
)

// readyApp builds the app with the readiness surface wired to a test clock and
// a fake queue read.
func readyApp(t *testing.T, st *store.Store, now time.Time, queueErr error) *fiber.App {
	t.Helper()
	app, err := New(Config{
		Token:      "test-token",
		Store:      st,
		ConfigPath: "/tmp/fake-config.yaml",
		Ready: &Readiness{
			TickInterval: time.Minute,
			Now:          func() time.Time { return now },
			QueueRead:    func(context.Context) error { return queueErr },
		},
	})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	return app
}

func readyStore(t *testing.T) (*store.Store, int64) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "fleet.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	pid, err := st.CreateProject(context.Background(), store.ProjectInput{
		Key: "app", Name: "app", ForgeKind: "github", ForgeProject: "o/r",
		Rig: "rig", ConcurrencyCap: 2, Enabled: true,
	})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	return st, pid
}

type readyBody struct {
	Status   string `json:"status"`
	Failures []struct {
		Fact      string `json:"fact"`
		Detail    string `json:"detail"`
		Age       string `json:"age"`
		Threshold string `json:"threshold"`
	} `json:"failures"`
}

func getReady(t *testing.T, app *fiber.App) (int, readyBody) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/ready", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("ready: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	var decoded readyBody
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return resp.StatusCode, decoded
}

// TestReadyHealthy: store answers, both reads and a tick are inside twice the
// tick interval, the queue read succeeds — 200 and an empty failure list.
func TestReadyHealthy(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	st, pid := readyStore(t)
	if err := st.SetForgeStamp(ctx, pid, now.Add(-30*time.Second)); err != nil {
		t.Fatalf("forge stamp: %v", err)
	}
	if err := st.SetRigStamp(ctx, pid, now.Add(-20*time.Second)); err != nil {
		t.Fatalf("rig stamp: %v", err)
	}
	if err := st.PutSnapshot(ctx, pid, "status", map[string]any{}, now.Add(-10*time.Second)); err != nil {
		t.Fatalf("status: %v", err)
	}

	status, body := getReady(t, readyApp(t, st, now, nil))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%+v)", status, body.Failures)
	}
	if body.Status != "ok" || len(body.Failures) != 0 {
		t.Fatalf("body = %+v, want ok with no failures", body)
	}
}

// TestReadyNamesAStaleMirror: the last forge read is older than twice the tick
// interval — 503 naming the project, its age and the threshold.
func TestReadyNamesAStaleMirror(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	st, pid := readyStore(t)
	if err := st.SetForgeStamp(ctx, pid, now.Add(-10*time.Minute)); err != nil {
		t.Fatalf("forge stamp: %v", err)
	}
	if err := st.SetRigStamp(ctx, pid, now.Add(-20*time.Second)); err != nil {
		t.Fatalf("rig stamp: %v", err)
	}
	if err := st.PutSnapshot(ctx, pid, "status", map[string]any{}, now.Add(-10*time.Second)); err != nil {
		t.Fatalf("status: %v", err)
	}

	status, body := getReady(t, readyApp(t, st, now, nil))
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", status)
	}
	if len(body.Failures) != 1 {
		t.Fatalf("failures = %+v, want only the stale forge read", body.Failures)
	}
	failure := body.Failures[0]
	if failure.Fact != "mirror.app" || failure.Age != "10m0s" || failure.Threshold != "2m0s" {
		t.Fatalf("failure = %+v, want mirror.app, 10m0s old, threshold 2m0s", failure)
	}
	if !strings.Contains(failure.Detail, "last forge read") {
		t.Fatalf("detail = %q, want the fact named", failure.Detail)
	}
}

// TestReadySlowReadInsideTheThreshold: a read 90 s old with a 2 m threshold is
// a service doing its job, not a failure — the probe must not restart it.
func TestReadySlowReadInsideTheThreshold(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	st, pid := readyStore(t)
	if err := st.SetForgeStamp(ctx, pid, now.Add(-90*time.Second)); err != nil {
		t.Fatalf("forge stamp: %v", err)
	}
	if err := st.SetRigStamp(ctx, pid, now.Add(-90*time.Second)); err != nil {
		t.Fatalf("rig stamp: %v", err)
	}
	if err := st.PutSnapshot(ctx, pid, "status", map[string]any{}, now.Add(-90*time.Second)); err != nil {
		t.Fatalf("status: %v", err)
	}

	status, body := getReady(t, readyApp(t, st, now, nil))
	if status != http.StatusOK || len(body.Failures) != 0 {
		t.Fatalf("status = %d failures = %+v, want 200 inside the threshold", status, body.Failures)
	}
}

// TestReadyNamesTheQueue: a queue read that fails is a named failure, and it is
// cached — a second probe within the TTL does not shell out again.
func TestReadyNamesTheQueue(t *testing.T) {
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	st, pid := readyStore(t)
	ctx := context.Background()
	if err := st.SetForgeStamp(ctx, pid, now); err != nil {
		t.Fatalf("forge stamp: %v", err)
	}
	if err := st.SetRigStamp(ctx, pid, now); err != nil {
		t.Fatalf("rig stamp: %v", err)
	}
	if err := st.PutSnapshot(ctx, pid, "status", map[string]any{}, now); err != nil {
		t.Fatalf("status: %v", err)
	}

	reads := 0
	app, err := New(Config{
		Token: "test-token", Store: st, ConfigPath: "/tmp/fake-config.yaml",
		Ready: &Readiness{
			TickInterval: time.Minute, Now: func() time.Time { return now },
			QueueRead: func(context.Context) error { reads++; return errors.New("rig: daemon unreachable") },
		},
	})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	status, body := getReady(t, app)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", status)
	}
	found := false
	for _, failure := range body.Failures {
		if failure.Fact == "queue" && strings.Contains(failure.Detail, "daemon unreachable") {
			found = true
		}
	}
	if !found {
		t.Fatalf("failures = %+v, want the queue named", body.Failures)
	}
	if _, _ = getReady(t, app); reads != 1 {
		t.Fatalf("queue reads = %d, want one inside the 30 s cache", reads)
	}
}

// TestHealthStaysAConstantWithDependenciesDown: the store is closed and the
// queue is unreachable, and /api/health still answers 200 — a restart is not
// the repair for a dependency, and liveness is not readiness.
func TestHealthStaysAConstantWithDependenciesDown(t *testing.T) {
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	st, _ := readyStore(t)
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	app := readyApp(t, st, now, errors.New("rig: daemon unreachable"))

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"ok"`) {
		t.Fatalf("health = %d %s, want 200 with the constant body", resp.StatusCode, body)
	}

	// And readiness on the same broken process names the store, never a bare 503.
	status, readyBody := getReady(t, app)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("ready = %d, want 503", status)
	}
	if len(readyBody.Failures) == 0 || readyBody.Failures[0].Fact != "database" {
		t.Fatalf("failures = %+v, want the database named first", readyBody.Failures)
	}
}
