// Package config loads fleet-console's settings from YAML with a strict schema,
// then resolves them with flag > env > file > default precedence. Nothing here
// logs or prints a token value.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
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
	Key            string   `yaml:"key"`
	Name           string   `yaml:"name"`
	Forge          string   `yaml:"forge"`
	Repo           string   `yaml:"repo"`
	RepoURL        string   `yaml:"repo_url"`
	DefaultBranch  string   `yaml:"default_branch"`
	Rig            string   `yaml:"rig,omitempty"`
	Rigs           []string `yaml:"rigs"`
	RigTemplate    string   `yaml:"rig_template"`
	ConcurrencyCap int      `yaml:"concurrency_cap"`
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
	Paths       struct {
		Main      string `yaml:"main"`
		Worktrees string `yaml:"worktrees"`
	} `yaml:"paths"`
}

// File is the YAML shape. KnownFields rejects anything else.
type File struct {
	Listen         string         `yaml:"listen"`
	Database       string         `yaml:"database"`
	Token          string         `yaml:"token"`
	TokenFile      string         `yaml:"token_file"`
	RigBinary      string         `yaml:"rig_binary"`
	GhBinary       string         `yaml:"gh_binary"`
	GlabBinary     string         `yaml:"glab_binary"`
	TickInterval   Duration       `yaml:"tick_interval"`
	NotifyInterval Duration       `yaml:"notify_interval"`
	ProjectTimeout ProjectTimeout `yaml:"project_timeout"`
	Act            bool           `yaml:"act"`
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
	case "tick_interval", "notify_interval", "act", EditableRigLifecycle:
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
	Listen            string
	Database          string
	Token             string
	TokenFile         string
	RigBinary         string
	GhBinary          string
	GlabBinary        string
	TickInterval      time.Duration
	NotifyInterval    time.Duration // digest cadence; 0 means off
	ProjectTimeout    Timeouts
	Act               bool
	DecisionNotes     bool
	DecisionAnswerers []string
	RigLifecycle      LifecycleSettings
	Lifecycles        []ProjectLifecycle
	Projects          []Project
	ConfigPath        string
	Sources           map[string]string
	Warnings          []string
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
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("config: token_file %s: %w", path, err)
	}
	mode := info.Mode().Perm()
	if mode != 0o600 {
		return "", fmt.Errorf("config: token_file %s has mode %04o; must be 0600", path, mode)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("config: token_file %s: %w", path, err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf("config: token_file %s is empty", path)
	}
	return token, nil
}

// Print renders the effective configuration as YAML without the token value and
// with the source of every value.
func (e *Effective) Print() ([]byte, error) {
	type view struct {
		Config     string `yaml:"config"`
		Listen     string `yaml:"listen"`
		Database   string `yaml:"database"`
		Token      string `yaml:"token,omitempty"`
		TokenFile  string `yaml:"token_file,omitempty"`
		RigBinary  string `yaml:"rig_binary"`
		GhBinary   string `yaml:"gh_binary"`
		GlabBinary string `yaml:"glab_binary"`
		Tick       string `yaml:"tick_interval"`
		Notify     string `yaml:"notify_interval"`
		Timeout    struct {
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
		Projects   []Project          `yaml:"projects,omitempty"`
		Sources    map[string]string  `yaml:"sources"`
		Warnings   []string           `yaml:"warnings,omitempty"`
	}
	out := view{
		Config: e.ConfigPath, Listen: e.Listen, Database: e.Database, TokenFile: e.TokenFile,
		RigBinary: e.RigBinary, GhBinary: e.GhBinary, GlabBinary: e.GlabBinary,
		Tick: e.TickInterval.String(), Notify: e.NotifyInterval.String(),
		Act: e.Act, Projects: e.Projects, Sources: e.Sources, Warnings: e.Warnings,
		DecisionNotes: e.DecisionNotes, DecisionAnswerers: e.DecisionAnswerers,
	}
	out.Timeout.Base = e.ProjectTimeout.Base.String()
	out.Timeout.PerItem = e.ProjectTimeout.PerItem.String()
	out.Timeout.Ceiling = e.ProjectTimeout.Ceiling.String()
	out.RigLifecycle.Enabled = e.RigLifecycle.Enabled
	out.RigLifecycle.IdleAfter = e.RigLifecycle.IdleAfter.String()
	out.RigLifecycle.IdlePollMax = e.RigLifecycle.IdlePollMax.String()
	out.Lifecycles = e.Lifecycles
	if out.Config == "" {
		out.Config = "(none)"
	}
	if e.Token != "" {
		out.Token = "<redacted>"
	}
	return yaml.Marshal(out)
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
		p.Rigs = []string{strings.TrimSpace(p.Rig)}
	}
	if len(p.Rigs) == 0 {
		add("rigs", "rig or rigs is required")
	}
	seen := make(map[string]bool, len(p.Rigs))
	for index, name := range p.Rigs {
		name = strings.TrimSpace(name)
		if name == "" {
			add("rigs", "rigs[%d] must be non-empty", index)
			continue
		}
		if seen[name] {
			add("rigs", "rigs[%d] duplicates rig %q", index, name)
			continue
		}
		seen[name] = true
		p.Rigs[index] = name
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
	if p.RigTemplate == "" {
		p.RigTemplate = "default"
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
		for _, name := range project.Rigs {
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
