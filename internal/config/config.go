// Package config loads fleet-console's settings from YAML with a strict schema,
// then resolves them with flag > env > file > default precedence. Nothing here
// logs or prints a token value.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/dinhtrung/fleet-console/internal/rig"
	"gopkg.in/yaml.v3"
)

// Duration is a YAML duration such as "10m".
type Duration time.Duration

// UnmarshalYAML parses a duration string.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.ScalarNode {
		return fmt.Errorf("duration must be a string")
	}
	parsed, err := time.ParseDuration(value.Value)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", value.Value, err)
	}
	*d = Duration(parsed)
	return nil
}

// Project is one project registry entry from the file.
type Project struct {
	Key           string `yaml:"key"`
	Name          string `yaml:"name"`
	Forge         string `yaml:"forge"`
	Repo          string `yaml:"repo"`
	RepoURL       string `yaml:"repo_url"`
	// ForgeHost names the host the forge API lives on (forge-host, design D1):
	// host[:port], no scheme, no path. Normalised by ValidateProject; resolved
	// by the package-level ForgeHost function. Unset keeps today's behaviour.
	ForgeHost     string `yaml:"forge_host"`
	DefaultBranch string `yaml:"default_branch"`
	Rig            string  `yaml:"rig,omitempty"`
	Rigs           RigRefs `yaml:"rigs"`
	RigTemplate    string  `yaml:"rig_template"`
	ConcurrencyCap int     `yaml:"concurrency_cap"`
	// Runtime is which agent runs this project's seats. One of pi, omp, codex
	// or claude; stored lowercase; omp when absent.
	Runtime string            `yaml:"runtime"`
	GateCmd string            `yaml:"gate_cmd"`
	Labels  map[string]string `yaml:"labels"`
	OnHold  *bool             `yaml:"onhold"`
	// Lifecycle is this project's per-project switch: `on` or `off`. An absent
	// key inherits rig_lifecycle.enabled; anything else is refused at load.
	Lifecycle string `yaml:"lifecycle,omitempty"`
	// CultureFile is where this project's rig culture lives. Unset means the
	// board derives it from the first rig; the loader resolves ~ here so the
	// running service never has to guess at a home directory.
	CultureFile string `yaml:"culture_file"`
	// VerdictRelay is the per-project verdict relay (verdict-relay, design D8).
	// It is a pointer so an absent block is distinguishable from an empty one:
	// both are off, but an empty block is still validated. Off means today's
	// behaviour — no relay rows, operator questions stay on human@kernel.
	VerdictRelay *VerdictRelay `yaml:"verdict_relay,omitempty"`
	// Reviewer is the project's reviewer member prefix (#384): review rows go
	// to "<reviewer>@<rig>" and the plan recognises one there. Unset is
	// DefaultReviewer, so a project that never names one keeps today's
	// behaviour; a rig whose reviewer is named anything else names it here
	// instead of waiting for the code to be edited.
	Reviewer string `yaml:"reviewer,omitempty"`
	Paths    struct {
		Main      string `yaml:"main"`
		Worktrees string `yaml:"worktrees"`
	} `yaml:"paths"`
}

// DefaultReviewer is the reviewer member prefix a project uses when its config
// names none (#384). It is the name every project had before the field existed.
const DefaultReviewer = "dev-review"

// Relay modes. The relay is off unless a project names one of these in its
// verdict_relay block; there is no default (D8 — an upgraded binary must not
// start creating rows to a seat that may not exist).
const (
	// RelayModeScribe routes a relayed verdict to a seat of the project's own
	// rig; operator questions keep going to human@kernel.
	RelayModeScribe = "scribe"
	// RelayModeOperator additionally routes the project's operator questions to
	// the configured seat instead of human@kernel.
	RelayModeOperator = "operator"
)

// VerdictRelay is the verdict_relay block. Seat is required whenever Mode is
// set — no suffixing, no default — and an empty block means off.
type VerdictRelay struct {
	Mode string `yaml:"mode"`
	Seat string `yaml:"seat"`
}

// File is the YAML shape. KnownFields rejects anything else.
type File struct {
	Listen    string `yaml:"listen"`
	Database  string `yaml:"database"`
	Token     string `yaml:"token"`
	TokenFile string `yaml:"token_file"`
	// WebhookSecretFile is the HMAC secret the forge delivery route verifies
	// against. A file next to the token, never inline: config files get copied
	// into tickets (design D3). Empty means the route is not registered.
	WebhookSecretFile string   `yaml:"webhook_secret_file"`
	RigBinary         string   `yaml:"rig_binary"`
	GhBinary          string   `yaml:"gh_binary"`
	GlabBinary        string   `yaml:"glab_binary"`
	TickInterval      Duration `yaml:"tick_interval"`
	NotifyInterval    Duration `yaml:"notify_interval"`
	// HousekeepingInterval is the reclaim sweep's cadence. Zero means off, the
	// default: a box that never opted in must not start deleting worktrees.
	HousekeepingInterval Duration       `yaml:"housekeeping_interval"`
	ProjectTimeout       ProjectTimeout `yaml:"project_timeout"`
	Act                  bool           `yaml:"act"`
	// DecisionNotes turns on writing a decision's question as a comment on its
	// item. Default false: a note is an external side effect on somebody's item,
	// and an upgraded binary must not start writing there by itself (design D8).
	DecisionNotes bool `yaml:"decision_notes"`
	// DecisionAnswerers lists the logins whose comments may answer a decision.
	// Empty means the owner of the project's repository slug (design D4).
	DecisionAnswerers []string `yaml:"decision_answerers"`
	// RigLifecycle is the opt-in that lets the app stop an idle rig and start a
	// stopped one. A pointer so "no block" is distinguishable from "block with
	// defaults": an upgraded box keeps today's behaviour (design D9).
	RigLifecycle *Lifecycle `yaml:"rig_lifecycle"`
	Projects     []Project  `yaml:"projects"`
}

// The settings surface writes only what the file can safely change while the
// app runs. Every other key describes where the app lives — its socket, its
// database, its credentials, the project registry — and needs the operator's
// hand or a different route. The classification lives here, beside File, so a
// key added to File without a decision fails the completeness test instead of
// becoming silently writable (design D2).
//
// EditableRigLifecycle is writable as a block; its fields are addressed as
// rig_lifecycle.<field> so a write can name one without replacing the block.
const EditableRigLifecycle = "rig_lifecycle"

// lifecycleFields are the rig_lifecycle block's writable fields.
var lifecycleFields = []string{"enabled", "idle_after", "idle_poll_max"}

// EditableKey reports whether the settings surface may write key. The second
// return value is the refusal sentence, empty when the key is writable. A key
// File does not declare is never writable.
func EditableKey(key string) (bool, string) {
	switch key {
	case "tick_interval", "notify_interval", "housekeeping_interval", "act", EditableRigLifecycle:
		return true, ""
	}
	if field, ok := strings.CutPrefix(key, EditableRigLifecycle+"."); ok {
		for _, known := range lifecycleFields {
			if field == known {
				return true, ""
			}
		}
		return false, fmt.Sprintf("%s is not a rig_lifecycle field", key)
	}
	switch key {
	case "listen", "database", "token", "token_file", "rig_binary", "gh_binary", "glab_binary":
		return false, fmt.Sprintf("%s needs the operator's hand: the running process reads it once at boot", key)
	case "webhook_secret_file":
		// The secret is read once at boot and lives in memory; the settings screen must
		// not write a credential, and a running process would keep the old one anyway.
		return false, "webhook_secret_file is read once at boot and names a credential; edit it in the file"
	case "decision_notes", "decision_answerers":
		return false, fmt.Sprintf("%s is read once when the notifier is built at boot; edit it in the file", key)
	case "project_timeout":
		return false, fmt.Sprintf("%s is per-project tuning; edit it in the file", key)
	case "projects":
		return false, "projects has its own route (POST /api/projects); the settings surface does not write the registry"
	}
	return false, fmt.Sprintf("unknown key %q", key)
}

// FileKeys lists every YAML key File declares, read from its own struct tags so
// the list cannot drift from the shape. The completeness test walks it.
func FileKeys() []string {
	typ := reflect.TypeOf(File{})
	keys := make([]string, 0, typ.NumField())
	for index := range typ.NumField() {
		name, _, _ := strings.Cut(typ.Field(index).Tag.Get("yaml"), ",")
		if name == "" || name == "-" {
			continue
		}
		keys = append(keys, name)
	}
	return keys
}

// ProjectTimeout is the file form of the per-project read deadline:
// base + per_item*items, capped at ceiling. The mirror resolves it per project.
type ProjectTimeout struct {
	Base    Duration `yaml:"base"`
	PerItem Duration `yaml:"per_item"`
	Ceiling Duration `yaml:"ceiling"`
}

// Timeouts is the resolved per-project read deadline.
type Timeouts struct {
	Base, PerItem, Ceiling time.Duration
}

// Lifecycle is the rig_lifecycle file block: the opt-in, how long a rig stays
// quiet before it is stopped, and the ceiling the poll backs off to (design D7).
type Lifecycle struct {
	Enabled     bool     `yaml:"enabled"`
	IdleAfter   Duration `yaml:"idle_after"`
	IdlePollMax Duration `yaml:"idle_poll_max"`
}

// LifecycleSettings is the resolved rig_lifecycle block. Written records whether
// the file actually carried the block, which is what decides the poll cadence: a
// box that never opts in must poll at the fixed tick_interval, exactly as it did
// before this setting existed. Enabled alone cannot answer that, because the
// block's zero value is also "not enabled".
type LifecycleSettings struct {
	Written     bool
	Enabled     bool
	IdleAfter   time.Duration
	IdlePollMax time.Duration
}

// ProjectLifecycle is one project's effective lifecycle value and where it came
// from: the project's own `lifecycle:` key, or the block it inherits. It is what
// --print-config lists per project.
type ProjectLifecycle struct {
	Key       string `yaml:"project"`
	Lifecycle string `yaml:"lifecycle"` // on | off
	Source    string `yaml:"source"`    // file | inherit
}

// LifecycleOn reports whether the app manages the named project's rigs. A key the
// file does not name inherits the block's enabled flag, so a project absent from
// the config is not exempted by accident.
func (e *Effective) LifecycleOn(projectKey string) bool {
	for _, entry := range e.Lifecycles {
		if entry.Key == projectKey {
			return entry.Lifecycle == "on"
		}
	}
	return e.RigLifecycle.Enabled
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// Options are the inputs to Load.
type Options struct {
	FlagPath string            // --config; must exist when set
	Flags    map[string]string // explicitly set flags, by config key
	Env      func(string) string
	Home     string
	Cwd      string
}

// Effective is the resolved configuration and where each value came from.
type Effective struct {
	Listen    string
	Database  string
	Token     string
	TokenFile string
	// WebhookSecret is read at boot and held in memory; empty disables the route.
	WebhookSecretFile string
	WebhookSecret     string
	RigBinary         string
	GhBinary          string
	GlabBinary        string
	TickInterval      time.Duration
	NotifyInterval    time.Duration // digest cadence; 0 means off
	// HousekeepingInterval is the reclaim sweep's cadence; 0 means off.
	HousekeepingInterval time.Duration
	ProjectTimeout       Timeouts
	Act                  bool
	DecisionNotes        bool
	DecisionAnswerers    []string
	RigLifecycle         LifecycleSettings
	Lifecycles           []ProjectLifecycle
	Projects             []Project
	ConfigPath           string
	Sources              map[string]string
	Warnings             []string
}

const (
	defaultListen      = "127.0.0.1:8787"
	defaultDatabase    = "fleet-console.db"
	defaultRig         = "rig"
	defaultForge       = "gh"
	defaultGlab        = "glab"
	defaultTick        = 15 * time.Second
	defaultIdleAfter   = 10 * time.Minute
	defaultIdlePollMax = 10 * time.Minute
)

// DefaultTickInterval is the mirror poll cadence a box uses when neither the
// file nor the environment sets tick_interval. The board judges how old a
// mirror read is against it (#394), so a caller that never learned the cadence
// still measures ages with the real default rather than a zero interval.
const DefaultTickInterval = defaultTick

// FindPath applies the documented lookup order and returns the first existing
// file. An explicitly requested file (flag or env) must exist.
func FindPath(opts Options) (string, error) {
	env := opts.Env
	if env == nil {
		env = os.Getenv
	}
	home := opts.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	cwd := opts.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if opts.FlagPath != "" {
		if _, err := os.Stat(opts.FlagPath); err != nil {
			return "", fmt.Errorf("config: %s: %w", opts.FlagPath, err)
		}
		return opts.FlagPath, nil
	}
	if explicit := env("FLEET_CONSOLE_CONFIG"); explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("config: FLEET_CONSOLE_CONFIG=%s: %w", explicit, err)
		}
		return explicit, nil
	}
	candidates := []string{}
	if xdg := env("XDG_CONFIG_HOME"); xdg != "" {
		candidates = append(candidates, filepath.Join(xdg, "fleet-console", "config.yaml"))
	}
	if home != "" {
		candidates = append(candidates, filepath.Join(home, ".config", "fleet-console", "config.yaml"))
	}
	candidates = append(candidates, filepath.Join(cwd, "fleet-console.yaml"))
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", nil
}

// Load finds, decodes and resolves the configuration. No file is not an error.
func Load(opts Options) (*Effective, error) {
	env := opts.Env
	if env == nil {
		env = os.Getenv
	}
	home := opts.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	path, err := FindPath(opts)
	if err != nil {
		return nil, err
	}
	if path != "" {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("config: %s: %w", path, err)
		}
		path = absolute
	}
	var file File
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("config: read %s: %w", path, err)
		}
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		decoder.KnownFields(true)
		if err := decoder.Decode(&file); err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("config: %s: %w", path, err)
		}
	}
	dir := ""
	if path != "" {
		dir = filepath.Dir(path)
	}
	eff := &Effective{ConfigPath: path, Sources: map[string]string{}}
	eff.Listen, eff.Sources["listen"] = pick("listen", opts.Flags, env, "FLEET_CONSOLE_ADDR", file.Listen, defaultListen)
	eff.Database, eff.Sources["database"] = pick("database", opts.Flags, env, "FLEET_CONSOLE_DB", resolve(dir, file.Database, home), defaultDatabase)
	eff.RigBinary, eff.Sources["rig_binary"] = pick("rig_binary", opts.Flags, env, "FLEET_CONSOLE_RIG_BIN", resolveBinary(dir, file.RigBinary, home), defaultRig)
	eff.GhBinary, eff.Sources["gh_binary"] = pick("gh_binary", opts.Flags, env, "FLEET_CONSOLE_GH_BIN", resolveBinary(dir, file.GhBinary, home), defaultForge)
	eff.GlabBinary, eff.Sources["glab_binary"] = pick("glab_binary", opts.Flags, env, "FLEET_CONSOLE_GLAB_BIN", resolveBinary(dir, file.GlabBinary, home), defaultGlab)

	tick := defaultTick
	tickSource := "default"
	if file.TickInterval > 0 {
		tick = time.Duration(file.TickInterval)
		tickSource = "file"
	}
	if raw := env("FLEET_CONSOLE_TICK_INTERVAL"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("config: FLEET_CONSOLE_TICK_INTERVAL: %w", err)
		}
		tick, tickSource = parsed, "env"
	}
	if raw, ok := opts.Flags["tick_interval"]; ok && raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("config: --tick-interval: %w", err)
		}
		tick, tickSource = parsed, "flag"
	}
	eff.TickInterval, eff.Sources["tick_interval"] = tick, tickSource

	// rig_lifecycle is file and default only: no env var, no flag (design D9).
	// The ceiling is refused rather than clamped when it cannot be honoured, but
	// only when the block is actually written: a box that never opts in must
	// behave exactly as it did before, whatever its tick_interval.
	enabled := false
	idleAfter, idlePollMax := defaultIdleAfter, defaultIdlePollMax
	eff.Sources["rig_lifecycle.enabled"] = "default"
	eff.Sources["rig_lifecycle.idle_after"] = "default"
	eff.Sources["rig_lifecycle.idle_poll_max"] = "default"
	if file.RigLifecycle != nil {
		enabled = file.RigLifecycle.Enabled
		eff.Sources["rig_lifecycle.enabled"] = "file"
		if file.RigLifecycle.IdleAfter > 0 {
			idleAfter = time.Duration(file.RigLifecycle.IdleAfter)
			eff.Sources["rig_lifecycle.idle_after"] = "file"
		}
		if file.RigLifecycle.IdlePollMax > 0 {
			idlePollMax = time.Duration(file.RigLifecycle.IdlePollMax)
			eff.Sources["rig_lifecycle.idle_poll_max"] = "file"
		}
		if idlePollMax < tick {
			return nil, fmt.Errorf("config: rig_lifecycle.idle_poll_max %s is below tick_interval %s",
				idlePollMax, tick)
		}
	}
	eff.RigLifecycle = LifecycleSettings{
		Written: file.RigLifecycle != nil, Enabled: enabled,
		IdleAfter: idleAfter, IdlePollMax: idlePollMax,
	}
	eff.Lifecycles = make([]ProjectLifecycle, 0, len(file.Projects))
	for _, project := range file.Projects {
		if project.Lifecycle == "" {
			eff.Lifecycles = append(eff.Lifecycles, ProjectLifecycle{
				Key: project.Key, Lifecycle: onOff(enabled), Source: "inherit",
			})
			continue
		}
		eff.Lifecycles = append(eff.Lifecycles, ProjectLifecycle{
			Key: project.Key, Lifecycle: project.Lifecycle, Source: "file",
		})
	}

	// notify_interval is the optional digest cadence. Zero means off, which is
	// the default: an unattended run should not start talking on its own.
	eff.NotifyInterval, eff.Sources["notify_interval"] = time.Duration(file.NotifyInterval), "file"
	// housekeeping_interval is the reclaim sweep's cadence. Zero means off, which is
	// the default: an upgraded box keeps its current behaviour and never starts
	// deleting worktrees because a new key appeared with a non-zero default.
	eff.HousekeepingInterval, eff.Sources["housekeeping_interval"] = time.Duration(file.HousekeepingInterval), "file"

	base, perItem, ceiling := 30*time.Second, 2*time.Second, 2*time.Minute
	timeoutSource := "default"
	if file.ProjectTimeout.Base > 0 {
		base, timeoutSource = time.Duration(file.ProjectTimeout.Base), "file"
	}
	if file.ProjectTimeout.PerItem > 0 {
		perItem, timeoutSource = time.Duration(file.ProjectTimeout.PerItem), "file"
	}
	if file.ProjectTimeout.Ceiling > 0 {
		ceiling, timeoutSource = time.Duration(file.ProjectTimeout.Ceiling), "file"
	}
	if ceiling < base {
		return nil, fmt.Errorf("config: project_timeout.ceiling %s is below base %s", ceiling, base)
	}
	eff.ProjectTimeout = Timeouts{Base: base, PerItem: perItem, Ceiling: ceiling}
	eff.Sources["project_timeout"] = timeoutSource

	eff.Act = file.Act
	if file.Act {
		eff.Sources["act"] = "file"
	} else {
		eff.Sources["act"] = "default"
	}

	eff.DecisionNotes = file.DecisionNotes
	if file.DecisionNotes {
		eff.Sources["decision_notes"] = "file"
	} else {
		eff.Sources["decision_notes"] = "default"
	}
	// The default author rule is resolved per project from its repository slug
	// (design D4), so an empty list here means "the owner", not "nobody".
	eff.DecisionAnswerers = append([]string(nil), file.DecisionAnswerers...)
	if len(eff.DecisionAnswerers) > 0 {
		eff.Sources["decision_answerers"] = "file"
	} else {
		eff.Sources["decision_answerers"] = "default"
	}

	if file.TokenFile != "" {
		eff.TokenFile = resolve(dir, file.TokenFile, home)
		eff.Sources["token_file"] = "file"
	} else {
		eff.Sources["token_file"] = "none"
	}
	switch {
	case env("FLEET_CONSOLE_TOKEN") != "":
		eff.Token, eff.Sources["token"] = env("FLEET_CONSOLE_TOKEN"), "env"
	case file.Token != "":
		eff.Token, eff.Sources["token"] = file.Token, "file"
		eff.Warnings = append(eff.Warnings, "token is set inline in the config; prefer token_file")
	case eff.TokenFile != "":
		token, err := readTokenFile(eff.TokenFile)
		if err != nil {
			return nil, err
		}
		eff.Token, eff.Sources["token"] = token, "token_file"
	default:
		eff.Sources["token"] = "none"
	}
	// The webhook secret follows the token's rules: a path resolved once at boot,
	// the value read into memory, and "none" when unset so an upgraded box gains
	// no public route until the operator asks for one (design D3).
	if file.WebhookSecretFile != "" {
		eff.WebhookSecretFile = resolve(dir, file.WebhookSecretFile, home)
		eff.Sources["webhook_secret_file"] = "file"
		secret, err := readSecretFile(eff.WebhookSecretFile, "webhook_secret_file")
		if err != nil {
			return nil, err
		}
		eff.WebhookSecret = secret
	} else {
		eff.Sources["webhook_secret_file"] = "none"
	}
	if err := validateProjects(&file, dir, home); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	for _, project := range file.Projects {
		eff.Warnings = append(eff.Warnings, ProjectWarnings(project)...)
		// repo_url is a slug in the file, not a URL: a self-hosted GitLab cannot
		// be derived from it, so say so once at load rather than guess.
		if project.Forge == "gitlab" && project.RepoURL == "" {
			eff.Warnings = append(eff.Warnings, fmt.Sprintf(
				"project %q: repo_url is not set; deriving https://gitlab.com/%s — set repo_url for a self-hosted GitLab",
				project.Key, project.Repo))
		}
	}
	eff.Projects = file.Projects
	if len(file.Projects) > 0 {
		eff.Sources["projects"] = "file"
	} else {
		eff.Sources["projects"] = "none"
	}
	return eff, nil
}

func pick(key string, flags map[string]string, env func(string) string, envName, fileValue, fallback string) (string, string) {
	if value, ok := flags[key]; ok && value != "" {
		return value, "flag"
	}
	if value := env(envName); value != "" {
		return value, "env"
	}
	if fileValue != "" {
		return fileValue, "file"
	}
	return fallback, "default"
}

// resolve expands ~ and joins relative file paths to the config's directory.
func resolve(dir, value, home string) string {
	if value == "" {
		return ""
	}
	if value == "~" {
		return home
	}
	if strings.HasPrefix(value, "~/") {
		value = filepath.Join(home, value[2:])
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	if dir == "" {
		return value
	}
	return filepath.Clean(filepath.Join(dir, value))
}

// resolveBinary leaves a bare command name on PATH; a path is resolved.
func resolveBinary(dir, value, home string) string {
	if value == "" {
		return ""
	}
	if !strings.Contains(value, "/") {
		return value
	}
	return resolve(dir, value, home)
}

func readTokenFile(path string) (string, error) {
	return readSecretFile(path, "token_file")
}

// readSecretFile reads one credential from a 0600 file. Both the bearer token and
// the webhook secret follow the same rules, so a mistake in one is a mistake in
// both: a readable-by-others file is refused rather than used, because a config
// error that silently weakens a credential is worse than a refusal at boot.
func readSecretFile(path, key string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("config: %s %s: %w", key, path, err)
	}
	mode := info.Mode().Perm()
	if mode != 0o600 {
		return "", fmt.Errorf("config: %s %s has mode %04o; must be 0600", key, path, mode)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("config: %s %s: %w", key, path, err)
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", fmt.Errorf("config: %s %s is empty", key, path)
	}
	return value, nil
}

// projectPrint is one project as --print-config shows it: the project's own
// fields plus the forge host the process resolved (forge-host, design D1).
// Empty host (GitHub, or a repo_url that carries no host) prints no host line —
// there is nothing the process would use.
type projectPrint struct {
	Project `yaml:",inline"`
	Host    string `yaml:"host,omitempty"`
}

// Print renders the effective configuration as YAML without the token value and
// with the source of every value.
func (e *Effective) Print() ([]byte, error) {
	type view struct {
		Config    string `yaml:"config"`
		Listen    string `yaml:"listen"`
		Database  string `yaml:"database"`
		Token     string `yaml:"token,omitempty"`
		TokenFile string `yaml:"token_file,omitempty"`
		// WebhookSecret is redacted like the token: the key and its source are named,
		// the value never is (design D3).
		WebhookSecret     string `yaml:"webhook_secret,omitempty"`
		WebhookSecretFile string `yaml:"webhook_secret_file,omitempty"`
		RigBinary         string `yaml:"rig_binary"`
		GhBinary          string `yaml:"gh_binary"`
		GlabBinary        string `yaml:"glab_binary"`
		Tick              string `yaml:"tick_interval"`
		Notify            string `yaml:"notify_interval"`
		// Housekeeping is the reclaim sweep's cadence; "0s" is off.
		Housekeeping string `yaml:"housekeeping_interval"`
		Timeout      struct {
			Base    string `yaml:"base"`
			PerItem string `yaml:"per_item"`
			Ceiling string `yaml:"ceiling"`
		} `yaml:"project_timeout"`
		Act               bool     `yaml:"act"`
		DecisionNotes     bool     `yaml:"decision_notes"`
		DecisionAnswerers []string `yaml:"decision_answerers,omitempty"`
		RigLifecycle      struct {
			Enabled     bool   `yaml:"enabled"`
			IdleAfter   string `yaml:"idle_after"`
			IdlePollMax string `yaml:"idle_poll_max"`
		} `yaml:"rig_lifecycle"`
		Lifecycles []ProjectLifecycle `yaml:"lifecycles,omitempty"`
		Projects   []projectPrint     `yaml:"projects,omitempty"`
		Sources    map[string]string  `yaml:"sources"`
		Warnings   []string           `yaml:"warnings,omitempty"`
	}
	out := view{
		Config: e.ConfigPath, Listen: e.Listen, Database: e.Database, TokenFile: e.TokenFile,
		RigBinary: e.RigBinary, GhBinary: e.GhBinary, GlabBinary: e.GlabBinary,
		Tick: e.TickInterval.String(), Notify: e.NotifyInterval.String(),
		Act: e.Act, Sources: e.Sources, Warnings: e.Warnings,
		DecisionNotes: e.DecisionNotes, DecisionAnswerers: e.DecisionAnswerers,
		Housekeeping: e.HousekeepingInterval.String(),
	}
	out.Timeout.Base = e.ProjectTimeout.Base.String()
	out.Timeout.PerItem = e.ProjectTimeout.PerItem.String()
	out.Timeout.Ceiling = e.ProjectTimeout.Ceiling.String()
	out.RigLifecycle.Enabled = e.RigLifecycle.Enabled
	out.RigLifecycle.IdleAfter = e.RigLifecycle.IdleAfter.String()
	out.RigLifecycle.IdlePollMax = e.RigLifecycle.IdlePollMax.String()
	out.Lifecycles = e.Lifecycles
	// The forge host is resolved here with the one resolver (forge-host D1), so
	// --print-config shows the host and source the process itself would use —
	// not a re-derivation. A resolver error (a repo_url with no host) leaves the
	// line out: the field says what could be resolved, not what could not.
	for _, project := range e.Projects {
		printed := projectPrint{Project: project}
		if host, source, err := ForgeHost(project); err == nil {
			printed.Host = fmt.Sprintf("%s (%s)", host, source)
		}
		out.Projects = append(out.Projects, printed)
	}
	if out.Config == "" {
		out.Config = "(none)"
	}
	if e.Token != "" {
		out.Token = "<redacted>"
	}
	// The webhook secret is redacted on the same rule as the token: the operator
	// sees that one is configured and where it came from, never its value.
	if e.WebhookSecret != "" {
		out.WebhookSecret = "<redacted>"
	}
	out.WebhookSecretFile = e.WebhookSecretFile
	return yaml.Marshal(out)
}

// reviewerName is a member prefix a seat can carry: lowercase letters, digits
// and dashes, no "@" (the rig is appended by the caller).
var reviewerName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ReviewerFor resolves a project's reviewer member prefix (#384). Unset is
// DefaultReviewer; the caller appends "@<rig>" for the destination seat. This is
// the one lookup path: the tick's raises and the plan's recognition both read
// the project through it, so a renamed seat moves both together.
func (e *Effective) ReviewerFor(projectKey string) string {
	project := findProjectByKey(e.Projects, projectKey)
	if project == nil || strings.TrimSpace(project.Reviewer) == "" {
		return DefaultReviewer
	}
	return strings.TrimSpace(project.Reviewer)
}

// RelayRow is the store's half of a project's verdict relay (#375). The zero
// value is "the row names nothing", which is what a project without a database
// row (or before the migration backfilled it) resolves with.
type RelayRow struct {
	Mode string
	Seat string
}

// RelayFor resolves a project's verdict relay (verdict-relay, design D8;
// #375) into the (mode, seat) pair. The row answers first — the board writes
// there, and the tick reads rows — and the config block is the fallback, so a
// project that only lives in the file keeps working. A project that names no
// mode in either place is ("", "") — off. This is the one lookup path: the
// decide arm (T2) and the act raise (T3) both call it, so the running process
// answers this pair and no other.
func (e *Effective) RelayFor(projectKey string, row RelayRow) (mode, seat string) {
	row.Mode = strings.TrimSpace(row.Mode)
	if row.Mode != "" {
		return row.Mode, strings.TrimSpace(row.Seat)
	}
	project := findProjectByKey(e.Projects, projectKey)
	if project == nil || project.VerdictRelay == nil || project.VerdictRelay.Mode == "" {
		return "", ""
	}
	return project.VerdictRelay.Mode, project.VerdictRelay.Seat
}

// ForgeHost resolves one project's forge API host (forge-host, design D1).
// The order is the whole design: forge_host when set, otherwise repo_url's
// host, otherwise gitlab.com for GitLab and empty for GitHub. source names
// where the answer came from ("forge_host", "repo_url" or "default"), so
// --print-config can prove what the process actually resolved. This is the
// one resolver: no caller, here or elsewhere, parses repo_url for a host on
// its own.
func ForgeHost(p Project) (host, source string, err error) {
	if normalized := strings.TrimSpace(p.ForgeHost); normalized != "" {
		return normalized, "forge_host", nil
	}
	if p.RepoURL != "" {
		parsed, parseErr := url.Parse(p.RepoURL)
		if parseErr != nil || parsed.Host == "" {
			return "", "", fmt.Errorf("project %q: repo_url %q has no host", p.Key, p.RepoURL)
		}
		return strings.ToLower(parsed.Host), "repo_url", nil
	}
	if strings.EqualFold(strings.TrimSpace(p.Forge), "gitlab") {
		return "gitlab.com", "default", nil
	}
	return "", "", nil
}

// findProjectByKey is the linear lookup by key. findProject in
// internal/web carries a different return shape, so config owns its own.
func findProjectByKey(projects []Project, key string) *Project {
	for index := range projects {
		if projects[index].Key == key {
			return &projects[index]
		}
	}
	return nil
}

// validateCultureFile resolves a project's culture_file against the config's
// directory and home, and refuses a value the board could not edit: it must name
// a markdown file, not a directory, absolute or ~-rooted. An unset key is the
// normal case — the board derives the path from the first rig — and a path that
// does not exist yet is accepted, because creating it is the editor's job.
func validateCultureFile(project *Project, dir, home string) error {
	value := strings.TrimSpace(project.CultureFile)
	if value == "" {
		return nil
	}
	if !filepath.IsAbs(value) && !strings.HasPrefix(value, "~/") && value != "~" {
		return cultureFieldError(project.Key, fmt.Sprintf("culture_file %s must be absolute or start with ~/", value))
	}
	resolved := resolve(dir, value, home)
	if !strings.EqualFold(filepath.Ext(resolved), ".md") {
		return cultureFieldError(project.Key, fmt.Sprintf("culture_file %s must be a markdown file", resolved))
	}
	if info, err := os.Stat(resolved); err == nil && info.IsDir() {
		return cultureFieldError(project.Key, fmt.Sprintf("culture_file %s is a directory", resolved))
	}
	project.CultureFile = resolved
	return nil
}

// FieldError is one rule failure on one field of a project.
type FieldError struct {
	Field   string
	Message string
}

// ValidationError carries every rule a single definition failed, in rule
// order. The board answers it as `422 {"errors": {<field>: <message>}}`; the
// loader prints Error(), so both speak with the same voice.
type ValidationError struct {
	Errors []FieldError
}

// Error names the first failure; the loader has no per-field surface.
func (e *ValidationError) Error() string {
	if len(e.Errors) == 0 {
		return "invalid project"
	}
	return e.Errors[0].Message
}

// Fields returns the first message per field, which is the 422 body.
func (e *ValidationError) Fields() map[string]string {
	fields := make(map[string]string, len(e.Errors))
	for _, failure := range e.Errors {
		if _, seen := fields[failure.Field]; !seen {
			fields[failure.Field] = failure.Message
		}
	}
	return fields
}

// ValidateProject applies the design §4 rule set to one definition and
// normalises it in place: trimmed values, `rig` folded into `rigs`, a missing
// name taken from the key, the default branch and rig template filled in, and
// paths expanded against dir and home. The loader and the HTTP handler both
// call it, so a project the board accepts is a project the next boot accepts.
func ValidateProject(p *Project, dir, home string) error {
	var failures []FieldError
	add := func(field, format string, args ...any) {
		failures = append(failures, FieldError{Field: field, Message: fmt.Sprintf(format, args...)})
	}

	p.Key = strings.TrimSpace(p.Key)
	p.Forge = strings.ToLower(strings.TrimSpace(p.Forge))
	p.Repo = strings.TrimSpace(p.Repo)
	p.RepoURL = strings.TrimSpace(p.RepoURL)
	p.Name = strings.TrimSpace(p.Name)
	p.DefaultBranch = strings.TrimSpace(p.DefaultBranch)
	p.Lifecycle = strings.TrimSpace(p.Lifecycle)

	// forge_host (forge-host, design D1): normalised here so the loader and the
	// board's 422 share one path — trim, drop one trailing slash, lower-case.
	// A URL-shaped value is refused: the field says host.
	p.ForgeHost = strings.TrimSpace(p.ForgeHost)
	if strings.HasSuffix(p.ForgeHost, "/") {
		p.ForgeHost = strings.TrimSuffix(p.ForgeHost, "/")
	}
	p.ForgeHost = strings.ToLower(p.ForgeHost)
	if strings.Contains(p.ForgeHost, "://") || strings.Contains(p.ForgeHost, "/") {
		add("forge_host", "forge_host must be a host, not a URL")
	}

	switch {
	case p.Key == "":
		add("key", "key is required")
	case !projectKeyRe.MatchString(p.Key):
		add("key", "key must match %s", projectKeyRe.String())
	}
	if p.Name == "" {
		p.Name = p.Key
	}
	if p.Repo == "" {
		add("repo", "repo is required")
	}
	switch p.Forge {
	case "github", "gitlab":
	default:
		add("forge", "forge must be github or gitlab")
	}
	if p.DefaultBranch == "" {
		p.DefaultBranch = "main"
	}
	if p.Rig != "" && p.Rigs != nil {
		add("rigs", "rig and rigs are mutually exclusive")
	} else if p.Rig != "" {
		// The single-rig spelling is the tier-0, on-full form: the same rig with
		// the defaults the union decode gives a bare string.
		p.Rigs = RigRefs{{Name: strings.TrimSpace(p.Rig), Tier: 0, Spill: SpillOnFull}}
	}
	if len(p.Rigs) == 0 {
		add("rigs", "rig or rigs is required")
	}
	seen := make(map[string]bool, len(p.Rigs))
	for index := range p.Rigs {
		ref := &p.Rigs[index]
		ref.Name = strings.TrimSpace(ref.Name)
		if ref.Name == "" {
			add("rigs", "rigs[%d] must be non-empty", index)
			continue
		}
		if seen[ref.Name] {
			// Two entries naming one rig would count its seats twice.
			add("rigs", "rigs[%d] duplicates rig %q", index, ref.Name)
			continue
		}
		seen[ref.Name] = true
		// A tier is a position, so a negative one has no meaning.
		if ref.Tier < 0 {
			add("rigs", "rigs[%d] (%s) has a negative tier %d", index, ref.Name, ref.Tier)
		}
		// An unknown trigger would silently become "never" if accepted, so name
		// the value the file actually carried.
		if ref.Spill == "" {
			ref.Spill = SpillOnFull
		}
		if !validSpill(ref.Spill) {
			add("rigs", "rigs[%d] (%s) has unknown spill %q; want one of %s", index, ref.Name, ref.Spill, SpillNames())
		}
	}
	p.Rig = ""
	if p.ConcurrencyCap < 0 {
		add("concurrency_cap", "concurrency_cap must not be negative")
	}
	switch canonical, ok := rig.KnownRuntime(p.Runtime); {
	case strings.TrimSpace(p.Runtime) == "":
		p.Runtime = rig.DefaultRuntime
	case ok:
		p.Runtime = canonical
	default:
		add("runtime", "runtime %q is not one of %s", strings.TrimSpace(p.Runtime), rig.RuntimesText)
	}
	switch p.Lifecycle {
	case "", "on", "off":
	default:
		add("lifecycle", "lifecycle must be on or off, not %q", p.Lifecycle)
	}
	// verdict_relay (verdict-relay, design D8): a mode outside the two
	// constants is refused on verdict_relay.mode; a mode with an empty seat is
	// refused on verdict_relay.seat. An absent or empty block is off, not an
	// error. No default seat, no default mode, no global setting.
	if relay := p.VerdictRelay; relay != nil {
		relay.Mode = strings.TrimSpace(relay.Mode)
		relay.Seat = strings.TrimSpace(relay.Seat)
		switch relay.Mode {
		case "", RelayModeScribe, RelayModeOperator:
		default:
			add("verdict_relay.mode", "verdict_relay.mode must be %s or %s, not %q", RelayModeScribe, RelayModeOperator, relay.Mode)
		}
		if relay.Mode != "" && relay.Seat == "" {
			add("verdict_relay.seat", "verdict_relay.seat is required when verdict_relay.mode is set")
		}
	}
	// forge_host's contradictions (forge-host, design D3): a host that
	// disagrees with repo_url's would clone from one instance and merge in
	// another, and GitHub's host is gh's business (GH_HOST). Both refuse on
	// forge_host. A GitLab project with neither field keeps today's behaviour —
	// the existing repo_url warning stays verbatim.
	if p.ForgeHost != "" && p.Forge == "github" {
		add("forge_host", "forge_host is only read for gitlab; GitHub's host is gh's business (GH_HOST)")
	}
	if p.ForgeHost != "" && p.RepoURL != "" && p.Forge == "gitlab" {
		repoHost := ""
		if parsed, parseErr := url.Parse(p.RepoURL); parseErr == nil {
			repoHost = strings.ToLower(parsed.Host)
		}
		if repoHost != "" && repoHost != p.ForgeHost {
			add("forge_host", "forge_host %q contradicts repo_url's host %q", p.ForgeHost, repoHost)
		}
	}
	if p.RigTemplate == "" {
		p.RigTemplate = "default"
	}
	// reviewer (#384): the member prefix a project's review rows go to. Unset
	// is the name every project had before the field existed. A bad value is a
	// field error here rather than an unroutable row at dispatch time.
	p.Reviewer = strings.TrimSpace(p.Reviewer)
	if p.Reviewer == "" {
		p.Reviewer = DefaultReviewer
	} else if !reviewerName.MatchString(p.Reviewer) {
		add("reviewer", "reviewer must be a member prefix like %q, not %q", DefaultReviewer, p.Reviewer)
	}
	p.Paths.Main = resolve(dir, p.Paths.Main, home)
	p.Paths.Worktrees = resolve(dir, p.Paths.Worktrees, home)

	if len(failures) > 0 {
		return &ValidationError{Errors: failures}
	}
	// culture_file is resolved and checked here rather than in the loader's own
	// loop, because ValidateProject is the one place both the loader and the
	// board's create route reach.
	if err := validateCultureFile(p, dir, home); err != nil {
		return err
	}
	return nil
}

// cultureFieldError is a per-field refusal: the board's 422 puts it on the
// culture_file input, and Error() keeps the loader's one-sentence voice with the
// project named, so config.Load prints what it always printed.
func cultureFieldError(key, message string) error {
	return &ValidationError{Errors: []FieldError{{
		Field:   "culture_file",
		Message: fmt.Sprintf("config: project %q: %s", key, message),
	}}}
}

// ProjectWarnings names what a valid definition is missing but may not be
// refused for: cloning paths.main is the operator's next step, so a missing
// directory is a note, not a rejection.
func ProjectWarnings(p Project) []string {
	if p.Paths.Main == "" {
		return nil
	}
	if _, err := os.Stat(p.Paths.Main); err != nil {
		return []string{fmt.Sprintf("project %q: paths.main %s does not exist yet", p.Key, p.Paths.Main)}
	}
	return nil
}

// ValidateDocument applies the loader's own rules to candidate bytes: the whole
// File decodes under KnownFields, the project rules run, and the cross-checks
// Load performs — the rig_lifecycle ceiling against tick_interval — are enforced
// too. ValidateContent stops short of those cross-checks, so a settings write
// that used only it could leave a file the app refuses to boot from. The bytes
// are written to a temp file and read through Load, which is the only way to run
// the loader's resolution without a second copy of its arithmetic.
func ValidateDocument(data []byte, dir, home string) error {
	if err := ValidateContent(data, dir, home); err != nil {
		return err
	}
	path := filepath.Join(dir, ".settings-validate.yaml")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("config: validate %s: %w", path, err)
	}
	defer os.Remove(path)
	_, err := Load(Options{FlagPath: path, Home: home, Cwd: dir})
	return err
}

// ValidateContent applies the loader's whole-file rules to a document the
// caller is about to write: the strict schema, every project rule, one key
// each, and one owner per rig. internal/configfile calls it before the rename,
// so a write that would produce an unloadable file never reaches the disk.
func ValidateContent(data []byte, dir, home string) error {
	var file File
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&file); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode: %w", err)
	}
	return validateProjects(&file, dir, home)
}

// validateProjects runs the per-project rules and the cross-project rules on a
// decoded file and normalises each project in place.
func validateProjects(file *File, dir, home string) error {
	owners := make(map[string]string, len(file.Projects))
	keys := make(map[string]bool, len(file.Projects))
	for index := range file.Projects {
		project := &file.Projects[index]
		if err := ValidateProject(project, dir, home); err != nil {
			return fmt.Errorf("project %q: %w", project.Key, err)
		}
		if keys[project.Key] {
			return fmt.Errorf("duplicate project key %q", project.Key)
		}
		keys[project.Key] = true
		for _, ref := range project.Rigs {
			name := ref.Name
			if owner, ok := owners[name]; ok {
				return &ValidationError{Errors: []FieldError{{
					Field:   "rigs",
					Message: fmt.Sprintf("rig %q is assigned to both %q and %q", name, owner, project.Key),
				}}}
			}
			owners[name] = project.Key
		}
	}
	return nil
}

var projectKeyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
