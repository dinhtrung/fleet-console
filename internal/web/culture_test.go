package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/store"
	"github.com/gofiber/fiber/v3"
)

// cultureHarness is a real store, a real config file and a real culture file in
// a temp dirs tree, so a handler test exercises the whole path rather than a
// mock of it.
type cultureHarness struct {
	app      *fiber.App
	st       *store.Store
	specs    string
	culture  string // the resolved CULTURE.md for project "app"
	rigDir   string
	confPath string
	calls    int
}

func newCultureHarness(t *testing.T, seed string) *cultureHarness {
	t.Helper()
	dir := t.TempDir()
	specs := filepath.Join(dir, "specs")
	rigDir := filepath.Join(specs, "rigs", "rig-a")
	if err := os.MkdirAll(rigDir, 0o755); err != nil {
		t.Fatalf("mkdir rig dir: %v", err)
	}
	// Resolve only treats a rig directory as a rig when it carries rig.yaml.
	if err := os.WriteFile(filepath.Join(rigDir, "rig.yaml"), []byte("name: rig-a\n"), 0o644); err != nil {
		t.Fatalf("rig.yaml: %v", err)
	}
	culturePath := filepath.Join(rigDir, "CULTURE.md")
	if seed != "" {
		if err := os.WriteFile(culturePath, []byte(seed), 0o644); err != nil {
			t.Fatalf("seed culture: %v", err)
		}
	}
	confPath := filepath.Join(dir, "config.yaml")
	conf := "listen: 127.0.0.1:0\nprojects:\n  - key: app\n    name: App\n    forge: github\n" +
		"    repo: o/r\n    rigs: [rig-a]\n    concurrency_cap: 1\n"
	if err := os.WriteFile(confPath, []byte(conf), 0o644); err != nil {
		t.Fatalf("config: %v", err)
	}

	st, err := store.Open(context.Background(), filepath.Join(dir, "fleet.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if _, err := st.CreateProject(context.Background(), store.ProjectInput{
		Key: "app", Name: "App", ForgeKind: "github", ForgeProject: "o/r",
		Rig: "rig-a", Rigs: []string{"rig-a"}, ConcurrencyCap: 1, Enabled: true,
	}); err != nil {
		t.Fatalf("project row: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	t.Setenv("OPENRIG_SPECS_DIR", specs)
	app, err := New(Config{Token: testToken, Store: st, ConfigPath: confPath})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	return &cultureHarness{app: app, st: st, specs: specs, culture: culturePath, rigDir: rigDir, confPath: confPath}
}

func (h *cultureHarness) do(t *testing.T, method, path, body string) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer "+testToken)
	req.Header.Set(idempotencyKeyHeader, fmt.Sprintf("culture-%s-%d", t.Name(), h.calls))
	h.calls++
	if body != "" {
		req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	}
	resp, err := h.app.Test(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, out
}

func decode[T any](t *testing.T, body []byte) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return out
}

func shaOfFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestCultureRead(t *testing.T) {
	h := newCultureHarness(t, "# rules\n\n- be plain\n")
	resp, body := h.do(t, http.MethodGet, "/api/projects/app/culture", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	got := decode[CultureResponse](t, body)
	if !got.Exists || !got.Writable {
		t.Fatalf("body = %+v, want exists and writable", got)
	}
	if got.Path != h.culture || got.Source != "derived" || got.Rig != "rig-a" {
		t.Fatalf("body = %+v, want the derived path and rig", got)
	}
	if got.Content != "# rules\n\n- be plain\n" || got.Bytes != 20 {
		t.Fatalf("content = %q bytes=%d", got.Content, got.Bytes)
	}
	if got.SHA256 != shaOfFile(t, h.culture) || len(got.SHA256) != 64 {
		t.Fatalf("sha = %q, want the file's sha256", got.SHA256)
	}
	if got.MTime == "" {
		t.Fatal("mtime is empty")
	}
}

// TestCultureReadMissingIs200: exists:false is an answer, not a 404 — the screen
// needs the path to explain itself.
func TestCultureReadMissingIs200(t *testing.T) {
	h := newCultureHarness(t, "")
	resp, body := h.do(t, http.MethodGet, "/api/projects/app/culture", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	got := decode[CultureResponse](t, body)
	if got.Exists || got.Content != "" {
		t.Fatalf("body = %+v, want exists=false with no content", got)
	}
	if got.Path != h.culture {
		t.Fatalf("path = %q, want the expected path carried", got.Path)
	}
}

func TestCultureUnknownProjectIs404(t *testing.T) {
	h := newCultureHarness(t, "# rules\n")
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		resp, body := h.do(t, method, "/api/projects/nope/culture", `{"content":"x","base_sha":null}`)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404 (%s)", method, resp.StatusCode, body)
		}
	}
}

func TestCultureWriteCreatesThenUpdates(t *testing.T) {
	h := newCultureHarness(t, "")
	resp, body := h.do(t, http.MethodPut, "/api/projects/app/culture", `{"content":"# new\n","base_sha":null}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	created := decode[CultureWriteResponse](t, body)
	if created.Bytes != 6 || created.Backup != "" || created.SHA256 != shaOfFile(t, h.culture) {
		t.Fatalf("created = %+v, want 6 bytes, no backup, the file's sha", created)
	}
	if data, _ := os.ReadFile(h.culture); string(data) != "# new\n" {
		t.Fatalf("file = %q", data)
	}

	// Update with the current sha: a backup appears.
	resp, body = h.do(t, http.MethodPut, "/api/projects/app/culture",
		`{"content":"# newer\n","base_sha":"`+created.SHA256+`"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	updated := decode[CultureWriteResponse](t, body)
	if updated.Backup == "" {
		t.Fatal("no backup on an update")
	}
	backup, err := os.ReadFile(updated.Backup)
	if err != nil || string(backup) != "# new\n" {
		t.Fatalf("backup = %q (%v), want the previous bytes", backup, err)
	}
}

// TestCultureWriteStaleNamesCurrentSHA is a required row: the 409 body carries
// the current sha and mtime so the screen can offer "load latest".
func TestCultureWriteStaleNamesCurrentSHA(t *testing.T) {
	h := newCultureHarness(t, "# rules\n")
	current := shaOfFile(t, h.culture)
	stale := strings.Repeat("0", 64)
	resp, body := h.do(t, http.MethodPut, "/api/projects/app/culture",
		`{"content":"# changed\n","base_sha":"`+stale+`"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", resp.StatusCode, body)
	}
	got := decode[struct {
		Error string `json:"error"`
		SHA   string `json:"sha"`
		MTime string `json:"mtime"`
	}](t, body)
	if got.SHA != current || got.MTime == "" {
		t.Fatalf("body = %+v, want the current sha %s and an mtime", got, current)
	}
	if data, _ := os.ReadFile(h.culture); string(data) != "# rules\n" {
		t.Fatalf("file = %q, want it untouched", data)
	}
}

func TestCultureWriteTooLargeIs413(t *testing.T) {
	h := newCultureHarness(t, "")
	big := strings.Repeat("a", 256<<10+1)
	resp, body := h.do(t, http.MethodPut, "/api/projects/app/culture",
		`{"content":`+quote(big)+`,"base_sha":null}`)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (%s)", resp.StatusCode, body)
	}
	if _, err := os.Stat(h.culture); !os.IsNotExist(err) {
		t.Fatal("a refused write created the file")
	}
}

func TestCultureWriteNotTextIs422(t *testing.T) {
	h := newCultureHarness(t, "")
	// Only the NUL case is reachable here: encoding/json replaces invalid UTF-8
	// with U+FFFD on the way in, so a JSON body cannot carry it. The rule is
	// still enforced in culture.Write and covered by its own tests; a handler
	// test cannot reach it, which is a property of JSON, not of the handler.
	for name, content := range map[string]string{
		"NUL": "text\x00here",
	} {
		t.Run(name, func(t *testing.T) {
			resp, body := h.do(t, http.MethodPut, "/api/projects/app/culture",
				`{"content":`+quote(content)+`,"base_sha":null}`)
			if resp.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (%s)", resp.StatusCode, body)
			}
		})
	}
}

// TestCultureWriteReadOnlyIs403 is a required row: 0444 is not writable, and the
// body names the path and mode.
func TestCultureWriteReadOnlyIs403(t *testing.T) {
	h := newCultureHarness(t, "# locked\n")
	if err := os.Chmod(h.culture, 0o444); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(h.culture, 0o644) })
	current := shaOfFile(t, h.culture)

	resp, body := h.do(t, http.MethodPut, "/api/projects/app/culture",
		`{"content":"# unlocked\n","base_sha":"`+current+`"}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", resp.StatusCode, body)
	}
	got := decode[struct {
		Error string `json:"error"`
		Path  string `json:"path"`
		Mode  string `json:"mode"`
	}](t, body)
	if got.Path != h.culture || got.Mode != "0444" {
		t.Fatalf("body = %+v, want the path and 0444", got)
	}
	if data, _ := os.ReadFile(h.culture); string(data) != "# locked\n" {
		t.Fatalf("file = %q, want it untouched", data)
	}
}

func TestCultureWriteSymlinkIs403(t *testing.T) {
	h := newCultureHarness(t, "")
	outside := filepath.Join(t.TempDir(), "ELSEWHERE.md")
	if err := os.WriteFile(outside, []byte("someone else's\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.Symlink(outside, h.culture); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	resp, body := h.do(t, http.MethodPut, "/api/projects/app/culture", `{"content":"x\n","base_sha":null}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", resp.StatusCode, body)
	}
	if data, _ := os.ReadFile(outside); string(data) != "someone else's\n" {
		t.Fatalf("outside file = %q, want it untouched", data)
	}
}

// TestCultureWriteRecordsEvent is the end-to-end promise: a save leaves a .bak
// and one culture.edited row naming both shas.
func TestCultureWriteRecordsEvent(t *testing.T) {
	h := newCultureHarness(t, "# before\n")
	oldSHA := shaOfFile(t, h.culture)
	resp, body := h.do(t, http.MethodPut, "/api/projects/app/culture",
		`{"content":"# after\n","base_sha":"`+oldSHA+`"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	written := decode[CultureWriteResponse](t, body)
	if written.Backup == "" {
		t.Fatal("no .bak beside the file")
	}
	if _, err := os.Stat(written.Backup); err != nil {
		t.Fatalf("backup missing: %v", err)
	}

	events, err := h.st.RecentEvents(context.Background(), 20)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	var found bool
	for _, event := range events {
		if event.Kind != "culture.edited" {
			continue
		}
		found = true
		for _, want := range []string{"old_sha=" + oldSHA, "new_sha=" + written.SHA256, "bytes=8", "path=" + h.culture} {
			if !strings.Contains(event.Detail, want) {
				t.Fatalf("event detail = %q, want it to contain %q", event.Detail, want)
			}
		}
	}
	if !found {
		t.Fatalf("no culture.edited event among %d", len(events))
	}
}

// TestCultureBodyCannotSteerThePath: the request has exactly two fields, and a
// body naming a different file is ignored.
func TestCultureBodyCannotSteerThePath(t *testing.T) {
	h := newCultureHarness(t, "")
	elsewhere := filepath.Join(t.TempDir(), "ELSEWHERE.md")
	body := `{"content":"# hi\n","base_sha":null,"path":"` + elsewhere + `","file":"` + elsewhere + `"}`

	resp, out := h.do(t, http.MethodPut, "/api/projects/app/culture", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, out)
	}
	if _, err := os.Stat(elsewhere); !os.IsNotExist(err) {
		t.Fatalf("the write followed a body field to %s", elsewhere)
	}
	if data, err := os.ReadFile(h.culture); err != nil || string(data) != "# hi\n" {
		t.Fatalf("culture = %q (%v), want the configured path written", data, err)
	}
}

// TestCultureRequiresAuth: no new auth path.
func TestCultureRequiresAuth(t *testing.T) {
	h := newCultureHarness(t, "# rules\n")
	req := httptest.NewRequest(http.MethodGet, "/api/projects/app/culture", nil)
	resp, err := h.app.Test(req)
	if err != nil {
		t.Fatalf("req: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func quote(s string) string {
	encoded, _ := json.Marshal(s)
	return string(encoded)
}
