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
	"strings"
	"time"

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
	Key            string            `yaml:"key"`
	Name           string            `yaml:"name"`
	Forge          string            `yaml:"forge"`
	Repo           string            `yaml:"repo"`
	DefaultBranch  string            `yaml:"default_branch"`
	Rig            string            `yaml:"rig,omitempty"`
	Rigs           []string          `yaml:"rigs"`
	RigTemplate    string            `yaml:"rig_template"`
	ConcurrencyCap int               `yaml:"concurrency_cap"`
	GateCmd        string            `yaml:"gate_cmd"`
	Labels         map[string]string `yaml:"labels"`
	OnHold         *bool             `yaml:"onhold"`
	Paths          struct {
		Main      string `yaml:"main"`
		Worktrees string `yaml:"worktrees"`
	} `yaml:"paths"`
}

// File is the YAML shape. KnownFields rejects anything else.
type File struct {
	Listen       string    `yaml:"listen"`
	Database     string    `yaml:"database"`
	Token        string    `yaml:"token"`
	TokenFile    string    `yaml:"token_file"`
	RigBinary    string    `yaml:"rig_binary"`
	GhBinary     string    `yaml:"gh_binary"`
	GlabBinary   string    `yaml:"glab_binary"`
	TickInterval Duration  `yaml:"tick_interval"`
	Act          bool      `yaml:"act"`
	Projects     []Project `yaml:"projects"`
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
	Listen       string
	Database     string
	Token        string
	TokenFile    string
	RigBinary    string
	GhBinary     string
	GlabBinary   string
	TickInterval time.Duration
	Act          bool
	Projects     []Project
	ConfigPath   string
	Sources      map[string]string
	Warnings     []string
}

const (
	defaultListen   = "127.0.0.1:8787"
	defaultDatabase = "fleet-console.db"
	defaultRig      = "rig"
	defaultForge    = "gh"
	defaultGlab     = "glab"
	defaultTick     = 15 * time.Second
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
	eff.Act = file.Act
	if file.Act {
		eff.Sources["act"] = "file"
	} else {
		eff.Sources["act"] = "default"
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

	rigOwners := make(map[string]string)
	for index, project := range file.Projects {
		project.Key = strings.TrimSpace(project.Key)
		project.Forge = strings.ToLower(strings.TrimSpace(project.Forge))
		project.Repo = strings.TrimSpace(project.Repo)
		if project.Key == "" {
			return nil, fmt.Errorf("config: project %d: key is required", index)
		}
		for prior := range index {
			if file.Projects[prior].Key == project.Key {
				return nil, fmt.Errorf("config: duplicate project key %q", project.Key)
			}
		}
		switch project.Forge {
		case "github", "gitlab":
		default:
			return nil, fmt.Errorf("config: project %q: unknown forge %q", project.Key, project.Forge)
		}
		if project.Repo == "" {
			return nil, fmt.Errorf("config: project %q: repo is required", project.Key)
		}
		if project.Rig != "" && project.Rigs != nil {
			return nil, fmt.Errorf("config: project %q: rig and rigs are mutually exclusive", project.Key)
		}
		if project.Rig != "" {
			project.Rigs = []string{strings.TrimSpace(project.Rig)}
		}
		if len(project.Rigs) == 0 {
			return nil, fmt.Errorf("config: project %q: rig or rigs is required", project.Key)
		}
		seen := make(map[string]bool, len(project.Rigs))
		for index, name := range project.Rigs {
			name = strings.TrimSpace(name)
			if name == "" {
				return nil, fmt.Errorf("config: project %q: rigs must be non-empty names", project.Key)
			}
			if seen[name] {
				return nil, fmt.Errorf("config: project %q: duplicate rig %q", project.Key, name)
			}
			seen[name] = true
			project.Rigs[index] = name
		}
		for _, name := range project.Rigs {
			if owner, ok := rigOwners[name]; ok {
				return nil, fmt.Errorf("config: rig %q is assigned to both %q and %q", name, owner, project.Key)
			}
			rigOwners[name] = project.Key
		}
		project.Rig = "" // the resolved list is the source
		project.Paths.Main = resolve(dir, project.Paths.Main, home)
		project.Paths.Worktrees = resolve(dir, project.Paths.Worktrees, home)
		if project.Paths.Main != "" {
			if _, err := os.Stat(project.Paths.Main); err != nil {
				eff.Warnings = append(eff.Warnings,
					fmt.Sprintf("project %q: paths.main %s does not exist yet", project.Key, project.Paths.Main))
			}
		}
		if project.DefaultBranch == "" {
			project.DefaultBranch = "main"
		}
		if project.RigTemplate == "" {
			project.RigTemplate = "default"
		}
		file.Projects[index] = project
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
		Config     string            `yaml:"config"`
		Listen     string            `yaml:"listen"`
		Database   string            `yaml:"database"`
		Token      string            `yaml:"token,omitempty"`
		TokenFile  string            `yaml:"token_file,omitempty"`
		RigBinary  string            `yaml:"rig_binary"`
		GhBinary   string            `yaml:"gh_binary"`
		GlabBinary string            `yaml:"glab_binary"`
		Tick       string            `yaml:"tick_interval"`
		Act        bool              `yaml:"act"`
		Projects   []Project         `yaml:"projects,omitempty"`
		Sources    map[string]string `yaml:"sources"`
		Warnings   []string          `yaml:"warnings,omitempty"`
	}
	out := view{
		Config: e.ConfigPath, Listen: e.Listen, Database: e.Database, TokenFile: e.TokenFile,
		RigBinary: e.RigBinary, GhBinary: e.GhBinary, GlabBinary: e.GlabBinary,
		Tick: e.TickInterval.String(), Act: e.Act, Projects: e.Projects, Sources: e.Sources, Warnings: e.Warnings,
	}
	if out.Config == "" {
		out.Config = "(none)"
	}
	if e.Token != "" {
		out.Token = "<redacted>"
	}
	return yaml.Marshal(out)
}
