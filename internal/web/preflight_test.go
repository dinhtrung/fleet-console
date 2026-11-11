package web

import (
	"context"
	"encoding/json"
	"errors"
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
	forge    *forgeRecorder
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
	forgeRec := &forgeRecorder{}
	app, err := New(Config{Token: testToken, Store: st, ConfigPath: confPath, SetupExec: rec.run, PreflightForge: forgeRec.run})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	return &preflightHarness{app: app, confPath: confPath, home: home, exec: rec, forge: forgeRec}
}

// forgeRecorder stands in for the forge CLI, so the forge_write check never
// shells out from a test. It answers the validation refusals a real gh/glab
// prints for the two probes, records every call, and a test can replace its
// answer through reply.
type forgeRecorder struct {
	calls [][]string
	reply func(ctx context.Context, binary string, args ...string) (string, string, error)
}

// The bodies gh prints for the two deliberately invalid writes. Both exit
// non-zero: the write was refused, and stdout carries the forge's own JSON.
const (
	probePRBody    = `{"message":"Validation Failed","errors":[{"resource":"PullRequest","field":"head","code":"invalid"}],"status":"422"}`
	probeNoteBody  = `{"message":"Validation Failed","errors":[{"resource":"IssueComment","code":"unprocessable","field":"data","message":"Body cannot be blank"}],"status":"422"}`
	probe422Stderr = "gh: HTTP 422: Validation Failed (https://api.github.com/)"

	probeScopeHeaders = "HTTP/2 200\nx-oauth-scopes: repo, workflow\n\n"
)

func (r *forgeRecorder) run(ctx context.Context, binary string, args ...string) (string, string, error) {
	argv := append([]string{binary}, args...)
	r.calls = append(r.calls, argv)
	if r.reply != nil {
		return r.reply(ctx, binary, args...)
	}
	joined := strings.Join(argv, " ")
	switch {
	case strings.Contains(joined, "-i user"):
		return probeScopeHeaders, "", nil
	case strings.Contains(joined, "/comments"):
		return probeNoteBody, probe422Stderr, errors.New("exit status 1")
	case strings.Contains(joined, "/pulls"), strings.Contains(joined, "/merge_requests"):
		return probePRBody, probe422Stderr, errors.New("exit status 1")
	}
	return "", "", errors.New("forgeRecorder: unexpected call " + joined)
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

// TestPreflightProjectHasFiveChecks: the report shape design.md §1 promises —
// exactly five checks in order, each with a state, a detail and a fix.
func TestPreflightProjectHasFiveChecks(t *testing.T) {
	h := newPreflightHarness(t, preflightConfig)
	resp := h.get(t, "/api/projects/app/preflight")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body PreflightResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := []string{"git_url", "forge_write", "base_dir", "rig", "runtime"}
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

// TestRuntimesReturnsNoCredential is the leak assertion the task names, in the form that
// actually holds: the shape is closed, the hint is one of four known commands, and no
// whitespace-free run of 20+ characters appears outside a fixed vocabulary.
//
// The naive form — "nothing longer than the longest login hint" — is wrong twice over: the
// hint itself is a legal field, so a threshold taken from it rises with an injected token;
// and LoginDoc is prose that is legitimately longer than any hint.
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
	// opaqueRun is a CONSTANT, not a measurement of the response. A threshold derived from
	// the payload cannot see a payload shorter than its own longest legitimate string: an
	// injected token raises the bar it has to clear, and the check passes. Measured — with
	// the threshold taken from `login_hint`, injecting a 39-char bot token into that same
	// field made the limit 50 and the token vanished.
	//
	// 20 is chosen against the longest legitimate whitespace-free run the API can carry:
	// `~/.pi/agent/auth.json` is 21 characters, so it is listed in the vocabulary below and
	// anything else this long is a suspect. A real bot token and a real API key are both
	// well past it.
	const opaqueRun = 20

	// The hints are a closed set defined in internal/preflight, so assert them exactly: a
	// command is the only thing this field may hold.
	knownHints := map[string]bool{
		"pi auth": true, "omp login": true, "codex login": true, "claude auth": true,
	}
	for _, runtime := range body.Runtimes {
		if runtime.ID == "" || runtime.LoginHint == "" || runtime.LoginDoc == "" {
			t.Fatalf("%+v: id, login_hint and login_doc are required", runtime)
		}
		if !knownHints[runtime.LoginHint] {
			t.Fatalf("login_hint = %q is not one of the four known commands: a value here is a leak", runtime.LoginHint)
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
	// Everything present in a CLEAN response must be vocabulary or short. Proving the
	// vocabulary is complete this way means a field added later cannot smuggle a long run
	// past the check by being absent from the list.
	var opaque []string
	var walk func(any)
	walk = func(value any) {
		switch typed := value.(type) {
		case string:
			for _, word := range strings.Fields(typed) {
				clean := strings.Trim(word, "`.,;:()")
				if len(clean) >= opaqueRun && !fixed[clean] {
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
		t.Fatalf("response carries %d opaque string(s) of %d+ chars that are not known vocabulary: %v",
			len(opaque), opaqueRun, opaque)
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
