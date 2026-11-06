package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/configfile"
	"github.com/dinhtrung/fleet-console/internal/store"
	"github.com/gofiber/fiber/v3"
)

// settingsFixture carries a credential in every shape the file supports, so the
// redaction test has something real to look for.
const settingsFixture = `# The operator's own header comment.
listen: "127.0.0.1:8787"
database: "fleet-console.db"
token: "super-secret-bearer-token"

# Poll cadence.
tick_interval: "15s"
notify_interval: "0s"
act: false

rig_lifecycle:
  enabled: true
  idle_after: "10m"
  idle_poll_max: "10m"
`

type settingsHarness struct {
	app  *fiber.App
	path string
	orig []byte
}

func newSettingsHarness(t *testing.T, write func(string, map[string]string, *[32]byte) (configfile.Outcome, error)) *settingsHarness {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(settingsFixture), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	st, err := store.Open(context.Background(), filepath.Join(dir, "fleet.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	app, err := New(Config{Token: testToken, Store: st, ConfigPath: path, WriteSettings: write})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	return &settingsHarness{app: app, path: path, orig: original}
}

func (h *settingsHarness) get(t *testing.T) (*http.Response, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer "+testToken)
	resp, err := h.app.Test(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, out
}

func (h *settingsHarness) put(t *testing.T, body string) (*http.Response, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewBufferString(body))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer "+testToken)
	// Every non-GET goes through the app's idempotency ledger, so a real client
	// sends this header; the tests do the same. A unique key per call keeps a
	// replayed body out of the ledger's cache.
	req.Header.Set(idempotencyKeyHeader, "settings-"+t.Name()+"-"+body)
	resp, err := h.app.Test(req)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, out
}

// The read must answer the writable surface plus its sources and the read-only
// keys with reasons — and must never leak the credential.
func TestReadSettingsRedactsCredentials(t *testing.T) {
	h := newSettingsHarness(t, nil)
	resp, body := h.get(t)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode, body)
	}
	var got SettingsResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v: %s", err, body)
	}
	if got.Hash == "" {
		t.Error("hash is empty; a save cannot send it back")
	}
	for _, key := range []string{
		"tick_interval", "notify_interval", "act",
		"rig_lifecycle.enabled", "rig_lifecycle.idle_after", "rig_lifecycle.idle_poll_max",
	} {
		if _, ok := got.Settings[key]; !ok {
			t.Errorf("settings is missing %q", key)
		}
	}
	// The token's value must not appear anywhere in the JSON, in any shape.
	text := string(body)
	for _, secret := range []string{"super-secret-bearer-token"} {
		if strings.Contains(text, secret) {
			t.Errorf("the response leaks the credential %q:\n%s", secret, text)
		}
	}
	// The read-only list must explain itself rather than list bare keys.
	if len(got.ReadOnly) == 0 {
		t.Fatal("read_only is empty; the screen cannot explain the refused fields")
	}
	for _, entry := range got.ReadOnly {
		if entry.Key == "token" || entry.Key == "token_file" || entry.Key == "listen" {
			if strings.TrimSpace(entry.Reason) == "" {
				t.Errorf("%q has no reason", entry.Key)
			}
		}
	}
}

// A save writes the allowed key, answers saved:true and restart_required:true,
// and leaves the operator's comments in place.
func TestSaveSettingsWritesAndRequiresRestart(t *testing.T) {
	h := newSettingsHarness(t, nil)
	_, readBody := h.get(t)
	var read SettingsResponse
	if err := json.Unmarshal(readBody, &read); err != nil {
		t.Fatalf("decode read: %v", err)
	}
	body := fmt.Sprintf(`{"values":{"tick_interval":"30s"},"hash":%q}`, read.Hash)
	resp, out := h.put(t, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode, out)
	}
	var saved SaveSettingsResponse
	if err := json.Unmarshal(out, &saved); err != nil {
		t.Fatalf("decode save: %v", err)
	}
	if !saved.Saved {
		t.Error("saved = false, want true for a changed value")
	}
	if !saved.RestartRequired {
		t.Error("restart_required = false, want true for a changed value")
	}
	if saved.Settings["tick_interval"] != "30s" {
		t.Errorf("settings after save = %q, want 30s", saved.Settings["tick_interval"])
	}
	file, _ := os.ReadFile(h.path)
	if !strings.Contains(string(file), "# The operator's own header comment.") {
		t.Errorf("the header comment was lost:\n%s", file)
	}
	if !strings.Contains(string(file), `tick_interval: "30s"`) {
		t.Errorf("the value was not written:\n%s", file)
	}
}

// Saving the same value twice must answer saved:false and not touch the file.
func TestSaveSettingsUnchangedIsNoOp(t *testing.T) {
	h := newSettingsHarness(t, nil)
	_, readBody := h.get(t)
	var read SettingsResponse
	_ = json.Unmarshal(readBody, &read)
	// tick_interval is already "15s" in the fixture.
	body := fmt.Sprintf(`{"values":{"tick_interval":"15s"},"hash":%q}`, read.Hash)
	resp, out := h.put(t, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode, out)
	}
	var saved SaveSettingsResponse
	_ = json.Unmarshal(out, &saved)
	if saved.Saved {
		t.Error("saved = true, want false when nothing changed")
	}
	if saved.RestartRequired {
		t.Error("restart_required = true, want false when nothing changed")
	}
}

// An unwritable key is a 400 naming it, and the file is untouched.
func TestSaveSettingsRejectsReadOnlyKey(t *testing.T) {
	h := newSettingsHarness(t, nil)
	_, readBody := h.get(t)
	var read SettingsResponse
	_ = json.Unmarshal(readBody, &read)
	body := fmt.Sprintf(`{"values":{"token":"hijacked"},"hash":%q}`, read.Hash)
	resp, out := h.put(t, body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", resp.StatusCode, out)
	}
	if !strings.Contains(string(out), "token") {
		t.Errorf("the refusal does not name the key: %s", out)
	}
	file, _ := os.ReadFile(h.path)
	if !bytes.Equal(file, h.orig) {
		t.Fatal("the file changed despite the 400")
	}
}

// A stale hash is a 409 and the file is untouched.
func TestSaveSettingsStaleHashIsConflict(t *testing.T) {
	h := newSettingsHarness(t, nil)
	stale := fmt.Sprintf("%x", bytes.Repeat([]byte{0x00}, 32))
	body := fmt.Sprintf(`{"values":{"act":"true"},"hash":%q}`, stale)
	resp, out := h.put(t, body)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", resp.StatusCode, out)
	}
	file, _ := os.ReadFile(h.path)
	if !bytes.Equal(file, h.orig) {
		t.Fatal("the file changed despite the 409")
	}
}

// A value the loader refuses is a 422 and the file is untouched.
func TestSaveSettingsInvalidValueIsUnprocessable(t *testing.T) {
	h := newSettingsHarness(t, nil)
	_, readBody := h.get(t)
	var read SettingsResponse
	_ = json.Unmarshal(readBody, &read)
	body := fmt.Sprintf(`{"values":{"tick_interval":"nope"},"hash":%q}`, read.Hash)
	resp, out := h.put(t, body)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", resp.StatusCode, out)
	}
	file, _ := os.ReadFile(h.path)
	if !bytes.Equal(file, h.orig) {
		t.Fatal("the file changed despite the 422")
	}
}

// The routes sit behind the existing auth.
func TestSettingsRequiresAuth(t *testing.T) {
	h := newSettingsHarness(t, nil)
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		req := httptest.NewRequest(method, "/api/config", strings.NewReader(`{"values":{"act":"true"}}`))
		req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		req.Header.Set(idempotencyKeyHeader, "auth-"+method)
		resp, err := h.app.Test(req)
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s status = %d, want 401", method, resp.StatusCode)
		}
	}
}

// A malformed body and an empty values map are both 400s that name the problem.
func TestSaveSettingsRejectsMalformedBodies(t *testing.T) {
	h := newSettingsHarness(t, nil)
	for _, body := range []string{`not json`, `{"values":{}}`, `{"values":{"act":"true"},"hash":"zzzz"}`} {
		resp, out := h.put(t, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("body %q status = %d, want 400: %s", body, resp.StatusCode, out)
		}
	}
	file, _ := os.ReadFile(h.path)
	if !bytes.Equal(file, h.orig) {
		t.Fatal("the file changed despite the 400s")
	}
}

var _ = filepath.Join
