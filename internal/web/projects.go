package web

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/dinhtrung/fleet-console/internal/config"
	"github.com/dinhtrung/fleet-console/internal/configfile"
	"github.com/dinhtrung/fleet-console/internal/store"
	"github.com/gofiber/fiber/v3"
)

// ProjectRequest is the shape the file uses (snake_case as in YAML).
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

// RegisterProjectsRoutes registers GET /api/projects and POST /api/projects.
func RegisterProjectsRoutes(api fiber.Router, cfg Config, configPath string) {
	api.Get("/projects", listProjects(cfg.Store, configPath))
	api.Post("/projects", createProject(cfg.Store, configPath))
}

func listProjects(st *store.Store, configPath string) fiber.Handler {
	return func(c fiber.Ctx) error {
		// Read from the config file to get source info
		eff, err := config.Load(config.Options{FlagPath: configPath})
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
		}

		projects := make([]ProjectResponse, 0, len(eff.Projects))
		for _, p := range eff.Projects {
			projects = append(projects, ProjectResponse{
				Key:            p.Key,
				Name:           p.Name,
				Forge:          p.Forge,
				Repo:           p.Repo,
				DefaultBranch:  p.DefaultBranch,
				Rigs:           p.Rigs,
				RigTemplate:    p.RigTemplate,
				ConcurrencyCap: p.ConcurrencyCap,
				GateCmd:        p.GateCmd,
				Labels:         p.Labels,
				OnHold:         p.OnHold,
				Paths: struct {
					Main      string `json:"main"`
					Worktrees string `json:"worktrees"`
				}{
					Main:      p.Paths.Main,
					Worktrees: p.Paths.Worktrees,
				},
				Source: eff.Sources["projects"],
			})
		}
		return c.JSON(ProjectsListResponse{Projects: projects})
	}
}

func createProject(st *store.Store, configPath string) fiber.Handler {
	return func(c fiber.Ctx) error {
		var req ProjectRequest
		if err := c.Bind().JSON(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{Error: "malformed JSON"})
		}

		// Convert to config.Project for validation
		proj := config.Project{
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
			Paths: struct {
				Main      string `yaml:"main"`
				Worktrees string `yaml:"worktrees"`
			}{
				Main:      req.Paths.Main,
				Worktrees: req.Paths.Worktrees,
			},
		}

		// Validate using the same rules the loader uses
		if err := config.ValidateProject(&proj, "", ""); err != nil {
			// Convert to field errors for 422
			errs := map[string]string{"": err.Error()}
			return c.Status(fiber.StatusUnprocessableEntity).JSON(ErrorResponse{Error: "validation failed", Errors: errs})
		}

		// Pre-check: if project key already exists in config, return 409 immediately
		effPre, err := config.Load(config.Options{FlagPath: configPath})
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
		}
		for _, p := range effPre.Projects {
			if p.Key == proj.Key {
				return c.Status(fiber.StatusConflict).JSON(ErrorResponse{Error: fmt.Sprintf("duplicate project key %q", proj.Key)})
			}
		}

		// Pre-check: if project key already exists in store, return 409 immediately
		existing, err := st.ProjectByKey(c.Context(), proj.Key)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
		}
		if existing != nil {
			return c.Status(fiber.StatusConflict).JSON(ErrorResponse{Error: fmt.Sprintf("duplicate project key %q", proj.Key)})
		}

		// Read file and compute hash for stale-read guard
		data, err := readFileHash(configPath)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
			}
			data = []byte("")
		}
		var h [32]byte
		h = sha256.Sum256(data)

		// Upsert to config file
		cfgProj := configfile.Project{
			Key:            proj.Key,
			Name:           proj.Name,
			Forge:          proj.Forge,
			Repo:           proj.Repo,
			DefaultBranch:  proj.DefaultBranch,
			Rigs:           proj.Rigs,
			RigTemplate:    proj.RigTemplate,
			ConcurrencyCap: proj.ConcurrencyCap,
			GateCmd:        proj.GateCmd,
			Labels:         proj.Labels,
			OnHold:         proj.OnHold,
			Paths: struct {
				Main      string `yaml:"main"`
				Worktrees string `yaml:"worktrees"`
			}{
				Main:      proj.Paths.Main,
				Worktrees: proj.Paths.Worktrees,
			},
		}

		outcome, err := configfile.UpsertProject(configPath, cfgProj, &h)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				// Directory doesn't exist
				return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
			}
			// Duplicate key or stale read -> 409
			return c.Status(fiber.StatusConflict).JSON(ErrorResponse{Error: err.Error()})
		}

		if outcome == configfile.OutcomeUnchanged {
			// Project already exists with identical content; reload and return success
			eff, err := config.Load(config.Options{FlagPath: configPath})
			if err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
			}
			var created config.Project
			found := false
			for _, p := range eff.Projects {
				if p.Key == proj.Key {
					created = p
					found = true
					break
				}
			}
			if !found {
				return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: "project not found after write"})
			}
			return c.Status(fiber.StatusOK).JSON(CreateProjectResponse{
				Project: ProjectResponse{
					Key:            created.Key,
					Name:           created.Name,
					Forge:          created.Forge,
					Repo:           created.Repo,
					DefaultBranch:  created.DefaultBranch,
					Rigs:           created.Rigs,
					RigTemplate:    created.RigTemplate,
					ConcurrencyCap: created.ConcurrencyCap,
					GateCmd:        created.GateCmd,
					Labels:         created.Labels,
					OnHold:         created.OnHold,
					Paths: struct {
						Main      string `json:"main"`
						Worktrees string `json:"worktrees"`
					}{
						Main:      created.Paths.Main,
						Worktrees: created.Paths.Worktrees,
					},
					Source: eff.Sources["projects"],
				},
				Warnings: eff.Warnings,
			})
		}

		// Reload effective config to get the project with resolved paths
		eff, err := config.Load(config.Options{FlagPath: configPath})
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
		}

		// Find the created project in the loaded config
		var created config.Project
		found := false
		for _, p := range eff.Projects {
			if p.Key == proj.Key {
				created = p
				found = true
				break
			}
		}
		if !found {
			return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: "project not found after write"})
		}

		// Upsert to store (same path as --apply-config uses)
		firstRig := ""
		if len(created.Rigs) > 0 {
			firstRig = created.Rigs[0]
		}
		_, err = st.CreateProject(c.Context(), store.ProjectInput{
			Key:            created.Key,
			Name:           created.Name,
			ForgeKind:      created.Forge,
			ForgeProject:   created.Repo,
			DefaultBranch:  created.DefaultBranch,
			Rig:            firstRig,
			Rigs:           created.Rigs,
			RigTemplate:    created.RigTemplate,
			ConcurrencyCap: created.ConcurrencyCap,
			Enabled:        !isOnHold(created.OnHold),
			GateCmd:        created.GateCmd,
		})
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed: project.key") {
				// Project already exists in store, which is fine for idempotency
			} else {
				return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
			}
		}

		return c.Status(fiber.StatusCreated).JSON(CreateProjectResponse{
			Project: ProjectResponse{
				Key:            created.Key,
				Name:           created.Name,
				Forge:          created.Forge,
				Repo:           created.Repo,
				DefaultBranch:  created.DefaultBranch,
				Rigs:           created.Rigs,
				RigTemplate:    created.RigTemplate,
				ConcurrencyCap: created.ConcurrencyCap,
				GateCmd:        created.GateCmd,
				Labels:         created.Labels,
				OnHold:         created.OnHold,
				Paths: struct {
					Main      string `json:"main"`
					Worktrees string `json:"worktrees"`
				}{
					Main:      created.Paths.Main,
					Worktrees: created.Paths.Worktrees,
				},
				Source: eff.Sources["projects"],
			},
			Warnings: eff.Warnings,
		})
	}
}

func isOnHold(onHold *bool) bool {
	if onHold == nil {
		return false
	}
	return *onHold
}

func readFileHash(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return data, nil
}