package web

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dinhtrung/fleet-console/internal/config"
	"github.com/dinhtrung/fleet-console/internal/configfile"
	"github.com/dinhtrung/fleet-console/internal/registry"
	"github.com/dinhtrung/fleet-console/internal/store"
	"github.com/gofiber/fiber/v3"
)

// ProjectRequest is the body of POST /api/projects: the shape the file uses,
// snake_case as the YAML tags spell it.
type ProjectRequest struct {
	Key            string            `json:"key"`
	Name           string            `json:"name"`
	Forge          string            `json:"forge"`
	Repo           string            `json:"repo"`
	DefaultBranch  string            `json:"default_branch"`
	Rigs           []string          `json:"rigs"`
	RigTemplate    string            `json:"rig_template"`
	ConcurrencyCap int               `json:"concurrency_cap"`
	GateCmd        string            `json:"gate_cmd"`
	Labels         map[string]string `json:"labels"`
	OnHold         *bool             `json:"onhold"`
	Paths          struct {
		Main      string `json:"main"`
		Worktrees string `json:"worktrees"`
	} `json:"paths"`
}

// ProjectResponse is one project as the board reads it back.
type ProjectResponse struct {
	Key            string            `json:"key"`
	Name           string            `json:"name"`
	Forge          string            `json:"forge"`
	Repo           string            `json:"repo"`
	DefaultBranch  string            `json:"default_branch"`
	Rigs           []string          `json:"rigs"`
	RigTemplate    string            `json:"rig_template"`
	ConcurrencyCap int               `json:"concurrency_cap"`
	GateCmd        string            `json:"gate_cmd"`
	Labels         map[string]string `json:"labels"`
	OnHold         *bool             `json:"onhold"`
	Paths          struct {
		Main      string `json:"main"`
		Worktrees string `json:"worktrees"`
	} `json:"paths"`
	Source string `json:"source"`
}

type ProjectsListResponse struct {
	Projects []ProjectResponse `json:"projects"`
}

type CreateProjectResponse struct {
	Project  ProjectResponse `json:"project"`
	Warnings []string        `json:"warnings"`
}

type ErrorResponse struct {
	Error  string            `json:"error"`
	Errors map[string]string `json:"errors,omitempty"`
}

// RegisterProjectsRoutes registers GET /api/projects and POST /api/projects
// inside the authenticated API group.
func RegisterProjectsRoutes(api fiber.Router, cfg Config, configPath string) {
	write := cfg.WriteProject
	if write == nil {
		write = configfile.UpsertProject
	}
	api.Get("/projects", listProjects(configPath))
	api.Post("/projects", createProject(cfg.Store, configPath, write))
}

func listProjects(configPath string) fiber.Handler {
	return func(c fiber.Ctx) error {
		eff, err := config.Load(config.Options{FlagPath: configPath})
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
		}
		projects := make([]ProjectResponse, 0, len(eff.Projects))
		for _, project := range eff.Projects {
			projects = append(projects, projectResponse(project, eff.Sources["projects"]))
		}
		return c.JSON(ProjectsListResponse{Projects: projects})
	}
}

// createProject validates one definition, writes the file, then upserts the
// store on the same path --apply-config uses. The order is the promise: a
// refused write leaves both the file and the store untouched.
func createProject(st *store.Store, configPath string, write func(string, configfile.Project, *[32]byte) (configfile.Outcome, error)) fiber.Handler {
	return func(c fiber.Ctx) error {
		if configPath == "" {
			return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: "config.yaml path is not configured"})
		}
		var req ProjectRequest
		if err := c.Bind().JSON(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{Error: "malformed JSON"})
		}

		dir := filepath.Dir(configPath)
		project := config.Project{
			Key:            req.Key,
			Name:           req.Name,
			Forge:          req.Forge,
			Repo:           req.Repo,
			DefaultBranch:  req.DefaultBranch,
			Rigs:           req.Rigs,
			RigTemplate:    req.RigTemplate,
			ConcurrencyCap: req.ConcurrencyCap,
			GateCmd:        req.GateCmd,
			Labels:         req.Labels,
			OnHold:         req.OnHold,
		}
		project.Paths.Main = req.Paths.Main
		project.Paths.Worktrees = req.Paths.Worktrees

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

		written := configfile.Project{
			Key:            project.Key,
			Name:           project.Name,
			Forge:          project.Forge,
			Repo:           project.Repo,
			DefaultBranch:  project.DefaultBranch,
			Rigs:           project.Rigs,
			RigTemplate:    project.RigTemplate,
			ConcurrencyCap: project.ConcurrencyCap,
			GateCmd:        project.GateCmd,
			Labels:         project.Labels,
			OnHold:         project.OnHold,
		}
		written.Paths.Main = project.Paths.Main
		written.Paths.Worktrees = project.Paths.Worktrees

		outcome, err := write(configPath, written, &expected)
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

		return c.Status(fiber.StatusCreated).JSON(CreateProjectResponse{
			Project:  projectResponse(created, eff.Sources["projects"]),
			Warnings: warningsFor(eff.Warnings, created),
		})
	}
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
		DefaultBranch:  project.DefaultBranch,
		Rigs:           project.Rigs,
		RigTemplate:    project.RigTemplate,
		ConcurrencyCap: project.ConcurrencyCap,
		GateCmd:        project.GateCmd,
		Labels:         project.Labels,
		OnHold:         project.OnHold,
		Source:         source,
	}
	out.Paths.Main = project.Paths.Main
	out.Paths.Worktrees = project.Paths.Worktrees
	return out
}
