package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/config"
	"github.com/dinhtrung/fleet-console/internal/configfile"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/mirror"
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
	app   *fiber.App
	path  string
	st    *store.Store
	orig  []byte
	seq   int
	waker *mirror.Waker
}

func newProjectsHarness(t *testing.T, write func(string, configfile.Project, *[32]byte) (configfile.Outcome, error)) *projectsHarness {
	return newProjectsHarnessWithRemove(t, write, nil)
}

func newProjectsHarnessWithRemove(t *testing.T, write func(string, configfile.Project, *[32]byte) (configfile.Outcome, error), remove func(string, string, *[32]byte) (configfile.Outcome, error)) *projectsHarness {
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
	waker := mirror.NewWaker()
	app, err := New(Config{Token: testToken, Store: st, ConfigPath: path, WriteProject: write, UpdateProject: write, RemoveProject: remove, Waker: waker})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	return &projectsHarness{app: app, path: path, st: st, orig: original, waker: waker}
}

func (h *projectsHarness) bytes(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(h.path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	return data
}

func (h *projectsHarness) get(t *testing.T, path string) (*http.Response, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer "+testToken)
	resp, err := h.app.Test(req)
	if err != nil {
		t.Fatalf("get %s: %v", path, err)
	}
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, out
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

// do is the authenticated call for the edit, pause and delete routes. Every
// request carries a fresh Idempotency-Key — a repeated key replays the first
// answer, which would hide the retry the delete's second attempt must make.
func (h *projectsHarness) do(t *testing.T, method, path, body string) (*http.Response, []byte) {
	t.Helper()
	h.seq++
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	}
	req.Header.Set(fiber.HeaderAuthorization, "Bearer "+testToken)
	req.Header.Set(idempotencyKeyHeader, fmt.Sprintf("%s %s #%d %s", method, path, h.seq, t.Name()))
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

// seedRow registers the store row of a file project, the "both" source.
func (h *projectsHarness) seedRow(t *testing.T, key, repo string) int64 {
	t.Helper()
	id, err := h.st.CreateProject(context.Background(), store.ProjectInput{
		Key: key, Name: key, ForgeKind: "github", ForgeProject: repo,
		Rig: "rig-a", ConcurrencyCap: 1, Enabled: true,
	})
	if err != nil {
		t.Fatalf("seed row %s: %v", key, err)
	}
	return id
}

// seedLiveDispatch opens one dispatch on item 48, the state the identity rule
// freezes the project in.
func (h *projectsHarness) seedLiveDispatch(t *testing.T, projectID int64) {
	t.Helper()
	if _, err := h.st.CreateDispatch(context.Background(), store.NewDispatch{
		ProjectID: projectID, ItemNumber: 48, Attempt: 1, Seat: "dev-build-1@fleet-console",
		Branch: "feature/48-live", Worktree: "/tmp/wt-48", BaseSHA: "base",
	}); err != nil {
		t.Fatalf("seed live dispatch: %v", err)
	}
}

func editBody(key string, mutate func(*ProjectRequest)) string {
	req := ProjectRequest{Key: key, Name: "Existing one", Forge: "github", Repo: "org/one",
		Rigs: []string{"rig-a"}, ConcurrencyCap: 1}
	if key == "existing-2" {
		req.Name = "Existing two"
		req.Repo = "org/two"
		req.Rigs = []string{"rig-b"}
		req.ConcurrencyCap = 2
	}
	if mutate != nil {
		mutate(&req)
	}
	encoded, err := json.Marshal(req)
	if err != nil {
		panic(err)
	}
	return string(encoded)
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

// TestCreateProjectIOFailureIs500: a write the server cannot perform — here a
// read-only config directory — is not the client's fault, so it answers 500.
// design §5 gives 409 exactly two meanings (duplicate key, file changed on
// disk); anything else is the server's.
func TestCreateProjectIOFailureIs500(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the directory mode, so the write would succeed")
	}
	h := newProjectsHarness(t, nil)
	dir := filepath.Dir(h.path)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	resp, body := h.post(t, validProject)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 for an I/O refusal (%s)", resp.StatusCode, body)
	}
	var answer ErrorResponse
	if err := json.Unmarshal(body, &answer); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(answer.Error, "permission denied") {
		t.Fatalf("error = %q, want the underlying I/O reason", answer.Error)
	}
	if row, _ := h.st.ProjectByKey(context.Background(), "new-proj"); row != nil {
		t.Fatalf("store row = %+v, want none after a failed write", row)
	}
}

// TestCreateProjectTypedRefusals: the two refusals that stay 409 are classified
// by identity, not by matching the error text — a duplicate key with different
// content included, which the OutcomeUnchanged branch never sees.
func TestCreateProjectTypedRefusals(t *testing.T) {
	cases := []struct {
		name  string
		write func(string, configfile.Project, *[32]byte) (configfile.Outcome, error)
		want  int
		body  string
	}{
		{
			name: "duplicate key with different content",
			write: func(path string, in configfile.Project, expected *[32]byte) (configfile.Outcome, error) {
				return "", fmt.Errorf("%w %q", configfile.ErrDuplicateKey, in.Key)
			},
			want: http.StatusConflict,
			body: `duplicate project key "new-proj"`,
		},
		{
			name: "file changed on disk",
			write: func(string, configfile.Project, *[32]byte) (configfile.Outcome, error) {
				return "", configfile.ErrStaleRead
			},
			want: http.StatusConflict,
			body: "config.yaml changed on disk; reload and retry",
		},
		{
			name: "a store that cannot be written",
			write: func(string, configfile.Project, *[32]byte) (configfile.Outcome, error) {
				return "", errors.New("configfile: rename: input/output error")
			},
			want: http.StatusInternalServerError,
			body: "input/output error",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newProjectsHarness(t, tc.write)
			resp, body := h.post(t, validProject)
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d (%s)", resp.StatusCode, tc.want, body)
			}
			var answer ErrorResponse
			if err := json.Unmarshal(body, &answer); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if !strings.Contains(answer.Error, tc.body) {
				t.Fatalf("error = %q, want it to contain %q", answer.Error, tc.body)
			}
			if !bytes.Equal(h.bytes(t), h.orig) {
				t.Fatal("a refused write changed the file")
			}
		})
	}
}

// TestCreateProjectCarriesCultureFile: the board can create a project that names
// its culture file, and GET reads it back. Without this the field is dropped on
// the way in and apply_rig can never succeed for a board-created project (#110).
func TestCreateProjectCarriesCultureFile(t *testing.T) {
	h := newProjectsHarness(t, nil)
	culture := filepath.Join(filepath.Dir(h.path), "specs", "rigs", "rig-a", "CULTURE.md")
	body := `{"key":"with-culture","name":"With culture","forge":"github","repo":"org/wc",
	  "rigs":["rig-a2"],"concurrency_cap":1,"culture_file":` + quoteJSON(culture) + `}`

	resp, out := h.post(t, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", resp.StatusCode, out)
	}
	var created CreateProjectResponse
	if err := json.Unmarshal(out, &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Project.CultureFile != culture {
		t.Fatalf("created culture_file = %q, want %q", created.Project.CultureFile, culture)
	}

	// The file on disk carries it, and the loader reads it back — the two halves
	// that make it reachable at all.
	written, err := os.ReadFile(h.path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(written), "culture_file: "+culture) {
		t.Fatalf("config does not carry culture_file:\n%s", written)
	}

	// GET /api/projects reports it too, so the board shows what it stored.
	listResp, listBody := h.get(t, "/api/projects")
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d (%s)", listResp.StatusCode, listBody)
	}
	var listed ProjectsListResponse
	if err := json.Unmarshal(listBody, &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	var found bool
	for _, project := range listed.Projects {
		if project.Key == "with-culture" {
			found = true
			if project.CultureFile != culture {
				t.Fatalf("listed culture_file = %q, want %q", project.CultureFile, culture)
			}
		}
	}
	if !found {
		t.Fatal("the created project is not in GET /api/projects")
	}
}

// TestCreateProjectRefusesBadCultureFile: the same rule the loader enforces, so a
// project the board accepts is one the next boot accepts.
func TestCreateProjectRefusesBadCultureFile(t *testing.T) {
	h := newProjectsHarness(t, nil)
	body := `{"key":"bad-culture","name":"Bad","forge":"github","repo":"org/bc",
	  "rigs":["rig-a3"],"concurrency_cap":1,"culture_file":"CULTURE.md"}`
	resp, out := h.post(t, body)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 for a relative culture_file (%s)", resp.StatusCode, out)
	}
	var answer ErrorResponse
	if err := json.Unmarshal(out, &answer); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(answer.Errors["culture_file"], "absolute") {
		t.Fatalf("errors = %v, want a culture_file message", answer.Errors)
	}
}

func quoteJSON(s string) string {
	encoded, _ := json.Marshal(s)
	return string(encoded)
}

// TestProjectEditStatuses: one row per status the edit route answers. The
// refused cases leave the file byte-identical, which is the spec's "nothing
// SHALL be written when the definition is refused".
func TestProjectEditStatuses(t *testing.T) {
	cases := []struct {
		name     string
		key      string
		body     string
		write    func(string, configfile.Project, *[32]byte) (configfile.Outcome, error)
		seed     func(t *testing.T, h *projectsHarness)
		want     int
		wantIn   string
		fileSame bool
	}{
		{
			name: "edited", key: "existing-1",
			body: editBody("existing-1", func(r *ProjectRequest) { r.Name = "Renamed one" }),
			want: http.StatusOK, wantIn: "Renamed one",
		},
		{
			name: "body renames the key", key: "existing-1",
			body: editBody("existing-2", nil),
			want: http.StatusUnprocessableEntity, wantIn: "key is immutable; delete and recreate",
			fileSame: true,
		},
		{
			name: "malformed json", key: "existing-1",
			body: `{"key":`, want: http.StatusBadRequest, fileSame: true,
		},
		{
			name: "unknown key", key: "nope",
			body: editBody("nope", nil),
			want: http.StatusNotFound, wantIn: "not registered",
			fileSame: true,
		},
		{
			name: "unknown forge", key: "existing-1",
			body: editBody("existing-1", func(r *ProjectRequest) { r.Forge = "bitbucket" }),
			want: http.StatusUnprocessableEntity, wantIn: "forge",
			fileSame: true,
		},
		{
			name: "stale read", key: "existing-1",
			body: editBody("existing-1", nil),
			write: func(string, configfile.Project, *[32]byte) (configfile.Outcome, error) {
				return "", configfile.ErrStaleRead
			},
			want: http.StatusConflict, wantIn: "changed on disk; reload and retry",
			fileSame: true,
		},
		{
			name: "a store-only project refuses the edit", key: "db-only",
			body: editBody("db-only", nil),
			seed: func(t *testing.T, h *projectsHarness) { h.seedRow(t, "db-only", "o/db") },
			want: http.StatusConflict, wantIn: "lives only in the database",
			fileSame: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newProjectsHarness(t, tc.write)
			if tc.seed != nil {
				tc.seed(t, h)
			}
			resp, body := h.do(t, http.MethodPut, "/api/projects/"+tc.key, tc.body)
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d (%s)", resp.StatusCode, tc.want, body)
			}
			if tc.wantIn != "" && !strings.Contains(string(body), tc.wantIn) {
				t.Fatalf("body = %s, want it to contain %q", body, tc.wantIn)
			}
			if tc.fileSame && !bytes.Equal(h.bytes(t), h.orig) {
				t.Fatalf("a refused edit changed the file:\n%s", h.bytes(t))
			}
		})
	}
}

// TestProjectEditRefusesIdentityChangeUnderLiveDispatch: the builder's
// worktree is cut from paths.worktrees and its branch is pushed to repo;
// moving either under a live dispatch loses the work at merge time, so the
// answer is 409 naming the dispatch, the item and the branch, and neither
// layer is written — the file's mtime included.
func TestProjectEditRefusesIdentityChangeUnderLiveDispatch(t *testing.T) {
	h := newProjectsHarness(t, nil)
	id := h.seedRow(t, "existing-1", "org/one")
	h.seedLiveDispatch(t, id)
	before, err := os.Stat(h.path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}

	resp, body := h.do(t, http.MethodPut, "/api/projects/existing-1",
		editBody("existing-1", func(r *ProjectRequest) { r.Repo = "org/moved" }))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", resp.StatusCode, body)
	}
	message := string(body)
	for _, want := range []string{"dispatch 1", "item #48", "feature/48-live", "pause the project"} {
		if !strings.Contains(message, want) {
			t.Fatalf("409 body %q does not name %q — the operator's instruction is part of the requirement", message, want)
		}
	}
	after, err := os.Stat(h.path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("a refused identity edit touched the config file")
	}
	if row, _ := h.st.ProjectByKey(context.Background(), "existing-1"); row == nil || row.ForgeProject != "org/one" {
		t.Fatalf("store row = %+v, want repo still org/one", row)
	}
}

// TestProjectEditAllowsCapChangeUnderLiveDispatch: the other half of D3 — the
// cap is what an operator changes mid-flight, so it applies while the
// dispatch is live.
func TestProjectEditAllowsCapChangeUnderLiveDispatch(t *testing.T) {
	h := newProjectsHarness(t, nil)
	id := h.seedRow(t, "existing-1", "org/one")
	h.seedLiveDispatch(t, id)

	resp, body := h.do(t, http.MethodPut, "/api/projects/existing-1",
		editBody("existing-1", func(r *ProjectRequest) { r.ConcurrencyCap = 3 }))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	var answer EditProjectResponse
	if err := json.Unmarshal(body, &answer); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(answer.Changed) != 1 || answer.Changed[0] != "concurrency_cap" {
		t.Fatalf("changed = %v, want [concurrency_cap]", answer.Changed)
	}
	row, err := h.st.ProjectByKey(context.Background(), "existing-1")
	if err != nil || row == nil || row.ConcurrencyCap != 3 {
		t.Fatalf("store row = %+v (%v), want cap 3", row, err)
	}
	if !strings.Contains(string(h.bytes(t)), "concurrency_cap: 3") {
		t.Fatalf("file does not carry the new cap:\n%s", h.bytes(t))
	}
}

// TestProjectEditClearsItemsOnRepoChange: items mirrored from the old
// repository must not be planned or dispatched against the new one, so the
// same request that moves the repo deletes them (design D4).
func TestProjectEditClearsItemsOnRepoChange(t *testing.T) {
	h := newProjectsHarness(t, nil)
	id := h.seedRow(t, "existing-1", "org/one")
	records := []store.ItemRecord{
		{Number: 1, Kind: "task", State: "open", Title: "One", UpdatedAt: time.Now()},
		{Number: 2, Kind: "task", State: "open", Title: "Two", UpdatedAt: time.Now()},
		{Number: 3, Kind: "task", State: "open", Title: "Three", UpdatedAt: time.Now()},
	}
	if err := h.st.ReplaceItems(context.Background(), id, records); err != nil {
		t.Fatalf("seed items: %v", err)
	}

	resp, body := h.do(t, http.MethodPut, "/api/projects/existing-1",
		editBody("existing-1", func(r *ProjectRequest) { r.Repo = "org/moved" }))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	items, err := h.st.Items(context.Background())
	if err != nil {
		t.Fatalf("items: %v", err)
	}
	for _, item := range items {
		if item.ProjectID == id {
			t.Fatalf("an item from the old repository survived: %+v", item)
		}
	}
	if len(items) != 0 {
		t.Fatalf("items = %+v, want none after the repo change", items)
	}
}

// TestProjectEditAuditsTheChange: one event row per write, actor board, kind
// project.edit, subject project:<key>, detail naming the field with its old
// and new values — read back over /api/events, the surface the operator reads.
func TestProjectEditAuditsTheChange(t *testing.T) {
	h := newProjectsHarness(t, nil)
	resp, body := h.do(t, http.MethodPut, "/api/projects/existing-2",
		editBody("existing-2", func(r *ProjectRequest) { r.ConcurrencyCap = 3 }))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	resp, body = h.get(t, "/api/events")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("events status = %d (%s)", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"project.edit"`) || !strings.Contains(string(body), "project:existing-2") {
		t.Fatalf("events = %s, want a project.edit row for project:existing-2", body)
	}
	if !strings.Contains(string(body), "concurrency_cap 2 → 3") {
		t.Fatalf("events = %s, want the detail to name concurrency_cap 2 → 3", body)
	}
	if !strings.Contains(string(body), `"board"`) {
		t.Fatalf("events = %s, want actor board", body)
	}
}

// TestProjectOnHoldStatuses: the pause is DB-owned (design D5) — the flag
// flips in the store, the config file's bytes are identical, and the audit
// row records it.
func TestProjectOnHoldStatuses(t *testing.T) {
	h := newProjectsHarness(t, nil)
	h.seedRow(t, "existing-1", "org/one")

	resp, body := h.do(t, http.MethodPut, "/api/projects/existing-1/onhold", `{"onhold":true}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pause status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	row, err := h.st.ProjectByKey(context.Background(), "existing-1")
	if err != nil || row == nil || !row.OnHold {
		t.Fatalf("row = %+v (%v), want onhold", row, err)
	}
	if !bytes.Equal(h.bytes(t), h.orig) {
		t.Fatal("pausing wrote the config file; the pause is DB-owned")
	}

	resp, body = h.do(t, http.MethodPut, "/api/projects/existing-1/onhold", `{"onhold":false}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resume status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	if row, _ := h.st.ProjectByKey(context.Background(), "existing-1"); row.OnHold {
		t.Fatalf("row = %+v, want resumed", row)
	}

	for _, tc := range []struct {
		name string
		body string
		want int
	}{
		{"non-boolean", `{"onhold":"yes"}`, http.StatusUnprocessableEntity},
		{"malformed", `{"onhold":`, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := h.do(t, http.MethodPut, "/api/projects/existing-1/onhold", tc.body)
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d (%s)", resp.StatusCode, tc.want, body)
			}
		})
	}

	resp, _ = h.do(t, http.MethodPut, "/api/projects/nope/onhold", `{"onhold":true}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown key status = %d, want 404", resp.StatusCode)
	}
}

// TestProjectDeleteRequiresConfirm: a missing or different confirm is 400 and
// writes nothing — the typed key is the guard against deleting the wrong row.
func TestProjectDeleteRequiresConfirm(t *testing.T) {
	h := newProjectsHarness(t, nil)
	id := h.seedRow(t, "existing-1", "org/one")
	for _, path := range []string{"/api/projects/existing-1", "/api/projects/existing-1?confirm=wrong"} {
		resp, body := h.do(t, http.MethodDelete, path, "")
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("DELETE %s = %d, want 400 (%s)", path, resp.StatusCode, body)
		}
	}
	if !bytes.Equal(h.bytes(t), h.orig) {
		t.Fatal("a refused delete changed the file")
	}
	if row, _ := h.st.ProjectByKey(context.Background(), "existing-1"); row == nil || row.ID != id {
		t.Fatalf("row = %+v, want it intact", row)
	}
}

// TestProjectDeleteRefusesLiveDispatch: deleting mid-build is refused 409
// naming the dispatch, the item and the branch, with the remedy — pause, then
// retry after the row ends. Neither layer changes.
func TestProjectDeleteRefusesLiveDispatch(t *testing.T) {
	h := newProjectsHarness(t, nil)
	id := h.seedRow(t, "existing-1", "org/one")
	h.seedLiveDispatch(t, id)

	resp, body := h.do(t, http.MethodDelete, "/api/projects/existing-1?confirm=existing-1", "")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", resp.StatusCode, body)
	}
	message := string(body)
	for _, want := range []string{"dispatch 1", "item #48", "feature/48-live", "pause the project"} {
		if !strings.Contains(message, want) {
			t.Fatalf("409 body %q does not name %q", message, want)
		}
	}
	if !bytes.Equal(h.bytes(t), h.orig) {
		t.Fatal("a refused delete changed the file")
	}
	if row, _ := h.st.ProjectByKey(context.Background(), "existing-1"); row == nil {
		t.Fatal("a refused delete removed the store row")
	}
}

// TestProjectDeleteCascadeAndAudit: the delete removes the file entry and the
// project's rows, answers the counts the cascade removed, keeps the event
// rows, and the event is the record the deletion happened.
func TestProjectDeleteCascadeAndAudit(t *testing.T) {
	h := newProjectsHarness(t, nil)
	id := h.seedRow(t, "existing-1", "org/one")
	if err := h.st.ReplaceItems(context.Background(), id, []store.ItemRecord{
		{Number: 1, Kind: "task", State: "open", Title: "One", UpdatedAt: time.Now()},
		{Number: 2, Kind: "task", State: "open", Title: "Two", UpdatedAt: time.Now()},
	}); err != nil {
		t.Fatalf("seed items: %v", err)
	}
	dispatch, err := h.st.CreateDispatch(context.Background(), store.NewDispatch{
		ProjectID: id, ItemNumber: 1, Attempt: 1, Seat: "dev-build-1@fleet-console",
		Branch: "feature/1", Worktree: "/tmp/wt", BaseSHA: "base",
	})
	if err != nil {
		t.Fatalf("seed dispatch: %v", err)
	}
	if err := h.st.EndDispatch(context.Background(), dispatch, "merged"); err != nil {
		t.Fatalf("end dispatch: %v", err)
	}
	if _, err := h.st.AddVerdict(context.Background(), store.NewVerdict{
		DispatchID: dispatch, ProjectID: id, ItemNumber: 1, SHA: "abc1234", Decision: "accept", Source: "board",
	}); err != nil {
		t.Fatalf("seed verdict: %v", err)
	}

	resp, body := h.do(t, http.MethodDelete, "/api/projects/existing-1?confirm=existing-1", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	var answer DeleteProjectResponse
	if err := json.Unmarshal(body, &answer); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if answer.Deleted.Items != 2 || answer.Deleted.Dispatches != 1 || answer.Deleted.Verdicts != 1 {
		t.Fatalf("deleted = %+v, want 2 items, 1 dispatch, 1 verdict", answer.Deleted)
	}
	if strings.Contains(string(h.bytes(t)), "key: existing-1") {
		t.Fatalf("the file still names the deleted project:\n%s", h.bytes(t))
	}
	if row, _ := h.st.ProjectByKey(context.Background(), "existing-1"); row != nil {
		t.Fatalf("store row = %+v, want it deleted", row)
	}
	items, _ := h.st.Items(context.Background())
	if len(items) != 0 {
		t.Fatalf("items = %+v, want the cascade to remove them all", items)
	}
	exists, err := h.st.HasEvent(context.Background(), "project.delete", "project:existing-1", "items=2")
	if err != nil || !exists {
		t.Fatalf("HasEvent = %v, %v; want the deletion audited", exists, err)
	}
}

// TestProjectDeleteFailureLeavesPausedThenRetryCompletes: D1's order — pause,
// file, rows — means a failure at the file step leaves the project onhold, so
// the act loop plans nothing for it, and the same request retried completes.
func TestProjectDeleteFailureLeavesPausedThenRetryCompletes(t *testing.T) {
	fail := true
	remove := func(path, key string, expected *[32]byte) (configfile.Outcome, error) {
		if fail {
			return "", errors.New("configfile: rename: permission denied")
		}
		return configfile.RemoveProject(path, key, expected)
	}
	h := newProjectsHarnessWithRemove(t, nil, remove)
	h.seedRow(t, "existing-1", "org/one")

	resp, body := h.do(t, http.MethodDelete, "/api/projects/existing-1?confirm=existing-1", "")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (%s)", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "config file step") {
		t.Fatalf("body = %s, want it to name the step that failed", body)
	}
	row, err := h.st.ProjectByKey(context.Background(), "existing-1")
	if err != nil || row == nil || !row.OnHold {
		t.Fatalf("row = %+v (%v), want the failed delete to leave it paused", row, err)
	}
	if !strings.Contains(string(h.bytes(t)), "key: existing-1") {
		t.Fatal("the file lost the entry before the failing step")
	}

	fail = false
	resp, body = h.do(t, http.MethodDelete, "/api/projects/existing-1?confirm=existing-1", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("retry status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	if row, _ := h.st.ProjectByKey(context.Background(), "existing-1"); row != nil {
		t.Fatalf("store row = %+v, want the retry to complete the deletion", row)
	}
	if strings.Contains(string(h.bytes(t)), "key: existing-1") {
		t.Fatal("the retry did not remove the file entry")
	}
}

// TestProjectDeleteStoreOnlyAndReplay: a store-only project deletes without
// touching the file (the file step is skipped), and a replayed delete of a key
// in neither layer answers 200 with empty counts — D9's replay rule, not an
// error the client should have to distinguish from a typo.
func TestProjectDeleteStoreOnlyAndReplay(t *testing.T) {
	h := newProjectsHarness(t, nil)
	h.seedRow(t, "db-only", "o/db")

	resp, body := h.do(t, http.MethodDelete, "/api/projects/db-only?confirm=db-only", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	if !bytes.Equal(h.bytes(t), h.orig) {
		t.Fatal("a store-only delete changed the file")
	}
	if row, _ := h.st.ProjectByKey(context.Background(), "db-only"); row != nil {
		t.Fatal("the store row survived")
	}

	resp, body = h.do(t, http.MethodDelete, "/api/projects/db-only?confirm=db-only", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("replay status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	var answer DeleteProjectResponse
	if err := json.Unmarshal(body, &answer); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if answer.Deleted != (DeleteCounts{}) {
		t.Fatalf("replay deleted = %+v, want zeros", answer.Deleted)
	}
}

// TestProjectDeleteFileOnlyProject: a project the boot pass has not applied
// yet deletes from the file alone, with zero counts.
func TestProjectDeleteFileOnlyProject(t *testing.T) {
	h := newProjectsHarness(t, nil)
	resp, body := h.do(t, http.MethodDelete, "/api/projects/existing-2?confirm=existing-2", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	var answer DeleteProjectResponse
	if err := json.Unmarshal(body, &answer); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if answer.Deleted != (DeleteCounts{}) {
		t.Fatalf("deleted = %+v, want zeros for a project with no rows", answer.Deleted)
	}
	if strings.Contains(string(h.bytes(t)), "key: existing-2") {
		t.Fatalf("the file still names it:\n%s", h.bytes(t))
	}
	if !strings.Contains(string(h.bytes(t)), "key: existing-1") {
		t.Fatalf("the delete took the sibling entry with it:\n%s", h.bytes(t))
	}
}

// TestProjectListMergedView: config.Load ∪ store.Projects joined on key, with
// the three source values and the counts from one grouped query. The forge
// fake fails the test if the list so much as looks at it — design D7: no
// forge call on this path.
func TestProjectListMergedView(t *testing.T) {
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
	id, err := st.CreateProject(context.Background(), store.ProjectInput{
		Key: "existing-1", Name: "existing-1", ForgeKind: "github", ForgeProject: "org/one",
		Rig: "rig-a", ConcurrencyCap: 1, Enabled: true,
	})
	if err != nil {
		t.Fatalf("seed existing-1: %v", err)
	}
	if _, err := st.CreateProject(context.Background(), store.ProjectInput{
		Key: "db-only", Name: "db-only", ForgeKind: "github", ForgeProject: "o/db",
		Rig: "rig-a", ConcurrencyCap: 1, Enabled: true,
	}); err != nil {
		t.Fatalf("seed db-only: %v", err)
	}
	if err := st.ReplaceItems(context.Background(), id, []store.ItemRecord{
		{Number: 1, Kind: "task", State: "open", Title: "One", UpdatedAt: time.Now()},
		{Number: 2, Kind: "task", State: "open", Title: "Two", UpdatedAt: time.Now()},
	}); err != nil {
		t.Fatalf("seed items: %v", err)
	}
	ended, err := st.CreateDispatch(context.Background(), store.NewDispatch{
		ProjectID: id, ItemNumber: 1, Attempt: 1, Seat: "s", Branch: "b1", Worktree: "w1", BaseSHA: "s1",
	})
	if err != nil {
		t.Fatalf("seed ended dispatch: %v", err)
	}
	if err := st.EndDispatch(context.Background(), ended, "merged"); err != nil {
		t.Fatalf("end dispatch: %v", err)
	}
	open, err := st.CreateDispatch(context.Background(), store.NewDispatch{
		ProjectID: id, ItemNumber: 2, Attempt: 1, Seat: "s", Branch: "b2", Worktree: "w2", BaseSHA: "s2",
	})
	if err != nil {
		t.Fatalf("seed open dispatch: %v", err)
	}
	if open <= ended {
		t.Fatalf("open dispatch id %d is not the live one (ended %d)", open, ended)
	}

	app, err := New(Config{
		Token: testToken, Store: st, ConfigPath: path,
		ForgeFor: func(context.Context, store.ProjectRow) (forge.Forge, error) {
			t.Error("GET /api/projects called the forge")
			return nil, errors.New("the list must not read the forge")
		},
	})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer "+testToken)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (%s)", resp.StatusCode, body)
	}
	var listed ProjectsListResponse
	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	byKey := make(map[string]ProjectResponse, len(listed.Projects))
	for _, project := range listed.Projects {
		byKey[project.Key] = project
	}
	if len(byKey) != 3 {
		t.Fatalf("projects = %d keys %v, want 3", len(byKey), byKey)
	}
	if byKey["existing-1"].Source != "both" || byKey["existing-2"].Source != "file" || byKey["db-only"].Source != "db" {
		t.Fatalf("sources = %s/%s/%s, want both/file/db",
			byKey["existing-1"].Source, byKey["existing-2"].Source, byKey["db-only"].Source)
	}
	both := byKey["existing-1"]
	if both.Items != 2 || both.Dispatches != 2 || both.LiveDispatch != open {
		t.Fatalf("counts = %+v, want 2 items, 2 dispatches, live %d", both, open)
	}
	if !both.Enabled || both.OnHold {
		t.Fatalf("flags = enabled %t onhold %t, want enabled and not paused", both.Enabled, both.OnHold)
	}
	fileOnly := byKey["existing-2"]
	if !fileOnly.Enabled || fileOnly.OnHold || fileOnly.Items != 0 || fileOnly.Dispatches != 0 || fileOnly.LiveDispatch != 0 {
		t.Fatalf("file-only row = %+v, want enabled, no counts", fileOnly)
	}
	if dbOnly := byKey["db-only"]; !dbOnly.Enabled || dbOnly.Items != 0 {
		t.Fatalf("db-only row = %+v, want enabled with zero counts", dbOnly)
	}
}

// TestProjectRoutesRequireAuth: all three new routes sit inside the
// authenticated group, so a caller with no cookie and no token writes nothing.
func TestProjectRoutesRequireAuth(t *testing.T) {
	h := newProjectsHarness(t, nil)
	h.seedRow(t, "existing-1", "org/one")
	requests := []struct {
		method, path, body string
	}{
		{http.MethodPut, "/api/projects/existing-1", editBody("existing-1", nil)},
		{http.MethodPut, "/api/projects/existing-1/onhold", `{"onhold":true}`},
		{http.MethodDelete, "/api/projects/existing-1?confirm=existing-1", ""},
	}
	for _, tc := range requests {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		if tc.body != "" {
			req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		}
		req.Header.Set(idempotencyKeyHeader, "unauthenticated-"+tc.method)
		resp, err := h.app.Test(req)
		if err != nil {
			t.Fatalf("%s %s: %v", tc.method, tc.path, err)
		}
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s %s = %d, want 401", tc.method, tc.path, resp.StatusCode)
		}
	}
	if !bytes.Equal(h.bytes(t), h.orig) {
		t.Fatal("an unauthenticated request changed the file")
	}
	if row, _ := h.st.ProjectByKey(context.Background(), "existing-1"); row == nil || row.OnHold {
		t.Fatalf("row = %+v, want it untouched", row)
	}
}

// TestProjectForgeHostRefusalsAre422: the two shapes forge-host design D3
// refuses arrive as 422 with the message on the forge_host field — the same
// message config.Load answers — and a refused create changes no bytes.
func TestProjectForgeHostRefusalsAre422(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "contradiction",
			body: `{"key":"hosted","name":"Hosted","forge":"gitlab","repo":"o/r",` +
				`"repo_url":"https://gitlab.other/o/r","forge_host":"gitlab.example.com",` +
				`"rigs":["rig-c"],"concurrency_cap":1,"paths":{"main":"./c","worktrees":"./w"}}`,
			want: `forge_host "gitlab.example.com" contradicts repo_url's host "gitlab.other"`,
		},
		{
			name: "github host",
			body: `{"key":"gh-host","name":"GH","forge":"github","repo":"o/r",` +
				`"forge_host":"git.example.com","rigs":["rig-c"],"concurrency_cap":1,` +
				`"paths":{"main":"./c","worktrees":"./w"}}`,
			want: "forge_host is only read for gitlab; GitHub's host is gh's business (GH_HOST)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newProjectsHarness(t, nil)
			resp, raw := h.post(t, tc.body)
			if resp.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (%s)", resp.StatusCode, raw)
			}
			var answer ErrorResponse
			if err := json.Unmarshal(raw, &answer); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if answer.Errors["forge_host"] != tc.want {
				t.Fatalf("errors = %v, want forge_host: %q", answer.Errors, tc.want)
			}
			if !bytes.Equal(h.bytes(t), h.orig) {
				t.Fatal("a refused create changed the file")
			}
		})
	}
}

// TestProjectRepoURLForgeHostRoundTrip is the promise the spec states (D6):
// a project created from the board with both fields carries them in the file,
// in config.Load's answer — what --print-config prints — in the list read,
// and an edit that does not name them leaves the file's two lines alone.
func TestProjectRepoURLForgeHostRoundTrip(t *testing.T) {
	ctx := context.Background()
	h := newProjectsHarness(t, nil)
	body := `{"key":"hosted","name":"Hosted","forge":"gitlab","repo":"o/r",` +
		`"repo_url":"https://gitlab.example.com/o/r.git","forge_host":"gitlab.example.com",` +
		`"rigs":["rig-c"],"concurrency_cap":1,"paths":{"main":"./c","worktrees":"./w"}}`
	resp, raw := h.post(t, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", resp.StatusCode, raw)
	}
	var created CreateProjectResponse
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Project.RepoURL != "https://gitlab.example.com/o/r.git" || created.Project.ForgeHost != "gitlab.example.com" {
		t.Fatalf("project = %+v, want both fields echoed", created.Project)
	}

	written := string(h.bytes(t))
	for _, want := range []string{
		"key: hosted",
		"repo_url: https://gitlab.example.com/o/r.git",
		"forge_host: gitlab.example.com",
	} {
		if !strings.Contains(written, want) {
			t.Fatalf("file lost %q:\n%s", want, written)
		}
	}

	// What --print-config prints is config.Load's answer: both fields survive
	// the write, and the resolver names the file's host.
	eff, err := config.Load(config.Options{FlagPath: h.path})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	entry, ok := findProject(eff.Projects, "hosted")
	if !ok {
		t.Fatalf("load = %+v, want hosted", eff.Projects)
	}
	if entry.RepoURL != "https://gitlab.example.com/o/r.git" || entry.ForgeHost != "gitlab.example.com" {
		t.Fatalf("entry = %+v, want both fields", entry)
	}
	if host, source, err := config.ForgeHost(entry); err != nil || host != "gitlab.example.com" || source != "forge_host" {
		t.Fatalf("ForgeHost = (%q, %q, %v), want gitlab.example.com from forge_host", host, source, err)
	}

	respList, rawList := h.get(t, "/api/projects")
	if respList.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d (%s)", respList.StatusCode, rawList)
	}
	var list ProjectsListResponse
	if err := json.Unmarshal(rawList, &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	var listed *ProjectResponse
	for i := range list.Projects {
		if list.Projects[i].Key == "hosted" {
			listed = &list.Projects[i]
		}
	}
	if listed == nil || listed.RepoURL != "https://gitlab.example.com/o/r.git" || listed.ForgeHost != "gitlab.example.com" {
		t.Fatalf("list row = %+v, want both fields", listed)
	}

	// The same definition back with one harmless change: the file's two lines
	// survive, and the diff names neither of them.
	edit := `{"key":"hosted","name":"Hosted","forge":"gitlab","repo":"o/r",` +
		`"repo_url":"https://gitlab.example.com/o/r.git","forge_host":"gitlab.example.com",` +
		`"rigs":["rig-c"],"concurrency_cap":3,"paths":{"main":"./c","worktrees":"./w"}}`
	respEdit, rawEdit := h.do(t, http.MethodPut, "/api/projects/hosted", edit)
	if respEdit.StatusCode != http.StatusOK {
		t.Fatalf("edit status = %d (%s)", respEdit.StatusCode, rawEdit)
	}
	var answer EditProjectResponse
	if err := json.Unmarshal(rawEdit, &answer); err != nil {
		t.Fatalf("decode edit: %v", err)
	}
	if len(answer.Changed) != 1 || answer.Changed[0] != "concurrency_cap" {
		t.Fatalf("changed = %v, want [concurrency_cap]", answer.Changed)
	}
	written = string(h.bytes(t))
	for _, want := range []string{
		"repo_url: https://gitlab.example.com/o/r.git",
		"forge_host: gitlab.example.com",
	} {
		if !strings.Contains(written, want) {
			t.Fatalf("edit lost %q:\n%s", want, written)
		}
	}
	row, err := h.st.ProjectByKey(ctx, "hosted")
	if err != nil || row == nil || row.ConcurrencyCap != 3 {
		t.Fatalf("store row = %+v (%v), want cap 3", row, err)
	}
}

// TestProjectEditRefusesForgeHostChangeUnderLiveDispatch: forge_host is an
// identity field (forge-host design D3) — it names the forge the merge gate
// judges on, so a change under a live dispatch is refused naming the dispatch,
// like repo.
func TestProjectEditRefusesForgeHostChangeUnderLiveDispatch(t *testing.T) {
	h := newProjectsHarness(t, nil)
	body := `{"key":"hosted","name":"Hosted","forge":"gitlab","repo":"o/r",` +
		`"repo_url":"https://gitlab.example.com/o/r.git","forge_host":"gitlab.example.com",` +
		`"rigs":["rig-c"],"concurrency_cap":1,"paths":{"main":"./c","worktrees":"./w"}}`
	resp, raw := h.post(t, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201 (%s)", resp.StatusCode, raw)
	}
	// The create registered the store row; the live dispatch hangs off it.
	seeded, err := h.st.ProjectByKey(context.Background(), "hosted")
	if err != nil || seeded == nil {
		t.Fatalf("store row = %+v (%v), want the created project", seeded, err)
	}
	h.seedLiveDispatch(t, seeded.ID)
	before, err := os.Stat(h.path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}

	// Both fields move together, so validation passes and only the identity
	// rule can refuse: the host the merge gate judges on must not move under
	// a live dispatch.
	edit := `{"key":"hosted","name":"Hosted","forge":"gitlab","repo":"o/r",` +
		`"repo_url":"https://gitlab.moved.example/o/r.git","forge_host":"gitlab.moved.example",` +
		`"rigs":["rig-c"],"concurrency_cap":1,"paths":{"main":"./c","worktrees":"./w"}}`
	respEdit, rawEdit := h.do(t, http.MethodPut, "/api/projects/hosted", edit)
	if respEdit.StatusCode != http.StatusConflict {
		t.Fatalf("edit status = %d, want 409 (%s)", respEdit.StatusCode, rawEdit)
	}
	message := string(rawEdit)
	for _, want := range []string{"dispatch 1", "pause the project"} {
		if !strings.Contains(message, want) {
			t.Fatalf("409 body %q does not name %q", message, want)
		}
	}
	after, err := os.Stat(h.path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("a refused identity edit touched the config file")
	}
	if !strings.Contains(string(h.bytes(t)), "forge_host: gitlab.example.com") {
		t.Fatalf("file:\n%s", h.bytes(t))
	}
	if row, _ := h.st.ProjectByKey(context.Background(), "hosted"); row == nil {
		t.Fatalf("store row = %+v, want it untouched", row)
	}
}

// TestProjectWritesWakeTheLoop: every board write that changes what the next
// tick would see asks the mirror loop for a tick now. The loop's quiet backoff
// reaches idle_poll_max (ten minutes by default), so without the wake a project
// created on the board stays invisible until the next scheduled poll, and the
// operator reads the silence as a dead daemon.
func TestProjectWritesWakeTheLoop(t *testing.T) {
	h := newProjectsHarness(t, nil)
	expectWake := func(step string) {
		t.Helper()
		select {
		case <-h.waker.Channel():
		default:
			t.Fatalf("%s did not wake the mirror loop: the change would wait out idle_poll_max", step)
		}
	}

	resp, body := h.post(t, validProject)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201 (%s)", resp.StatusCode, body)
	}
	expectWake("create")

	resp, body = h.do(t, http.MethodPut, "/api/projects/existing-1", editBody("existing-1", func(r *ProjectRequest) { r.Name = "Renamed one" }))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("edit status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	expectWake("edit")

	resp, body = h.do(t, http.MethodDelete, "/api/projects/existing-2?confirm=existing-2", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	expectWake("delete")
}

// TestProjectRelayLivesOnTheRow (#375): the board can set verdict_relay. The
// edit writes the file block and the store row together, the answer reports
// the pair, and the loader's rule — a mode needs a seat — refuses the pair the
// file would refuse.
func TestProjectRelayLivesOnTheRow(t *testing.T) {
	h := newProjectsHarness(t, nil)

	// The board's create carries the pair too: the row exists before any edit.
	createBody := `{"key":"relay-created","name":"Relay created","forge":"github","repo":"org/relay",
	 "default_branch":"main","rigs":["rig-a-2"],"concurrency_cap":2,
	 "relay_mode":"operator","relay_seat":"operator-agent@kernel",
	 "paths":{"main":"./checkouts/relay","worktrees":"./wt"}}`
	if resp, body := h.post(t, createBody); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201 (%s)", resp.StatusCode, body)
	}
	created, err := h.st.ProjectByKey(context.Background(), "relay-created")
	if err != nil || created == nil {
		t.Fatalf("created row: %+v (%v)", created, err)
	}
	if created.RelayMode != "operator" || created.RelaySeat != "operator-agent@kernel" {
		t.Fatalf("created relay = (%q, %q), want the posted pair", created.RelayMode, created.RelaySeat)
	}

	resp, body := h.do(t, http.MethodPut, "/api/projects/existing-1",
		editBody("existing-1", func(r *ProjectRequest) {
			r.RelayMode, r.RelaySeat = "operator", "operator-agent@kernel"
		}))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	var answer EditProjectResponse
	if err := json.Unmarshal(body, &answer); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if answer.Project.RelayMode != "operator" || answer.Project.RelaySeat != "operator-agent@kernel" {
		t.Fatalf("answer relay = (%q, %q), want the posted pair", answer.Project.RelayMode, answer.Project.RelaySeat)
	}
	row, err := h.st.ProjectByKey(context.Background(), "existing-1")
	if err != nil || row == nil {
		t.Fatalf("row: %+v (%v)", row, err)
	}
	if row.RelayMode != "operator" || row.RelaySeat != "operator-agent@kernel" {
		t.Fatalf("row relay = (%q, %q), want the posted pair", row.RelayMode, row.RelaySeat)
	}
	written := string(h.bytes(t))
	if !strings.Contains(written, "verdict_relay:") || !strings.Contains(written, "operator-agent@kernel") {
		t.Fatalf("file does not carry the relay block:\n%s", written)
	}

	// Clearing it is an explicit off on both layers.
	resp, body = h.do(t, http.MethodPut, "/api/projects/existing-1",
		editBody("existing-1", func(r *ProjectRequest) { r.RelayMode, r.RelaySeat = "", "" }))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("clear status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	row, _ = h.st.ProjectByKey(context.Background(), "existing-1")
	if row.RelayMode != "" || row.RelaySeat != "" {
		t.Fatalf("row relay = (%q, %q), want cleared", row.RelayMode, row.RelaySeat)
	}
	// Clearing it is an explicit off on both layers: the row empties and the
	// file keeps the empty block, which is the statement that clears it — an
	// absent block would leave the row alone.
	if !strings.Contains(string(h.bytes(t)), "verdict_relay: {}") {
		t.Fatalf("file lost the explicit off block:\n%s", h.bytes(t))
	}

	// The file's own rule: a mode with no seat is refused on the field.
	resp, body = h.do(t, http.MethodPut, "/api/projects/existing-1",
		editBody("existing-1", func(r *ProjectRequest) { r.RelayMode, r.RelaySeat = "operator", "" }))
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "verdict_relay.seat") {
		t.Fatalf("body = %s, want the seat field named", body)
	}
}

// TestProjectReviewerRoundTrip (#384): the board carries the reviewer prefix
// through the whole-definition edit — wrote it, it is in the file and the
// answer; cleared it, the file says nothing and the default applies.
func TestProjectReviewerRoundTrip(t *testing.T) {
	h := newProjectsHarness(t, nil)

	resp, body := h.do(t, http.MethodPut, "/api/projects/existing-1",
		editBody("existing-1", func(r *ProjectRequest) { r.Reviewer = "dev-reviewer" }))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	var answer EditProjectResponse
	if err := json.Unmarshal(body, &answer); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if answer.Project.Reviewer != "dev-reviewer" {
		t.Fatalf("answer reviewer = %q, want dev-reviewer", answer.Project.Reviewer)
	}
	if !strings.Contains(string(h.bytes(t)), "reviewer: dev-reviewer") {
		t.Fatalf("file does not carry the reviewer:\n%s", h.bytes(t))
	}

	resp, body = h.do(t, http.MethodPut, "/api/projects/existing-1",
		editBody("existing-1", func(r *ProjectRequest) { r.Reviewer = "" }))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("clear status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if answer.Project.Reviewer != "dev-review" {
		t.Fatalf("cleared reviewer = %q, want the default filled in", answer.Project.Reviewer)
	}
	if strings.Contains(string(h.bytes(t)), "reviewer:") {
		t.Fatalf("file kept a default reviewer key:\n%s", h.bytes(t))
	}
}

// TestProjectEpicSerialRoundTrip (#443 follow-up): epic_serial: false is an
// operator's escape hatch, and the fixed edit form carries it back on every
// save. The API contract that makes that work: an edit sending the value keeps
// it, and only a genuine change is reported as changed.
func TestProjectEpicSerialRoundTrip(t *testing.T) {
	h := newProjectsHarness(t, nil)
	off := false

	resp, body := h.do(t, http.MethodPut, "/api/projects/existing-1",
		editBody("existing-1", func(r *ProjectRequest) { r.EpicSerial = &off }))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	if !strings.Contains(string(h.bytes(t)), "epic_serial: false") {
		t.Fatalf("file does not carry the escape hatch:\n%s", h.bytes(t))
	}

	// The shape the fixed dialog sends: the row's own value carried on a rename.
	resp, body = h.do(t, http.MethodPut, "/api/projects/existing-1",
		editBody("existing-1", func(r *ProjectRequest) { r.Name = "Renamed one"; r.EpicSerial = &off }))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rename status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	var answer EditProjectResponse
	if err := json.Unmarshal(body, &answer); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(answer.Changed) != 1 || answer.Changed[0] != "name" {
		t.Fatalf("changed = %v, want only the name", answer.Changed)
	}
	if !strings.Contains(string(h.bytes(t)), "epic_serial: false") {
		t.Fatalf("a rename erased the escape hatch:\n%s", h.bytes(t))
	}
}
