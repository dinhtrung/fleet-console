package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/config"
	"github.com/dinhtrung/fleet-console/internal/configfile"
	"github.com/dinhtrung/fleet-console/internal/registry"
	"github.com/dinhtrung/fleet-console/internal/store"
	"github.com/gofiber/fiber/v3"
)

const projectsConfig = `# fleet-console configuration
# Keep this header: it is the operator's.
listen: 127.0.0.1:8787
database: fleet-console.db

projects:
  - key: existing-1
    name: Existing one
    forge: github
    repo: org/one
    rigs: [rig-a]
    concurrency_cap: 1
  - key: existing-2
    name: Existing two
    forge: github
    repo: org/two
    rigs: [rig-b]
    concurrency_cap: 2
`

type projectsHarness struct {
	app  *fiber.App
	path string
	st   *store.Store
	orig []byte
}

func newProjectsHarness(t *testing.T, write func(string, configfile.Project, *[32]byte) (configfile.Outcome, error)) *projectsHarness {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(projectsConfig), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	st, err := store.Open(context.Background(), filepath.Join(dir, "fleet.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	app, err := New(Config{Token: testToken, Store: st, ConfigPath: path, WriteProject: write})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	return &projectsHarness{app: app, path: path, st: st, orig: original}
}

func (h *projectsHarness) bytes(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(h.path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	return data
}

func (h *projectsHarness) post(t *testing.T, body string) (*http.Response, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/projects", bytes.NewBufferString(body))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer "+testToken)
	req.Header.Set(idempotencyKeyHeader, "create-"+t.Name())
	resp, err := h.app.Test(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, out
}

const validProject = `{"key":"new-proj","name":"New project","forge":"github","repo":"org/new",
 "default_branch":"main","rigs":["rig-a-2"],"concurrency_cap":2,
 "labels":{"status::ongoing":"in flight"},"paths":{"main":"./checkouts/new","worktrees":"./wt"}}`

// TestCreateProjectRequiresAuth: the routes sit behind the existing auth, so the
// request that writes nothing is the one with no token.
func TestCreateProjectRequiresAuth(t *testing.T) {
	h := newProjectsHarness(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(validProject))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	req.Header.Set(idempotencyKeyHeader, "create-unauthenticated")
	resp, err := h.app.Test(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if !bytes.Equal(h.bytes(t), h.orig) {
		t.Fatal("an unauthenticated request changed the file")
	}
}

func TestCreateProjectStatuses(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		want     int
		fileSame bool
	}{
		{name: "created", body: validProject, want: http.StatusCreated, fileSame: false},
		{name: "duplicate key", body: `{"key":"existing-1","name":"Dup","forge":"github","repo":"o/d","rigs":["rig-z"]}`, want: http.StatusConflict, fileSame: true},
		{name: "malformed json", body: `{"key":`, want: http.StatusBadRequest, fileSame: true},
		{
			name:     "rig collision with another project",
			body:     `{"key":"new-proj","name":"New","forge":"github","repo":"o/n","rigs":["rig-a"]}`,
			want:     http.StatusUnprocessableEntity,
			fileSame: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newProjectsHarness(t, nil)
			resp, body := h.post(t, tc.body)
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d (%s)", resp.StatusCode, tc.want, body)
			}
			if tc.fileSame && !bytes.Equal(h.bytes(t), h.orig) {
				t.Fatalf("a refused create changed the file:\n%s", h.bytes(t))
			}
			if !tc.fileSame && bytes.Equal(h.bytes(t), h.orig) {
				t.Fatal("a created project did not reach the file")
			}
		})
	}
}

// TestCreateProject422NamesFields: design §5 wants one message per field, which
// is what the board renders on the input that failed.
func TestCreateProject422NamesFields(t *testing.T) {
	h := newProjectsHarness(t, nil)
	resp, body := h.post(t, `{"key":"MNP","forge":"bitbucket","repo":"o/r","rigs":["rig-z"]}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", resp.StatusCode, body)
	}
	var answer ErrorResponse
	if err := json.Unmarshal(body, &answer); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if answer.Errors["key"] == "" || answer.Errors["forge"] == "" {
		t.Fatalf("errors = %v, want key and forge named", answer.Errors)
	}
	if _, ok := answer.Errors[""]; ok {
		t.Fatalf("errors = %v, want no empty field name", answer.Errors)
	}
	if !bytes.Equal(h.bytes(t), h.orig) {
		t.Fatal("a refused create changed the file")
	}
}

// TestCreateProjectStoresAndPreserves: the accepted definition lands in the file
// with the other two untouched, and in the store the mirror reads.
func TestCreateProjectStoresAndPreserves(t *testing.T) {
	h := newProjectsHarness(t, nil)
	resp, body := h.post(t, validProject)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", resp.StatusCode, body)
	}

	written := string(h.bytes(t))
	for _, want := range []string{
		"# fleet-console configuration", "# Keep this header: it is the operator's.",
		"key: existing-1", "key: existing-2", "key: new-proj",
	} {
		if !strings.Contains(written, want) {
			t.Fatalf("file lost %q:\n%s", want, written)
		}
	}

	var answer CreateProjectResponse
	if err := json.Unmarshal(body, &answer); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if answer.Project.Key != "new-proj" || answer.Project.ConcurrencyCap != 2 {
		t.Fatalf("project = %+v, want new-proj with cap 2", answer.Project)
	}
	if !filepath.IsAbs(answer.Project.Paths.Main) || !strings.HasSuffix(answer.Project.Paths.Main, "checkouts/new") {
		t.Fatalf("paths.main = %q, want it resolved against the config dir", answer.Project.Paths.Main)
	}

	row, err := h.st.ProjectByKey(context.Background(), "new-proj")
	if err != nil || row == nil {
		t.Fatalf("store row = %+v (%v), want the created project", row, err)
	}
	if row.LabelsJSON != `{"status::ongoing":"in flight"}` {
		t.Fatalf("labels = %s, want the posted labels", row.LabelsJSON)
	}
	path, err := h.st.RepoPath(context.Background(), row.ID)
	if err != nil || path != answer.Project.Paths.Main {
		t.Fatalf("repo path = %q (%v), want %q", path, err, answer.Project.Paths.Main)
	}
	if len(row.Rigs) != 1 || row.Rigs[0] != "rig-a-2" || row.ConcurrencyCap != 2 {
		t.Fatalf("row = %+v, want rig-a-2 and cap 2", row)
	}

	// The next --apply-config finds nothing to change.
	eff, err := config.Load(config.Options{FlagPath: h.path})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	entry, _ := findProject(eff.Projects, "new-proj")
	result, err := registry.ApplyOne(context.Background(), h.st, entry, false)
	if err != nil || result != store.UpsertUnchanged {
		t.Fatalf("apply after create = %q (%v), want unchanged", result, err)
	}
}

// TestCreateProjectRefusesStaleRead: a hand edit between the request's read and
// its write answers 409 and the operator's bytes survive.
func TestCreateProjectRefusesStaleRead(t *testing.T) {
	var h *projectsHarness
	handEdited := []byte(projectsConfig + "# hand edit during the submit\n")
	write := func(path string, in configfile.Project, expected *[32]byte) (configfile.Outcome, error) {
		if err := os.WriteFile(path, handEdited, 0o644); err != nil {
			t.Fatalf("simulate hand edit: %v", err)
		}
		return configfile.UpsertProject(path, in, expected)
	}
	h = newProjectsHarness(t, write)
	resp, body := h.post(t, validProject)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", resp.StatusCode, body)
	}
	if got := h.bytes(t); !bytes.Equal(got, handEdited) {
		t.Fatalf("the write clobbered the hand edit:\n%s", got)
	}
	if row, _ := h.st.ProjectByKey(context.Background(), "new-proj"); row != nil {
		t.Fatalf("store row = %+v, want none after a refused write", row)
	}
}

// TestOneRuleSetForLoaderAndHandler: the same bad project produces the same
// message from config.Load and from POST /api/projects, so a project the board
// accepts is a project the next boot accepts.
func TestOneRuleSetForLoaderAndHandler(t *testing.T) {
	cases := []struct {
		name  string
		field string
		body  string
		yaml  string
	}{
		{
			name:  "uppercase key",
			field: "key",
			body:  `{"key":"MNP","name":"M","forge":"github","repo":"o/r","rigs":["rig-z"]}`,
			yaml:  "  - {key: MNP, name: M, forge: github, repo: o/r, rigs: [rig-z]}\n",
		},
		{
			name:  "unknown forge",
			field: "forge",
			body:  `{"key":"ok","name":"M","forge":"bitbucket","repo":"o/r","rigs":["rig-z"]}`,
			yaml:  "  - {key: ok, name: M, forge: bitbucket, repo: o/r, rigs: [rig-z]}\n",
		},
		{
			name:  "missing repo",
			field: "repo",
			body:  `{"key":"ok","name":"M","forge":"github","rigs":["rig-z"]}`,
			yaml:  "  - {key: ok, name: M, forge: github, rigs: [rig-z]}\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			loaderPath := filepath.Join(dir, "loader.yaml")
			if err := os.WriteFile(loaderPath, []byte("projects:\n"+tc.yaml), 0o644); err != nil {
				t.Fatalf("write loader config: %v", err)
			}
			_, loaderErr := config.Load(config.Options{FlagPath: loaderPath, Home: dir, Cwd: dir})
			if loaderErr == nil {
				t.Fatal("loader accepted the bad project")
			}

			h := newProjectsHarness(t, nil)
			resp, body := h.post(t, tc.body)
			if resp.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (%s)", resp.StatusCode, body)
			}
			var answer ErrorResponse
			if err := json.Unmarshal(body, &answer); err != nil {
				t.Fatalf("decode: %v", err)
			}
			handlerMessage := answer.Errors[tc.field]
			if handlerMessage == "" {
				t.Fatalf("errors = %v, want a %s message", answer.Errors, tc.field)
			}
			if want := loaderMessage(loaderErr.Error()); handlerMessage != want {
				t.Fatalf("handler says %q, loader says %q", handlerMessage, want)
			}
		})
	}
}

// loaderMessage strips the loader's `config: project "key": ` wrapper, which is
// the only difference the two entry points may have.
func loaderMessage(message string) string {
	if index := strings.Index(message, `": `); index >= 0 {
		return message[index+3:]
	}
	return message
}
