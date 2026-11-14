package web

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dinhtrung/fleet-console/internal/config"
	"github.com/dinhtrung/fleet-console/internal/configfile"
	"github.com/dinhtrung/fleet-console/internal/mirror"
	"github.com/dinhtrung/fleet-console/internal/registry"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
	"github.com/gofiber/fiber/v3"
)

// ProjectRequest is the body of POST /api/projects: the shape the file uses,
// snake_case as the YAML tags spell it.
// rigRefsFromNames is the API's boundary: the projects route publishes and accepts
// rigs as plain names, so a name arriving over the wire becomes the tier-0,
// on-full entry that the union decode gives a bare string. Tiers are configured in
// the file until a later task widens this contract.
func rigRefsFromNames(names []string) config.RigRefs {
	out := make(config.RigRefs, 0, len(names))
	for _, name := range names {
		out = append(out, config.RigRef{Name: name, Tier: 0, Spill: config.SpillOnFull})
	}
	return out
}

type ProjectRequest struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	Forge string `json:"forge"`
	Repo  string `json:"repo"`
	// RepoURL is the clone URL (forge-host, design D6): it drives setup and
	// preflight, and for GitLab its host is the forge API's host when
	// forge_host is empty. Empty keeps today's slug-derived behaviour.
	RepoURL string `json:"repo_url"`
	// ForgeHost names the host the forge API lives on. Empty is the resolver's
	// answer (repo_url's host, else gitlab.com); set, it is read for GitLab
	// only — config.ValidateProject refuses it for GitHub.
	ForgeHost      string            `json:"forge_host"`
	DefaultBranch  string            `json:"default_branch"`
	Rigs           []string          `json:"rigs"`
	RigTemplate    string            `json:"rig_template"`
	ConcurrencyCap int               `json:"concurrency_cap"`
	GateCmd        string            `json:"gate_cmd"`
	Labels         map[string]string `json:"labels"`
	OnHold         *bool             `json:"onhold"`
	// CultureFile is where the rig culture lives. Without it a board-created
	// project can never run `apply_rig`: internal/rig refuses to render a rig
	// with no culture file, and nothing derives one (design: "a missing culture
	// file is explained, not created").
	CultureFile string `json:"culture_file"`
	// RelayMode and RelaySeat are the verdict relay (#375): "" (off), scribe or
	// operator, with a seat required whenever a mode is set. The rule is the
	// file's own (config.ValidateProject), so the API cannot write a pair the
	// loader would refuse.
	RelayMode string `json:"relay_mode"`
	RelaySeat string `json:"relay_seat"`
	// Reviewer is the member prefix review rows go to (#384). Empty means the
	// default; the whole-definition PUT carries it or an edit would drop it.
	Reviewer string `json:"reviewer"`
	// EpicSerial is the one-child-per-epic rule (#443); nil means the default
	// (true). The whole-definition PUT carries it, or an edit would drop it.
	EpicSerial *bool `json:"epic_serial"`
	Paths      struct {
		Main      string `json:"main"`
		Worktrees string `json:"worktrees"`
	} `json:"paths"`
}

// ProjectResponse is one project as the board reads it. Enabled and OnHold are
// the store's own view — the DB-owned pause, not the file's `onhold:` — for any
// project that has a row; counts come from one grouped query, never a
// per-project loop (design D7). A file-only project reports enabled and its
// file-pinned onhold, with zero counts.
type ProjectResponse struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	Forge string `json:"forge"`
	Repo  string `json:"repo"`
	// RepoURL and ForgeHost are the two clone/API fields (forge-host, design
	// D6): the board writes the same two it reads, so an edit round-trips.
	RepoURL   string `json:"repo_url"`
	ForgeHost string `json:"forge_host"`

	DefaultBranch  string            `json:"default_branch"`
	Rigs           []string          `json:"rigs"`
	RigTemplate    string            `json:"rig_template"`
	ConcurrencyCap int               `json:"concurrency_cap"`
	GateCmd        string            `json:"gate_cmd"`
	Labels         map[string]string `json:"labels"`
	Enabled        bool              `json:"enabled"`
	OnHold         bool              `json:"onhold"`
	Items          int64             `json:"items"`
	Dispatches     int64             `json:"dispatches"`
	LiveDispatch   int64             `json:"live_dispatch"`
	CultureFile    string            `json:"culture_file"`
	// RelayMode and RelaySeat are the effective verdict relay (#375): the store
	// row first, the file block second — the same answer RelayFor gives the
	// tick, so the board edits the pair the gate actually uses.
	RelayMode string `json:"relay_mode"`
	RelaySeat string `json:"relay_seat"`
	// Reviewer is the member prefix review rows go to (#384). Empty means the
	// default; the whole-definition PUT carries it or an edit would drop it.
	Reviewer string `json:"reviewer"`
	// EpicSerial is the one-child-per-epic rule (#443); nil means the default
	// (true). The whole-definition PUT carries it, or an edit would drop it.
	EpicSerial *bool `json:"epic_serial"`
	Paths      struct {
		Main      string `json:"main"`
		Worktrees string `json:"worktrees"`
	} `json:"paths"`
	Source string `json:"source"`
	// Waiting is the project's "waiting on a human" panel (#429): the seats on
	// its rigs that have stopped on a person, and where a person answers them.
	// Known=false means the rig read failed — an unread rig is not a clear one.
	Waiting WaitingPanel `json:"waiting"`
}

type ProjectsListResponse struct {
	Projects []ProjectResponse `json:"projects"`
}

type CreateProjectResponse struct {
	Project  ProjectResponse `json:"project"`
	Warnings []string        `json:"warnings"`
}

// EditProjectResponse is PUT /api/projects/{key}'s answer: the stored project,
// the field names that changed, and the loader's warnings.
type EditProjectResponse struct {
	Project  ProjectResponse `json:"project"`
	Changed  []string        `json:"changed"`
	Warnings []string        `json:"warnings"`
}

// OnHoldRequest is PUT /api/projects/{key}/onhold's body. OnHold stays
// untyped on purpose: a strict *bool decode turns a non-boolean into 400
// malformed JSON, and the contract here is 422 on the `onhold` field.
type OnHoldRequest struct {
	OnHold any `json:"onhold"`
}

// DeleteCounts is what the delete removed — the same numbers the dialog warned
// with (design D7: one set of numbers, from the list to the cascade).
type DeleteCounts struct {
	Items      int64 `json:"items"`
	Dispatches int64 `json:"dispatches"`
	Verdicts   int64 `json:"verdicts"`
}

type DeleteProjectResponse struct {
	Deleted DeleteCounts `json:"deleted"`
}

type ErrorResponse struct {
	Error  string            `json:"error"`
	Errors map[string]string `json:"errors,omitempty"`
}

// wake asks the mirror loop for a tick now. A board write changes what the next
// tick would see, and a quiet loop may be idling on its backoff (idle_poll_max,
// ten minutes by default), so without this the new project is invisible for
// minutes and the operator reads it as a dead daemon. Nil is safe: the one-shot
// commands run no loop.
func wake(w *mirror.Waker) {
	if w != nil {
		w.Wake()
	}
}

// RegisterProjectsRoutes registers the projects surface inside the
// authenticated API group: the merged list, the create, the edit, the
// DB-owned pause and the delete (tasks §3).
func RegisterProjectsRoutes(api fiber.Router, cfg Config, configPath string) {
	write := cfg.WriteProject
	if write == nil {
		write = configfile.UpsertProject
	}
	update := cfg.UpdateProject
	if update == nil {
		update = configfile.UpdateProject
	}
	remove := cfg.RemoveProject
	if remove == nil {
		remove = configfile.RemoveProject
	}
	api.Get("/projects", listProjects(cfg.Store, configPath, cfg.Rig, cfg.Now))
	api.Post("/projects", createProject(cfg.Store, configPath, write, cfg.Waker))
	api.Put("/projects/:key", editProject(cfg.Store, configPath, update, cfg.Waker))
	api.Put("/projects/:key/onhold", setProjectOnHold(cfg.Store, configPath, cfg.Waker))
	api.Delete("/projects/:key", deleteProject(cfg.Store, configPath, remove, cfg.Waker))
}

// mergedView is one read of both layers, joined on key. The list, the pause
// and the delete all start here, so every route answers from the same picture.
type mergedView struct {
	eff    *config.Effective
	rows   map[string]*store.ProjectRow
	counts map[int64]store.ProjectCount
}

func loadMergedView(ctx context.Context, st *store.Store, configPath string) (*mergedView, error) {
	eff, err := config.Load(config.Options{FlagPath: configPath})
	if err != nil {
		return nil, err
	}
	rows, err := st.Projects(ctx)
	if err != nil {
		return nil, err
	}
	counts, err := st.ProjectCounts(ctx)
	if err != nil {
		return nil, err
	}
	view := &mergedView{eff: eff, rows: make(map[string]*store.ProjectRow, len(rows)), counts: counts}
	for index := range rows {
		view.rows[rows[index].Key] = &rows[index]
	}
	return view, nil
}

// entry returns the file's definition for a key, if the file names it.
func (v *mergedView) entry(key string) (config.Project, bool) {
	return findProject(v.eff.Projects, key)
}

// sourceClassifies where a project lives (design D7's table).
func (v *mergedView) source(key string) string {
	_, inFile := v.entry(key)
	_, inStore := v.rows[key]
	switch {
	case inFile && inStore:
		return "both"
	case inFile:
		return "file"
	default:
		return "db"
	}
}

// render builds one board row from the merged view: the file's definition when
// there is one, the store's enabled/onhold and counts when there is a row.
func (v *mergedView) render(key string) ProjectResponse {
	project, inFile := v.entry(key)
	row := v.rows[key]
	if !inFile {
		return renderRow(row, v.counts[row.ID], v.source(key))
	}
	out := projectResponse(project, v.source(key))
	if row != nil {
		out.Enabled = row.Enabled
		out.OnHold = row.OnHold
		// The row answers first, exactly as RelayFor does (#375): a store-only
		// relay is what the tick uses, so it is what the board shows.
		if row.RelayMode != "" {
			out.RelayMode, out.RelaySeat = row.RelayMode, row.RelaySeat
		}
		if count, ok := v.counts[row.ID]; ok {
			out.Items, out.Dispatches, out.LiveDispatch = count.Items, count.Dispatches, count.LiveDispatch
		}
	}
	return out
}

// renderRow is the board row of a store-only project: the row is everything
// the board knows.
func renderRow(row *store.ProjectRow, count store.ProjectCount, source string) ProjectResponse {
	out := ProjectResponse{
		Key:            row.Key,
		Name:           row.Name,
		Forge:          row.ForgeKind,
		Repo:           row.ForgeProject,
		DefaultBranch:  row.DefaultBranch,
		Rigs:           row.Rigs,
		RigTemplate:    row.RigTemplate,
		ConcurrencyCap: row.ConcurrencyCap,
		Enabled:        row.Enabled,
		OnHold:         row.OnHold,
		Items:          count.Items,
		Dispatches:     count.Dispatches,
		LiveDispatch:   count.LiveDispatch,
		Source:         source,
		RelayMode:      row.RelayMode,
		RelaySeat:      row.RelaySeat,
		Reviewer:       config.DefaultReviewer,
		EpicSerial:     epicSerialPtr(true),
	}
	if out.Rigs == nil {
		out.Rigs = []string{}
	}
	if out.Labels == nil {
		out.Labels = map[string]string{}
	}
	return out
}

// listProjects answers GET /api/projects: config.Load ∪ store.Projects joined
// on key, with counts from one grouped query. The route makes no forge call —
// the list renders on every visit and a per-project forge read would put the
// board's own traffic into the mirror's rate-limit budget (design D7).
func listProjects(st *store.Store, configPath string, rigs *rig.Adapter, clock func() time.Time) fiber.Handler {
	return func(c fiber.Ctx) error {
		if configPath == "" {
			return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: "config.yaml path is not configured"})
		}
		view, err := loadMergedView(c.Context(), st, configPath)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
		}
		projects := make([]ProjectResponse, 0, len(view.eff.Projects)+len(view.rows))
		seen := make(map[string]bool, len(view.eff.Projects))
		for _, project := range view.eff.Projects {
			projects = append(projects, view.render(project.Key))
			seen[project.Key] = true
		}
		// The store-only rows follow, in the store's own order, so a row the
		// file does not name is still listed (design D7: it is a --prune
		// candidate the operator must see).
		for _, row := range view.rows {
			if !seen[row.Key] {
				projects = append(projects, view.render(row.Key))
			}
		}
		// The waiting panel reads each project's own snapshots; the UI address
		// is one read for the whole request, not one per waiting project (#429):
		// /api/projects renders on every visit, and a per-project CLI call would
		// put the board's own traffic on the daemon. It is skipped entirely when
		// no project has anything waiting.
		now := time.Now().UTC()
		if clock != nil {
			now = clock().UTC()
		}
		uiURL := ""
		for index := range projects {
			row, ok := view.rows[projects[index].Key]
			if !ok {
				projects[index].Waiting = WaitingPanel{
					Seats:  []WaitingSeat{},
					Reason: "this project has no store row yet, so its rig has not been read",
				}
				continue
			}
			projects[index].Waiting = waitingPanel(c.Context(), st, row.ID, "", now)
			if len(projects[index].Waiting.Seats) > 0 {
				uiURL = waitingUIURL(c.Context(), rigs, uiURL)
			}
		}
		for index := range projects {
			if len(projects[index].Waiting.Seats) > 0 {
				projects[index].Waiting.UIURL = uiURL
			}
		}
		return c.JSON(ProjectsListResponse{Projects: projects})
	}
}

// createProject validates one definition, writes the file, then upserts the
// store on the same path --apply-config uses. The order is the promise: a
// refused write leaves both the file and the store untouched.
func createProject(st *store.Store, configPath string, write func(string, configfile.Project, *[32]byte) (configfile.Outcome, error), waker *mirror.Waker) fiber.Handler {
	return func(c fiber.Ctx) error {
		if configPath == "" {
			return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: "config.yaml path is not configured"})
		}
		var req ProjectRequest
		if err := c.Bind().JSON(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{Error: "malformed JSON"})
		}

		dir := filepath.Dir(configPath)
		project := requestProject(req)

		// One rule set: the same function the loader runs, answering one message
		// per field.
		if err := config.ValidateProject(&project, dir, homeDir()); err != nil {
			return validationFailure(c, err)
		}

		// The bytes this request read are the bytes the write must still find.
		data, err := os.ReadFile(configPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
		}
		expected := sha256.Sum256(data)

		outcome, err := write(configPath, writtenProject(project), &expected)
		if err != nil {
			return writeFailure(c, configPath, err)
		}
		if outcome == configfile.OutcomeUnchanged {
			// The file already holds this key: design §5 answers 409, and a
			// replayed request is the Idempotency-Key middleware's job.
			return c.Status(fiber.StatusConflict).JSON(ErrorResponse{
				Error: fmt.Sprintf("duplicate project key %q", project.Key),
			})
		}

		// Reload: the answer carries the project as the file resolves it
		// (trimmed, paths expanded, defaults filled).
		eff, err := config.Load(config.Options{FlagPath: configPath})
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
		}
		created, found := findProject(eff.Projects, project.Key)
		if !found {
			return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: "project not found after write"})
		}

		if _, err := registry.ApplyOne(c.Context(), st, created, false); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
		}
		// The file and the row are both written: the next tick can see the project.
		wake(waker)

		return c.Status(fiber.StatusCreated).JSON(CreateProjectResponse{
			Project:  projectWithRow(c.Context(), st, created, "both"),
			Warnings: warningsFor(eff.Warnings, created),
		})
	}
}

// editProject answers PUT /api/projects/{key}: a whole-definition PUT of the
// file's project shape (design D2). The key is immutable (D8); an identity
// change is refused while a dispatch is live (D3); a repo or forge change
// clears the mirrored items in the same request (D4); one audit row per write
// (D10). The refusal checks run before the write, so a refused edit writes
// neither layer — the spec requirement and the unchanged-mtime test both.
func editProject(st *store.Store, configPath string, write func(string, configfile.Project, *[32]byte) (configfile.Outcome, error), waker *mirror.Waker) fiber.Handler {
	return func(c fiber.Ctx) error {
		if configPath == "" {
			return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: "config.yaml path is not configured"})
		}
		key := c.Params("key")
		var req ProjectRequest
		if err := c.Bind().JSON(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{Error: "malformed JSON"})
		}
		// D8: a rename is a delete and a create, and both routes say so.
		if req.Key != "" && req.Key != key {
			return c.Status(fiber.StatusUnprocessableEntity).JSON(ErrorResponse{
				Error:  "validation failed",
				Errors: map[string]string{"key": "key is immutable; delete and recreate"},
			})
		}
		req.Key = key

		dir := filepath.Dir(configPath)
		project := requestProject(req)
		if err := config.ValidateProject(&project, dir, homeDir()); err != nil {
			return validationFailure(c, err)
		}

		view, err := loadMergedView(c.Context(), st, configPath)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
		}
		oldEntry, inFile := view.entry(key)
		row, inStore := view.rows[key]
		if !inFile && !inStore {
			return c.Status(fiber.StatusNotFound).JSON(ErrorResponse{
				Error: fmt.Sprintf("project %q is not registered", key),
			})
		}
		if !inFile {
			// D7: a store-only project is a --prune candidate; the file is the
			// source of an edit, so the edit refuses naming it.
			return c.Status(fiber.StatusConflict).JSON(ErrorResponse{
				Error: fmt.Sprintf("project %q lives only in the database; %s is the source of an edit", key, filepath.Base(configPath)),
			})
		}

		// D3, before any write: an identity field change under a live dispatch
		// loses the builder's work at merge time, so it is refused naming the
		// dispatch, the item and the branch.
		if _, identity := projectDiff(oldEntry, project); len(identity) > 0 && inStore {
			if refusal, ok, err := liveDispatchRefusal(c.Context(), st, row.ID); err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
			} else if ok {
				return c.Status(fiber.StatusConflict).JSON(ErrorResponse{Error: refusal})
			}
		}

		data, err := os.ReadFile(configPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
		}
		expected := sha256.Sum256(data)

		if _, err := write(configPath, writtenProject(project), &expected); err != nil {
			return writeFailure(c, configPath, err)
		}

		eff, err := config.Load(config.Options{FlagPath: configPath})
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
		}
		newEntry, found := findProject(eff.Projects, key)
		if !found {
			return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: "project not found after write"})
		}

		changed, _ := projectDiff(oldEntry, newEntry)
		if changed == nil {
			changed = []string{}
		}
		if _, err := registry.ApplyOne(c.Context(), st, newEntry, false); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
		}

		// D4: items mirrored from the old repository must not be planned or
		// dispatched against the new one, so the change clears them in the same
		// request, after the store upsert and before the response.
		if slices.Contains(changed, "forge") || slices.Contains(changed, "repo") {
			fresh, err := st.ProjectByKey(c.Context(), key)
			if err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
			}
			if fresh != nil {
				if err := st.ClearItems(c.Context(), fresh.ID); err != nil {
					return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
				}
			}
		}

		if len(changed) > 0 {
			if _, err := st.AppendEvent(c.Context(), store.Event{
				Actor: "board", Kind: "project.edit", Subject: "project:" + key,
				Detail: diffDetail(oldEntry, newEntry, changed),
			}); err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
			}
		}
		log.Printf("projects: edit key=%s changed=%s", key, strings.Join(changed, ","))
		wake(waker)

		return c.JSON(EditProjectResponse{
			Project:  projectWithRow(c.Context(), st, newEntry, "both"),
			Changed:  changed,
			Warnings: warningsFor(eff.Warnings, newEntry),
		})
	}
}

// setProjectOnHold answers PUT /api/projects/{key}/onhold: the DB-owned pause
// (design D5). It never writes the config file — `enabled` is the flag every
// upsert resets, `onhold` is the one that survives the boot pass.
func setProjectOnHold(st *store.Store, configPath string, waker *mirror.Waker) fiber.Handler {
	return func(c fiber.Ctx) error {
		key := c.Params("key")
		var req OnHoldRequest
		if err := c.Bind().JSON(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{Error: "malformed JSON"})
		}
		onhold, ok := req.OnHold.(bool)
		if !ok {
			return c.Status(fiber.StatusUnprocessableEntity).JSON(ErrorResponse{
				Error:  "validation failed",
				Errors: map[string]string{"onhold": "onhold must be a boolean"},
			})
		}
		row, err := st.ProjectByKey(c.Context(), key)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
		}
		if row == nil {
			return c.Status(fiber.StatusNotFound).JSON(ErrorResponse{
				Error: fmt.Sprintf("project %q is not registered in the store; the boot pass creates the row the pause sets", key),
			})
		}
		if _, err := st.SetProjectOnHold(c.Context(), row.ID, onhold); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
		}
		if _, err := st.AppendEvent(c.Context(), store.Event{
			Actor: "board", Kind: "project.onhold", Subject: "project:" + key,
			Detail: fmt.Sprintf("onhold %t → %t", row.OnHold, onhold),
		}); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
		}
		log.Printf("projects: onhold key=%s onhold=%t", key, onhold)
		wake(waker)
		view, err := loadMergedView(c.Context(), st, configPath)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
		}
		return c.JSON(EditProjectResponse{Project: view.render(key)})
	}
}

// deleteProject answers DELETE /api/projects/{key}?confirm=: D1's order —
// refuse on a live dispatch, pause, remove the file entry, delete the rows —
// because that is the only order whose half-finished state is safe. Every step
// is idempotent, so a retry after a failure resumes instead of starting over.
func deleteProject(st *store.Store, configPath string, remove func(path, key string, expected *[32]byte) (configfile.Outcome, error), waker *mirror.Waker) fiber.Handler {
	return func(c fiber.Ctx) error {
		if configPath == "" {
			return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: "config.yaml path is not configured"})
		}
		key := c.Params("key")
		// The typed confirmation is the guard against deleting the wrong row.
		if confirm := c.Query("confirm"); confirm != key {
			return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
				Error: "confirm must equal the key to delete",
			})
		}

		view, err := loadMergedView(c.Context(), st, configPath)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
		}
		_, inFile := view.entry(key)
		row, inStore := view.rows[key]

		// D9's replay rule: a delete that already happened is not an error the
		// client should have to distinguish from a typo when it carries the
		// right confirm.
		var counts DeleteCounts
		if inStore {
			if count, ok := view.counts[row.ID]; ok {
				counts = DeleteCounts{Items: count.Items, Dispatches: count.Dispatches, Verdicts: count.Verdicts}
			}
		}

		if inStore {
			if refusal, ok, err := liveDispatchRefusal(c.Context(), st, row.ID); err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
			} else if ok {
				return c.Status(fiber.StatusConflict).JSON(ErrorResponse{Error: refusal})
			}
			// Step 1, the pause: the act loop skips an onhold project, so a
			// failure later in this request cannot leave it planning work.
			if _, err := st.SetProjectOnHold(c.Context(), row.ID, true); err != nil {
				return stepFailure(c, "pause", key, err)
			}
		}

		// Step 2, the file: the entry must go before the rows, or the next
		// boot or --apply-config upserts the project back (design D1).
		if inFile {
			data, err := os.ReadFile(configPath)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
			}
			expected := sha256.Sum256(data)
			if _, err := remove(configPath, key, &expected); err != nil && !errors.Is(err, configfile.ErrNotFound) {
				if errors.Is(err, configfile.ErrStaleRead) {
					return c.Status(fiber.StatusConflict).JSON(ErrorResponse{
						Error: fmt.Sprintf("%s changed on disk; reload and retry", filepath.Base(configPath)),
					})
				}
				return stepFailure(c, "config file", key, err)
			}
		}

		// Step 3, the rows: one transaction over the project's families. Event
		// rows survive it, by design (D6).
		if inStore {
			if err := st.DeleteProject(c.Context(), row.ID); err != nil {
				return stepFailure(c, "store rows", key, err)
			}
		}

		if _, err := st.AppendEvent(c.Context(), store.Event{
			Actor: "board", Kind: "project.delete", Subject: "project:" + key,
			Detail: fmt.Sprintf("items=%d dispatches=%d verdicts=%d", counts.Items, counts.Dispatches, counts.Verdicts),
		}); err != nil {
			return stepFailure(c, "audit", key, err)
		}
		log.Printf("projects: delete key=%s items=%d dispatches=%d verdicts=%d", key, counts.Items, counts.Dispatches, counts.Verdicts)
		wake(waker)

		return c.JSON(DeleteProjectResponse{Deleted: counts})
	}
}

// stepFailure answers 500 naming the step that failed. D1's contract: the
// project is already paused, so the half-finished state is safe, and the same
// request retried completes it.
func stepFailure(c fiber.Ctx, step, key string, err error) error {
	return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
		Error: fmt.Sprintf("delete of %q stopped at the %s step: %v", key, step, err),
	})
}

// liveDispatchRefusal is the operator's instruction, not just a status: it
// names the dispatch, the item and the branch, and the next step. ok is false
// when no dispatch is live.
func liveDispatchRefusal(ctx context.Context, st *store.Store, projectID int64) (string, bool, error) {
	id, live, err := st.LiveDispatch(ctx, projectID)
	if err != nil || !live {
		return "", false, err
	}
	dispatches, err := st.DispatchesForProject(ctx, projectID)
	if err != nil {
		return "", false, err
	}
	for _, dispatch := range dispatches {
		if dispatch.ID != id {
			continue
		}
		return fmt.Sprintf("dispatch %d on item #%d (branch %s) is live; pause the project, wait for the row to end, then retry",
			dispatch.ID, dispatch.ItemNumber, dispatch.Branch), true, nil
	}
	return fmt.Sprintf("dispatch %d is live; pause the project, wait for the row to end, then retry", id), true, nil
}

// requestProject is the wire shape as the loader's config.Project.
func requestProject(req ProjectRequest) config.Project {
	project := config.Project{
		Key:            req.Key,
		Name:           req.Name,
		Forge:          req.Forge,
		Repo:           req.Repo,
		RepoURL:        req.RepoURL,
		ForgeHost:      req.ForgeHost,
		DefaultBranch:  req.DefaultBranch,
		Rigs:           rigRefsFromNames(req.Rigs),
		RigTemplate:    req.RigTemplate,
		ConcurrencyCap: req.ConcurrencyCap,
		GateCmd:        req.GateCmd,
		Labels:         req.Labels,
		OnHold:         req.OnHold,
		CultureFile:    req.CultureFile,
		VerdictRelay:   relayFromRequest(req.RelayMode, req.RelaySeat),
		Reviewer:       req.Reviewer,
		EpicSerial:     req.EpicSerial,
	}
	project.Paths.Main = req.Paths.Main
	project.Paths.Worktrees = req.Paths.Worktrees
	return project
}

// relayFromRequest is the API's relay pair as the file's block. An empty pair
// is an explicit off, written as an empty block rather than no block at all:
// a whole-definition PUT states every field, and "absent block leaves the row
// alone" is the rule that lets a hand-edited file keep a board-set relay — so
// the board's own "off" has to be a statement, or it could never clear one.
// The rest of the rule — the two modes, and a seat required whenever a mode is
// set — is ValidateProject's, so an edit cannot write a pair the loader would
// refuse at the next boot.
func relayFromRequest(mode, seat string) *config.VerdictRelay {
	return &config.VerdictRelay{Mode: strings.TrimSpace(mode), Seat: strings.TrimSpace(seat)}
}

// writtenProject is the config-file shape of a validated definition, the same
// conversion the create route has always made.
func writtenProject(project config.Project) configfile.Project {
	written := configfile.Project{
		Key:            project.Key,
		Name:           project.Name,
		Forge:          project.Forge,
		Repo:           project.Repo,
		RepoURL:        project.RepoURL,
		ForgeHost:      project.ForgeHost,
		DefaultBranch:  project.DefaultBranch,
		Rigs:           project.Rigs.Names(),
		RigTemplate:    project.RigTemplate,
		ConcurrencyCap: project.ConcurrencyCap,
		GateCmd:        project.GateCmd,
		Labels:         project.Labels,
		OnHold:         project.OnHold,
		CultureFile:    project.CultureFile,
	}
	if project.VerdictRelay != nil {
		written.VerdictRelay = &configfile.VerdictRelay{Mode: project.VerdictRelay.Mode, Seat: project.VerdictRelay.Seat}
	}
	// The default is what an absent key means, so a whole-definition write
	// omits it and the file stays as the operator wrote it.
	if project.Reviewer != config.DefaultReviewer {
		written.Reviewer = project.Reviewer
	}
	if project.EpicSerial != nil && !*project.EpicSerial {
		written.EpicSerial = project.EpicSerial
	}
	written.Paths.Main = project.Paths.Main
	written.Paths.Worktrees = project.Paths.Worktrees
	return written
}

// projectDiff compares two file entries on the fields the API carries and
// classifies each as identity (frozen while a dispatch is live, design D3) or
// harmless. repo_url and forge_host are identity (forge-host design D3): the
// first drives clone, the second drives the forge API the merge gate talks to,
// and a change under a live dispatch splits what the builder built from where
// the gate judges it. Fields the API does not carry — runtime, lifecycle —
// pass through the write unchanged and are never in the diff.
func projectDiff(oldEntry, newEntry config.Project) (changed, identity []string) {
	add := func(name string, isIdentity bool, equal bool) {
		if equal {
			return
		}
		changed = append(changed, name)
		if isIdentity {
			identity = append(identity, name)
		}
	}
	add("name", false, oldEntry.Name == newEntry.Name)
	add("forge", true, oldEntry.Forge == newEntry.Forge)
	add("repo", true, oldEntry.Repo == newEntry.Repo)
	add("repo_url", true, oldEntry.RepoURL == newEntry.RepoURL)
	add("forge_host", true, oldEntry.ForgeHost == newEntry.ForgeHost)
	add("default_branch", true, oldEntry.DefaultBranch == newEntry.DefaultBranch)
	add("rigs", true, slices.Equal(oldEntry.Rigs.Names(), newEntry.Rigs.Names()))
	add("concurrency_cap", false, oldEntry.ConcurrencyCap == newEntry.ConcurrencyCap)
	add("rig_template", false, oldEntry.RigTemplate == newEntry.RigTemplate)
	add("gate_cmd", false, oldEntry.GateCmd == newEntry.GateCmd)
	add("labels", false, maps.Equal(oldEntry.Labels, newEntry.Labels))
	add("culture_file", false, oldEntry.CultureFile == newEntry.CultureFile)
	add("reviewer", false, oldEntry.Reviewer == newEntry.Reviewer)
	add("epic_serial", false, epicSerialOf(oldEntry) == epicSerialOf(newEntry))
	add("relay_mode", false, relayOf(oldEntry).Mode == relayOf(newEntry).Mode)
	add("relay_seat", false, relayOf(oldEntry).Seat == relayOf(newEntry).Seat)
	add("paths.main", true, oldEntry.Paths.Main == newEntry.Paths.Main)
	add("paths.worktrees", true, oldEntry.Paths.Worktrees == newEntry.Paths.Worktrees)
	return changed, identity
}

// relayOf reads a project's relay block as a value, so a nil block and an empty
// one compare equal — both are off.
func relayOf(project config.Project) config.VerdictRelay {
	if project.VerdictRelay == nil {
		return config.VerdictRelay{}
	}
	return *project.VerdictRelay
}

// diffDetail is the audit row's detail: the changed fields with their old and
// new values, `field old → new` (design D10).
func diffDetail(oldEntry, newEntry config.Project, changed []string) string {
	oldValues := map[string]any{
		"name": oldEntry.Name, "forge": oldEntry.Forge, "repo": oldEntry.Repo,
		"repo_url": oldEntry.RepoURL, "forge_host": oldEntry.ForgeHost,
		"default_branch": oldEntry.DefaultBranch, "rigs": oldEntry.Rigs.Names(),
		"concurrency_cap": oldEntry.ConcurrencyCap, "rig_template": oldEntry.RigTemplate,
		"gate_cmd": oldEntry.GateCmd, "labels": oldEntry.Labels,
		"culture_file": oldEntry.CultureFile,
		"reviewer":     oldEntry.Reviewer,
		"epic_serial":  epicSerialOf(oldEntry),
		"relay_mode":   relayOf(oldEntry).Mode, "relay_seat": relayOf(oldEntry).Seat,
		"paths.main": oldEntry.Paths.Main, "paths.worktrees": oldEntry.Paths.Worktrees,
	}
	newValues := map[string]any{
		"name": newEntry.Name, "forge": newEntry.Forge, "repo": newEntry.Repo,
		"repo_url": newEntry.RepoURL, "forge_host": newEntry.ForgeHost,
		"default_branch": newEntry.DefaultBranch, "rigs": newEntry.Rigs.Names(),
		"concurrency_cap": newEntry.ConcurrencyCap, "rig_template": newEntry.RigTemplate,
		"gate_cmd": newEntry.GateCmd, "labels": newEntry.Labels,
		"culture_file": newEntry.CultureFile,
		"reviewer":     newEntry.Reviewer,
		"epic_serial":  epicSerialOf(newEntry),
		"relay_mode":   relayOf(newEntry).Mode, "relay_seat": relayOf(newEntry).Seat,
		"paths.main": newEntry.Paths.Main, "paths.worktrees": newEntry.Paths.Worktrees,
	}
	parts := make([]string, 0, len(changed))
	for _, field := range changed {
		parts = append(parts, fmt.Sprintf("%s %v → %v", field, oldValues[field], newValues[field]))
	}
	return strings.Join(parts, "; ")
}

// projectWithRow answers the stored project as the merged view renders it: the
// file's resolved definition plus the store's enabled/onhold and counts.
func projectWithRow(ctx context.Context, st *store.Store, project config.Project, source string) ProjectResponse {
	out := projectResponse(project, source)
	row, err := st.ProjectByKey(ctx, project.Key)
	if err != nil || row == nil {
		return out
	}
	out.Enabled = row.Enabled
	out.OnHold = row.OnHold
	if counts, err := st.ProjectCounts(ctx); err == nil {
		if count, ok := counts[row.ID]; ok {
			out.Items, out.Dispatches, out.LiveDispatch = count.Items, count.Dispatches, count.LiveDispatch
		}
	}
	return out
}

// validationFailure answers 422 with one message per field.
func validationFailure(c fiber.Ctx, err error) error {
	fields := map[string]string{}
	var invalid *config.ValidationError
	if errors.As(err, &invalid) {
		fields = invalid.Fields()
	}
	if len(fields) == 0 {
		fields = map[string]string{"config": err.Error()}
	}
	return c.Status(fiber.StatusUnprocessableEntity).JSON(ErrorResponse{
		Error: "validation failed", Errors: fields,
	})
}

// writeFailure maps a refused write onto the statuses design §5 names.
//
// Only two refusals belong to the client: a definition the file cannot hold
// (422, one message per field) and a key the file already holds or a file that
// moved under the writer (409). Everything else — a read-only directory, a full
// disk, a vanished path — is a server-side failure the client cannot fix by
// changing its request, so it answers 500 with the error, never a 409 that
// sends the operator looking at their own body.
func writeFailure(c fiber.Ctx, configPath string, err error) error {
	var invalid *config.ValidationError
	if errors.As(err, &invalid) {
		return validationFailure(c, err)
	}
	switch {
	case errors.Is(err, configfile.ErrStaleRead):
		return c.Status(fiber.StatusConflict).JSON(ErrorResponse{
			Error: fmt.Sprintf("%s changed on disk; reload and retry", filepath.Base(configPath)),
		})
	case errors.Is(err, configfile.ErrDuplicateKey):
		return c.Status(fiber.StatusConflict).JSON(ErrorResponse{Error: err.Error()})
	case errors.Is(err, configfile.ErrNotFound):
		return c.Status(fiber.StatusNotFound).JSON(ErrorResponse{Error: err.Error()})
	default:
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
	}
}

// warningsFor is what the dialog shows after a 201: the loader's warnings about
// this project plus the ones ProjectWarnings derives, deduplicated because a
// missing paths.main arrives down both routes.
func warningsFor(all []string, project config.Project) []string {
	seen := make(map[string]bool, len(all)+1)
	out := make([]string, 0, len(all)+1)
	add := func(warning string) {
		if seen[warning] {
			return
		}
		seen[warning] = true
		out = append(out, warning)
	}
	for _, warning := range config.ProjectWarnings(project) {
		add(warning)
	}
	quoted := fmt.Sprintf("%q", project.Key)
	for _, warning := range all {
		if strings.Contains(warning, quoted) {
			add(warning)
		}
	}
	return out
}

func homeDir() string {
	home, _ := os.UserHomeDir()
	return home
}

func findProject(projects []config.Project, key string) (config.Project, bool) {
	for _, project := range projects {
		if project.Key == key {
			return project, true
		}
	}
	return config.Project{}, false
}

func projectResponse(project config.Project, source string) ProjectResponse {
	out := ProjectResponse{
		Key:            project.Key,
		Name:           project.Name,
		Forge:          project.Forge,
		Repo:           project.Repo,
		RepoURL:        project.RepoURL,
		ForgeHost:      project.ForgeHost,
		DefaultBranch:  project.DefaultBranch,
		Rigs:           project.Rigs.Names(),
		RigTemplate:    project.RigTemplate,
		ConcurrencyCap: project.ConcurrencyCap,
		GateCmd:        project.GateCmd,
		Labels:         project.Labels,
		Enabled:        true,
		OnHold:         project.OnHold != nil && *project.OnHold,
		CultureFile:    project.CultureFile,
		Source:         source,
	}
	if project.VerdictRelay != nil {
		out.RelayMode, out.RelaySeat = project.VerdictRelay.Mode, project.VerdictRelay.Seat
	}
	out.Reviewer = project.Reviewer
	out.EpicSerial = project.EpicSerial
	out.Paths.Main = project.Paths.Main
	out.Paths.Worktrees = project.Paths.Worktrees
	return out
}

// epicSerialOf reads the epic-serial flag as a value: nil is the default (true),
// so an absent key and an explicit true compare equal.
func epicSerialOf(project config.Project) bool {
	return project.EpicSerial == nil || *project.EpicSerial
}

func epicSerialPtr(value bool) *bool { return &value }
