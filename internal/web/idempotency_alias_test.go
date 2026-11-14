package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// idempotencyProbeApp routes three mutating requests onto one ledger and echoes
// each request's own key in the body, so a replay is visible as a body that
// belongs to a different request.
func idempotencyProbeApp(ledger *idemLedger) *fiber.App {
	app := fiber.New()
	app.Use(ledger.handler())
	app.Put("/thing", func(c fiber.Ctx) error {
		return c.SendString("ran:" + c.Get(idempotencyKeyHeader))
	})
	return app
}

func putWithKey(t *testing.T, app *fiber.App, key string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/thing", nil)
	req.Header.Set(idempotencyKeyHeader, key)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request %q: %v", key, err)
	}
	body := make([]byte, 512)
	n, _ := resp.Body.Read(body)
	return resp.StatusCode, string(body[:n])
}

// TestIdempotencyStoredKeysAreOwnedCopies is #474's regression test, and it is
// deterministic: it does not need an unlucky ordering, it reads the keys the
// ledger kept.
//
// fiber's c.Get returns a string that aliases fasthttp's reused header buffer
// (the app does not set fiber's Immutable option), and the ledger keeps the key
// for 24 hours. Storing the aliased string made the stored bytes get rewritten
// by the next request: after three requests the ledger held three entries whose
// text all read like the last key, so a lookup could match a stale entry and
// replay an unrelated response with the handler never run.
//
// A correct middleware stores its own copies, so every key in the ledger is the
// distinct text its request sent.
func TestIdempotencyStoredKeysAreOwnedCopies(t *testing.T) {
	ledger := newIdemLedger()
	app := idempotencyProbeApp(ledger)

	keys := []string{"key-one", "key-two", "key-three"}
	for _, key := range keys {
		status, body := putWithKey(t, app, key)
		if status != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", key, status)
		}
		if want := "ran:" + key; body != want {
			t.Fatalf("%s: body = %q, want %q — a replay served another request's response", key, body, want)
		}
	}

	ledger.mu.Lock()
	stored := make(map[string]bool, len(ledger.entries))
	for storedKey := range ledger.entries {
		stored[storedKey] = true
	}
	ledger.mu.Unlock()

	if len(stored) != len(keys) {
		t.Fatalf("ledger holds %d distinct keys (%v), want %d: stored keys aliased a reused buffer and collided", len(stored), stored, len(keys))
	}
	for _, key := range keys {
		if !stored[key] {
			t.Fatalf("ledger keys = %v, want %q among them", stored, key)
		}
	}
}

// TestIdempotencyReplaysTheSameKey is the promise the ledger exists for, so the
// fix above cannot be "stop deduping": the same key twice runs the handler once.
func TestIdempotencyReplaysTheSameKey(t *testing.T) {
	ledger := newIdemLedger()
	app := idempotencyProbeApp(ledger)

	first, _ := putWithKey(t, app, "the-same-key")
	second, _ := putWithKey(t, app, "the-same-key")
	if first != http.StatusOK || second != http.StatusOK {
		t.Fatalf("statuses = %d, %d, want 200 both", first, second)
	}

	ledger.mu.Lock()
	n := len(ledger.entries)
	ledger.mu.Unlock()
	if n != 1 {
		t.Fatalf("ledger entries = %d, want 1: the same key must not add a second entry", n)
	}

	// And a distinct key still runs its own handler after a replay happened.
	status, body := putWithKey(t, app, "a-different-key")
	if status != http.StatusOK || body != "ran:a-different-key" {
		t.Fatalf("distinct key after a replay = %d %q, want its own response", status, body)
	}
}

// TestIdempotencyKeysAreTrimmed pins the trimming the key has always had, so a
// padded header cannot dodge dedupe.
func TestIdempotencyKeysAreTrimmed(t *testing.T) {
	ledger := newIdemLedger()
	app := idempotencyProbeApp(ledger)

	if status, _ := putWithKey(t, app, "  padded-key  "); status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if _, ok := ledger.entries["padded-key"]; !ok {
		keys := make([]string, 0, len(ledger.entries))
		for key := range ledger.entries {
			keys = append(keys, key)
		}
		t.Fatalf("ledger keys = %v, want the trimmed \"padded-key\"", strings.Join(keys, ", "))
	}
}
