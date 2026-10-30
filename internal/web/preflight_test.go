package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/dinhtrung/fleet-console/internal/store"
)

// preflightHarness is a real config file, a real store and a real app, so the
// handlers are exercised through the same path the binary uses.
type preflightHarness struct {
	app      *fiber.App
	confPath string
	home     string
	exec     *execRecorder
}

// execRecorder stands in for the setup chain's external command, so no test
// clones or launches anything.
type execRecorder struct {
	calls []string
	out   string
	err   error
}

func (e *execRecorder) run(_ context.Context, name string, args ...string) (string, error) {
	e.calls = append(e.calls, name+" "+strings.Join(args, " "))
	if e.err != nil {
		return e.out, e.err
	}
	return e.out, nil
}

func newPreflightHarness(t *testing.T, configBody string) *preflightHarness {
	t.Helper()
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	confPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(confPath, []byte(configBody), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), filepath.Join(dir, "fleet.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	rec := &execRecorder{out: "cloned"}
	app, err := New(Config{Token: testToken, Store: st, ConfigPath: confPath, SetupExec: rec.run})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	return &preflightHarness{app: app, confPath: confPath, home: home, exec: rec}
}

const preflightConfig = `listen: 127.0.0.1:0
projects:
  - key: app
    name: App
    forge: github
    repo: o/r
    rigs: [rig-a]
    concurrency_cap: 1
    runtime: omp
    paths:
      main: /nonexistent/app
      worktrees: /tmp/wt
`

func (h *preflightHarness) get(t *testing.T, path string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := h.app.Test(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

func (h *preflightHarness) post(t *testing.T, path, body string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "test-"+strings.ReplaceAll(path, "/", "-"))
	resp, err := h.app.Test(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

// TestPreflightProjectHasFourChecks: the report shape design.md §1 promises —
// exactly four checks in order, each with a state, a detail and a fix.
func TestPreflightProjectHasFourChecks(t *testing.T) {
	h := newPreflightHarness(t, preflightConfig)
	resp := h.get(t, "/api/projects/app/preflight")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body PreflightResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := []string{"git_url", "base_dir", "rig", "runtime"}
	if len(body.Checks) != len(want) {
		t.Fatalf("checks = %d, want %d", len(body.Checks), len(want))
	}
	for i, id := range want {
		if body.Checks[i].ID != id {
			t.Fatalf("check %d = %q, want %q", i, body.Checks[i].ID, id)
		}
		if body.Checks[i].State == "" || body.Checks[i].Detail == "" {
			t.Fatalf("%s: state=%q detail=%q, want both", id, body.Checks[i].State, body.Checks[i].Detail)
		}
	}
	if body.Project != "app" {
		t.Fatalf("project = %q, want app", body.Project)
	}
	if body.CheckedAt.IsZero() {
		t.Fatal("checked_at is zero")
	}
}

// TestPreflightUnknownProject: a key that is not registered is a 404, not an
// empty report that reads as "nothing wrong".
func TestPreflightUnknownProject(t *testing.T) {
	h := newPreflightHarness(t, preflightConfig)
	if resp := h.get(t, "/api/projects/nope/preflight"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// TestPreflightBaseDirFailCarriesTheDiagnosis: a missing checkout is fail with
// the mkdir_base step, so the client can offer "Set up this step".
func TestPreflightBaseDirFailCarriesTheDiagnosis(t *testing.T) {
	h := newPreflightHarness(t, preflightConfig)
	resp := h.get(t, "/api/projects/app/preflight")
	var body PreflightResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	var base CheckResponse
	for _, check := range body.Checks {
		if check.ID == "base_dir" {
			base = check
		}
	}
	if base.State != "fail" {
		t.Fatalf("base_dir state = %q, want fail", base.State)
	}
	if base.Fix.Step != "mkdir_base" {
		t.Fatalf("base_dir step = %q, want mkdir_base", base.Fix.Step)
	}
	if !strings.Contains(base.Detail, "does not exist") {
		t.Fatalf("detail = %q, want the diagnosis", base.Detail)
	}
}

// TestRuntimesReturnsNoCredential is the leak assertion the task names: nothing
// in the body may be longer than the longest login hint, because a token would
// be.
func TestRuntimesReturnsNoCredential(t *testing.T) {
	h := newPreflightHarness(t, preflightConfig)
	resp := h.get(t, "/api/runtimes")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var body RuntimesResponse
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Runtimes) != 4 {
		t.Fatalf("runtimes = %d, want 4", len(body.Runtimes))
	}
	longest := 0
	for _, runtime := range body.Runtimes {
		if runtime.ID == "" || runtime.LoginHint == "" || runtime.LoginDoc == "" {
			t.Fatalf("%+v: id, login_hint and login_doc are required", runtime)
		}
		if len(runtime.LoginHint) > longest {
			longest = len(runtime.LoginHint)
		}
	}
	// The assertion the task names — "no value longer than a login hint" — read
	// as what it is FOR: no opaque secret. LoginDoc is prose and legitimately
	// long, so length alone cannot be the test, and an allowlist built from the
	// response would admit a token that was injected into it.
	//
	// Two checks that cannot be gamed by the payload:
	//
	//  1. structural: the shape is closed. Exactly these five keys per runtime,
	//     and auth_present is a bool, so no field can carry a value.
	//  2. opacity: no whitespace-free run longer than the longest hint, drawn
	//     from a fixed vocabulary the server does not control.
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatal(err)
	}
	list, ok := generic["runtimes"].([]any)
	if !ok {
		t.Fatalf("runtimes is not an array: %s", raw)
	}
	allowed := map[string]bool{"id": true, "supported": true, "auth_present": true, "login_hint": true, "login_doc": true}
	for _, entry := range list {
		object, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("runtime entry is not an object: %v", entry)
		}
		for key, value := range object {
			if !allowed[key] {
				t.Fatalf("unexpected field %q: a credential would need a field to live in", key)
			}
			switch key {
			case "supported", "auth_present":
				if _, ok := value.(bool); !ok {
					t.Fatalf("%s = %T, want bool: a credential value here would be a leak", key, value)
				}
			case "id", "login_hint", "login_doc":
				if _, ok := value.(string); !ok {
					t.Fatalf("%s = %T, want string", key, value)
				}
			}
		}
	}

	// Opacity, with a vocabulary fixed in this test rather than read from the
	// response: the words a legitimately-prose login_doc contains.
	fixed := map[string]bool{}
	for _, word := range strings.Fields(
		"pi omp codex claude login auth ~/.pi/agent/auth.json ~/.omp ~/.codex/auth.json ~/.claude " +
			"keeps provider credentials in prints or checks them terminal counterpart of manages and the in-app " +
			"same store writes removes it under per-profile when is used") {
		fixed[strings.Trim(word, "`.,;:()")] = true
	}
	var opaque []string
	var walk func(any)
	walk = func(value any) {
		switch typed := value.(type) {
		case string:
			for _, word := range strings.Fields(typed) {
				clean := strings.Trim(word, "`.,;:()")
				if len(clean) > longest && !fixed[clean] {
					opaque = append(opaque, clean)
				}
			}
		case []any:
			for _, item := range typed {
				walk(item)
			}
		case map[string]any:
			for _, item := range typed {
				walk(item)
			}
		}
	}
	walk(generic)
	if len(opaque) > 0 {
		t.Fatalf("response carries %d opaque string(s) longer than the longest hint (%d chars): %v",
			len(opaque), longest, opaque)
	}
}

// TestDraftPreflightChecksBeforeTheRowExists: git_url runs against the body,
// rig is unknown with its reason, and no project row is needed first.
func TestDraftPreflightChecksBeforeTheRowExists(t *testing.T) {
	h := newPreflightHarness(t, preflightConfig)
	body := `{"key":"draft","name":"Draft","forge":"github","repo":"o/r","rigs":["rig-a"],` +
		`"runtime":"omp","paths":{"main":"/nonexistent/draft","worktrees":"/tmp/wt"}}`
	resp := h.post(t, "/api/projects/preflight", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var report PreflightResponse
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		t.Fatal(err)
	}
	var git, rigCheck CheckResponse
	for _, check := range report.Checks {
		switch check.ID {
		case "git_url":
			git = check
		case "rig":
			rigCheck = check
		}
	}
	// A real network call is attempted; the repo o/r does not exist on GitHub,
	// so the verdict is fail or unknown, never ok.
	if git.State == "ok" {
		t.Fatalf("git_url = ok for a nonexistent repository: %q", git.Detail)
	}
	if git.Detail == "" {
		t.Fatal("git_url detail is empty")
	}
	if rigCheck.State != "unknown" {
		t.Fatalf("rig = %q, want unknown for a draft", rigCheck.State)
	}
	if !strings.Contains(rigCheck.Detail, "created with the project") {
		t.Fatalf("rig detail = %q, want the draft reason", rigCheck.Detail)
	}
	if rigCheck.Fix.Command == "" {
		t.Fatal("rig unknown carries no answering command")
	}
}

// TestSetupRunsAllowlistedStepsInOrder: the chain runs mkdir_base then clone and
// reports one entry per step; a step outside the list is refused.
func TestSetupRunsAllowlistedStepsInOrder(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "checkout")
	cfg := strings.Replace(preflightConfig, "/nonexistent/app", main, 1)
	h := newPreflightHarness(t, cfg)

	resp := h.post(t, "/api/projects/app/setup", `{"steps":["clone","mkdir_base"]}`)
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode, raw)
	}
	var body SetupResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Steps) != 2 {
		t.Fatalf("steps = %+v, want 2", body.Steps)
	}
	// Canonical order, not the order asked for.
	if body.Steps[0].Step != "mkdir_base" || body.Steps[1].Step != "clone" {
		t.Fatalf("order = %s then %s, want mkdir_base then clone", body.Steps[0].Step, body.Steps[1].Step)
	}
	if body.Steps[0].State != "done" {
		t.Fatalf("mkdir_base = %q, want done", body.Steps[0].State)
	}
	// The clone ran through the injected exec, not a real git.
	if len(h.exec.calls) != 1 || !strings.HasPrefix(h.exec.calls[0], "git clone") {
		t.Fatalf("exec calls = %v, want one git clone", h.exec.calls)
	}
}

// TestSetupRefusesAnUnknownStep: an id outside the four is 422, so a caller
// cannot smuggle a command into the chain.
func TestSetupRefusesAnUnknownStep(t *testing.T) {
	h := newPreflightHarness(t, preflightConfig)
	resp := h.post(t, "/api/projects/app/setup", `{"steps":["rm_rf"]}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	if len(h.exec.calls) != 0 {
		t.Fatalf("exec calls = %v, want none", h.exec.calls)
	}
}
