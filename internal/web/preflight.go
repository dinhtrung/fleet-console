// Preflight, setup and runtime endpoints.
//
// Three reads and one write:
//
//	GET  /api/projects/{key}/preflight  the four checks for a registered project
//	POST /api/projects/preflight        the same checks against a draft body
//	POST /api/projects/{key}/setup      the allowlisted steps, in canonical order
//	GET  /api/runtimes                  support, credential presence, login hint
//
// Nothing here accepts a credential, returns one, or offers a field to type one:
// the runtime block carries a boolean and a command the operator runs on the box.
package web

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/dinhtrung/fleet-console/internal/config"
	"github.com/dinhtrung/fleet-console/internal/preflight"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/setup"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// PreflightResponse is the report shape from design.md §1. It mirrors
// preflight.Report field for field rather than serialising the domain type, so
// the wire shape can carry json tags and the domain type stays free of them.
type PreflightResponse struct {
	Project   string          `json:"project"`
	Checks    []CheckResponse `json:"checks"`
	Ready     bool            `json:"ready"`
	Runtime   RuntimeResponse `json:"runtime"`
	CheckedAt time.Time       `json:"checked_at"`
}

// CheckResponse is one check: its verdict, the fact found and how to fix it.
type CheckResponse struct {
	ID     string      `json:"id"`
	Label  string      `json:"label"`
	State  string      `json:"state"`
	Detail string      `json:"detail"`
	Fix    FixResponse `json:"fix"`
}

// FixResponse carries the pasteable command and, when the app may perform the
// repair itself, the allowlisted step id.
type FixResponse struct {
	Command string `json:"command"`
	Step    string `json:"step"`
	Why     string `json:"why"`
}

// RuntimeResponse is one runtime as this host sees it. AuthPresent is a boolean
// on purpose: a credential value would be a leak, and no field here can hold one.
type RuntimeResponse struct {
	ID          string `json:"id"`
	Supported   bool   `json:"supported"`
	AuthPresent bool   `json:"auth_present"`
	LoginHint   string `json:"login_hint"`
	LoginDoc    string `json:"login_doc"`
}

// RuntimesResponse is the list the client's picker is driven by, so the four
// values live on the server and the client never hardcodes them.
type RuntimesResponse struct {
	Runtimes []RuntimeResponse `json:"runtimes"`
}

// SetupResponse is one entry per step that ran, in order.
type SetupResponse struct {
	Project string          `json:"project"`
	Steps   []SetupStepJSON `json:"steps"`
}

// SetupStepJSON is a setup.Result on the wire.
type SetupStepJSON struct {
	Step    string `json:"step"`
	State   string `json:"state"`
	Command string `json:"command"`
	Output  string `json:"output"`
}

// draftRequest is the body of a draft preflight: the fields the create dialog
// has so far. Only the four facts the checks read are taken; everything else in
// the real create body is irrelevant before the row exists.
type draftRequest struct {
	Key     string   `json:"key"`
	Name    string   `json:"name"`
	Forge   string   `json:"forge"`
	Repo    string   `json:"repo"`
	Rigs    []string `json:"rigs"`
	Runtime string   `json:"runtime"`
	Paths   struct {
		Main      string `json:"main"`
		Worktrees string `json:"worktrees"`
	} `json:"paths"`
}

// setupRequest is the body of a setup run: an optional subset of step ids.
type setupRequest struct {
	Steps []string `json:"steps"`
}

func preflightResponse(report preflight.Report) PreflightResponse {
	out := PreflightResponse{
		Project:   report.Project,
		Ready:     report.Ready,
		CheckedAt: report.CheckedAt,
		Runtime:   runtimeResponse(report.Runtime),
		Checks:    make([]CheckResponse, 0, len(report.Checks)),
	}
	for _, check := range report.Checks {
		out.Checks = append(out.Checks, CheckResponse{
			ID: check.ID, Label: check.Label, State: string(check.State), Detail: check.Detail,
			Fix: FixResponse{Command: check.Fix.Command, Step: check.Fix.Step, Why: check.Fix.Why},
		})
	}
	return out
}

func runtimeResponse(runtime preflight.Runtime) RuntimeResponse {
	return RuntimeResponse{
		ID: runtime.ID, Supported: runtime.Supported, AuthPresent: runtime.AuthPresent,
		LoginHint: runtime.LoginHint, LoginDoc: runtime.LoginDoc,
	}
}

// RegisterPreflightRoutes wires the four endpoints into the authenticated group.
func RegisterPreflightRoutes(api fiber.Router, cfg Config, configPath string) {
	api.Get("/projects/:key/preflight", preflightProject(cfg, configPath))
	api.Post("/projects/preflight", preflightDraft(cfg, configPath))
	api.Post("/projects/:key/setup", runSetup(cfg, configPath))
	api.Get("/runtimes", listRuntimes(cfg, configPath))
}

// osGetenv is the process environment. A test replaces it through Config.Env.
func osGetenv(key string) string { return envLookup(key) }

// envLookup is a package variable so a test can pin the environment without
// touching the process.
var envLookup = os.Getenv

// loadEffective reads the config the service was started with. Every handler
// below needs it: the checks read a project row out of the file, and the setup
// chain renders the rig from it.
func loadEffective(configPath string) (*config.Effective, error) {
	if strings.TrimSpace(configPath) == "" {
		return nil, errors.New("no config file: the service was started without --config")
	}
	return config.Load(config.Options{FlagPath: configPath, Env: osGetenv})
}

func preflightProject(cfg Config, configPath string) fiber.Handler {
	return func(c fiber.Ctx) error {
		key := c.Params("key")
		eff, err := loadEffective(configPath)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		project, ok := findConfigProject(eff.Projects, key)
		if !ok {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": fmt.Sprintf("project %q is not registered", key)})
		}
		p := preflight.FromConfig(project)
		// The note-write probe aims at an item the mirror already has: the
		// newest open one. An empty mirror leaves the number zero, and the
		// check answers unknown rather than aiming at nothing.
		p.ProbeItemNumber = newestOpenItem(c.Context(), cfg.Store, project.Key)
		report := preflight.RunWith(c.Context(), p, *eff, preflight.Deps{Forge: cfg.PreflightForge})
		return c.JSON(preflightResponse(report))
	}
}

// newestOpenItem is the highest-numbered open item the mirror holds for the
// project, or 0 when the mirror is empty, the project has no row or the store
// is unavailable. Zero means "aim at nothing", which the forge_write check
// turns into an honest unknown.
func newestOpenItem(ctx context.Context, st *store.Store, key string) int {
	if st == nil {
		return 0
	}
	row, err := st.ProjectByKey(ctx, key)
	if err != nil || row == nil {
		return 0
	}
	items, err := st.Items(ctx)
	if err != nil {
		return 0
	}
	newest := 0
	for _, item := range items {
		if item.ProjectID == row.ID && strings.EqualFold(strings.TrimSpace(item.State), "open") && item.Number > newest {
			newest = item.Number
		}
	}
	return newest
}

// preflightDraft checks a body that is not a project row yet. `rig` answers
// unknown with its reason, because the rig is created with the project.
func preflightDraft(cfg Config, configPath string) fiber.Handler {
	return func(c fiber.Ctx) error {
		var in draftRequest
		if err := c.Bind().Body(&in); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid JSON body"})
		}
		eff, err := loadEffective(configPath)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		rigName := ""
		if len(in.Rigs) > 0 {
			rigName = strings.TrimSpace(in.Rigs[0])
		}
		project := preflight.Project{
			Key: in.Key, Rig: rigName, Forge: in.Forge, Repo: in.Repo,
			MainPath: in.Paths.Main, Runtime: in.Runtime,
			Worktrees: in.Paths.Worktrees,
			Draft:     true,
		}
		// A draft has no row, so the mirror holds nothing to aim the note probe
		// at; the change-request probe alone answers what a token may do.
		report := preflight.RunWith(c.Context(), project, *eff, preflight.Deps{Forge: cfg.PreflightForge})
		return c.JSON(preflightResponse(report))
	}
}

func listRuntimes(cfg Config, configPath string) fiber.Handler {
	return func(c fiber.Ctx) error {
		eff, err := loadEffective(configPath)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		out := RuntimesResponse{Runtimes: make([]RuntimeResponse, 0, len(preflight.Runtimes))}
		for _, id := range preflight.Runtimes {
			// A probe per runtime. The Deps are narrowed so a single probe costs
			// no network and no rig call: only the runtime block is read, and
			// the other checks are not run at all because Draft skips the rig
			// and the URL is empty.
			project := preflight.Project{Key: "probe", Runtime: id, Draft: true}
			report := preflight.RunWith(c.Context(), project, *eff, preflight.Deps{
				Git: func(context.Context, ...string) (string, string, error) {
					return "", "", errors.New("probe")
				},
				Seats: func(context.Context, string) ([]rig.Seat, error) { return nil, nil },
			})
			out.Runtimes = append(out.Runtimes, runtimeResponse(report.Runtime))
		}
		return c.JSON(out)
	}
}

func runSetup(cfg Config, configPath string) fiber.Handler {
	return func(c fiber.Ctx) error {
		key := c.Params("key")
		var in setupRequest
		if len(c.Body()) > 0 {
			if err := c.Bind().Body(&in); err != nil {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid JSON body"})
			}
		}
		eff, err := loadEffective(configPath)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		project, ok := findConfigProject(eff.Projects, key)
		if !ok {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": fmt.Sprintf("project %q is not registered", key)})
		}
		// The step list is the caller's to get wrong, so it is validated before anything
		// about this service's configuration is examined: a bad id is a 422 whatever the
		// host looks like.
		if bad := unknownStep(in.Steps); bad != "" {
			return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
				"error": fmt.Sprintf("setup: %q is not one of %s", bad, strings.Join(setup.Order, ", ")),
			})
		}
		runner := &setup.Runner{
			Store: cfg.Store,
			Rig:   setupRig{adapter: cfg.Rig},
			Exec:  cfg.SetupExec,
			Now:   time.Now,
		}
		if runner.Exec == nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "setup is not configured on this service"})
		}
		input := setupInput(*eff, project)
		results, err := runner.Run(c.Context(), input, in.Steps)
		if err != nil {
			// An unknown step id is the caller's error, not a server fault.
			return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"error": err.Error()})
		}
		out := SetupResponse{Project: project.Key, Steps: make([]SetupStepJSON, 0, len(results))}
		for _, result := range results {
			out.Steps = append(out.Steps, SetupStepJSON{
				Step: result.Step, State: string(result.State), Command: result.Command, Output: result.Output,
			})
		}
		return c.JSON(out)
	}
}

// unknownStep returns the first requested step that is not allowlisted, or "" when all are.
// Checked here rather than in internal/setup because the id is the caller's input: refusing it
// must not depend on whether this host happens to have a runner configured.
func unknownStep(steps []string) string {
	for _, step := range steps {
		step = strings.TrimSpace(step)
		if step == "" {
			continue
		}
		known := false
		for _, allowed := range setup.Order {
			if step == allowed {
				known = true
				break
			}
		}
		if !known {
			return step
		}
	}
	return ""
}

// setupRig adapts the rig CLI to the three calls the setup chain makes.
type setupRig struct {
	adapter *rig.Adapter
}

func (s setupRig) LiveSeats(ctx context.Context, rigName string) (int, error) {
	if s.adapter == nil {
		return 0, errors.New("no rig adapter is configured")
	}
	seats, err := s.adapter.SeatsForRig(ctx, rigName)
	if err != nil {
		return 0, err
	}
	return len(seats), nil
}

func (s setupRig) SpecsSync(ctx context.Context) error {
	if s.adapter == nil {
		return errors.New("no rig adapter is configured")
	}
	_, err := s.adapter.SpecsSync(ctx)
	return err
}

func (s setupRig) Up(ctx context.Context, specPath string) error {
	if s.adapter == nil {
		return errors.New("no rig adapter is configured")
	}
	report, err := s.adapter.Up(ctx, specPath)
	if err != nil {
		return err
	}
	if len(report.Errors) > 0 {
		return fmt.Errorf("%s", strings.Join(report.Errors, "; "))
	}
	return nil
}

// setupInput resolves the chain's inputs from the project row and the config.
//
// The row does not carry a model, a provider credential or a culture path, so
// the render inputs come from the same environment the service runs in: the
// runtime's provider variable and the rig's culture file, both defaulted. When
// one is missing RenderRig refuses and apply_rig reports that refusal verbatim
// as its output rather than inventing a value.
func setupInput(eff config.Effective, project config.Project) setup.Input {
	rigName := strings.TrimSpace(project.Rig)
	if rigName == "" && len(project.Rigs) > 0 {
		rigName = strings.TrimSpace(project.Rigs[0].Name)
	}
	builders := project.ConcurrencyCap
	if builders < 1 {
		builders = 1
	}
	return setup.Input{
		BaseDir: project.Paths.Main,
		RepoURL: repoURLFor(project),
		RigName: rigName,
		Home:    homeDir(),
		Project: rig.Project{
			Key:           project.Key,
			Rig:           rigName,
			Name:          project.Name,
			Summary:       project.Name,
			RepoPath:      project.Paths.Main,
			WorktreeRoot:  project.Paths.Worktrees,
			Builders:      builders,
			Runtime:       project.Runtime,
			ProviderEnv:   providerEnvFor(project.Runtime),
			CulturePath:   project.CultureFile,
			BuilderModel:  defaultModel(),
			ReviewerModel: defaultModel(),
		},
		Culture:   project.CultureFile,
		RigBinary: eff.RigBinary,
	}
}

// repoURLFor is the clone URL for a project row: the configured one when the row
// carries it, otherwise the web URL its forge and repository imply.
func repoURLFor(project config.Project) string {
	if url := strings.TrimSpace(project.RepoURL); url != "" {
		return url
	}
	repo := strings.TrimSuffix(strings.TrimSpace(project.Repo), ".git")
	switch strings.ToLower(strings.TrimSpace(project.Forge)) {
	case "github":
		return "https://github.com/" + repo + ".git"
	case "gitlab":
		return "https://gitlab.com/" + repo + ".git"
	default:
		return ""
	}
}

// providerEnvNames maps a runtime to the environment variable its seats get.
// It is a NAME, never a value: nothing here reads or returns a credential, and
// the rig spec carries the name so the seat inherits it from the service's own
// environment.
var providerEnvNames = map[string]string{
	"pi":     "PI_API_KEY",
	"omp":    "OMP_API_KEY",
	"codex":  "OPENAI_API_KEY",
	"claude": "ANTHROPIC_API_KEY",
}

// providerEnvFor names the variable the runtime reads. An unknown runtime gets
// the neutral name; RenderRig refuses the runtime itself, so this is only ever
// reached for one of the four.
func providerEnvFor(runtimeID string) string {
	if name, ok := providerEnvNames[strings.ToLower(strings.TrimSpace(runtimeID))]; ok {
		return name
	}
	return "OPENAI_API_KEY"
}

func defaultModel() string {
	if model := strings.TrimSpace(osGetenv("FLEET_CONSOLE_MODEL")); model != "" {
		return model
	}
	return "litellm/coder"
}

// findConfigProject returns the row with this key.
func findConfigProject(projects []config.Project, key string) (config.Project, bool) {
	for _, project := range projects {
		if project.Key == key {
			return project, true
		}
	}
	return config.Project{}, false
}
