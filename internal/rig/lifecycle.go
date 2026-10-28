package rig

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MCPServer is one mcp.json entry.
type MCPServer struct {
	Name     string
	Type     string
	Command  string
	Args     []string
	Env      []EnvVar
	URL      string
	Exposure string
}

// SerenaMCP is the project's symbol server entry.
func SerenaMCP(repoPath string) MCPServer {
	return MCPServer{
		Name:     "serena",
		Type:     "stdio",
		Command:  "serena",
		Args:     []string{"start-mcp-server", "--context=ide", "--project", repoPath},
		Env:      []EnvVar{{Name: "SERENA_USAGE_REPORTING", Value: "false"}},
		Exposure: "direct",
	}
}

// SeatSeed is everything written into one seat's agent directory before start.
type SeatSeed struct {
	Session     string
	ProviderEnv string
	ProviderKey string
	RepoPath    string
	MCP         []MCPServer
}

// SeatPaths are the files seeded for one seat.
type SeatPaths struct {
	Config string
	Env    string
	MCP    string
}

// SeedSeat writes config.yml, .env and mcp.json for one seat. It refuses a seat
// without a provider credential and never returns the value.
func SeedSeat(home string, seed SeatSeed) (SeatPaths, error) {
	if home == "" {
		return SeatPaths{}, fmt.Errorf("seed seat: home is required")
	}
	if seed.Session == "" || seed.ProviderEnv == "" {
		return SeatPaths{}, fmt.Errorf("seed seat: session and provider environment are required")
	}
	dir := filepath.Join(home, ".openrig", "state", "omp", seed.Session, "agent")
	paths := SeatPaths{
		Config: filepath.Join(dir, "config.yml"),
		Env:    filepath.Join(dir, ".env"),
		MCP:    filepath.Join(dir, "mcp.json"),
	}
	if strings.TrimSpace(seed.ProviderKey) == "" {
		return paths, fmt.Errorf("seed seat %s: provider credential %s is missing; expected %s", seed.Session, seed.ProviderEnv, paths.Config)
	}
	provider := strings.ToLower(strings.TrimSuffix(seed.ProviderEnv, "_API_KEY"))
	if provider == strings.ToLower(seed.ProviderEnv) {
		provider = strings.ToLower(seed.ProviderEnv)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return paths, fmt.Errorf("seed seat %s: %w", seed.Session, err)
	}
	config := fmt.Sprintf("providers:\n  %s:\n    apiKey: %s\nmcp:\n  startupTimeoutMs: 20000\n", provider, yamlScalar(seed.ProviderKey))
	if err := writeFileAtomic(paths.Config, []byte(config), 0o600); err != nil {
		return paths, err
	}
	if err := writeFileAtomic(paths.Env, []byte(seed.ProviderEnv+"="+seed.ProviderKey+"\n"), 0o600); err != nil {
		return paths, err
	}
	servers := seed.MCP
	if len(servers) == 0 && seed.RepoPath != "" {
		servers = []MCPServer{SerenaMCP(seed.RepoPath)}
	}
	mcp := mcpFile{Servers: map[string]mcpEntry{}}
	for _, server := range servers {
		entry := mcpEntry{
			Type:     server.Type,
			Command:  server.Command,
			Args:     server.Args,
			URL:      server.URL,
			Exposure: server.Exposure,
		}
		if len(server.Env) > 0 {
			entry.Env = map[string]string{}
			for _, e := range server.Env {
				entry.Env[e.Name] = e.Value
			}
		}
		mcp.Servers[server.Name] = entry
	}
	encoded, err := json.MarshalIndent(mcp, "", "  ")
	if err != nil {
		return paths, fmt.Errorf("seed seat %s: encode mcp: %w", seed.Session, err)
	}
	if err := writeFileAtomic(paths.MCP, append(encoded, '\n'), 0o600); err != nil {
		return paths, err
	}
	return paths, nil
}

// VerifySeat checks the seeded files exist and are not empty.
func VerifySeat(paths SeatPaths) error {
	for _, path := range []string{paths.Config, paths.Env, paths.MCP} {
		info, err := os.Stat(path)
		if err != nil || info.Size() == 0 {
			return fmt.Errorf("seat credential file %s is missing or empty", path)
		}
	}
	return nil
}

func yamlScalar(value string) string {
	if strings.ContainsAny(value, ":#{}[]&*!|>'\"%@` \t") {
		return fmt.Sprintf("%q", value)
	}
	return value
}

type mcpFile struct {
	Servers map[string]mcpEntry `json:"mcpServers"`
}

type mcpEntry struct {
	Type     string            `json:"type"`
	Command  string            `json:"command,omitempty"`
	Args     []string          `json:"args,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	URL      string            `json:"url,omitempty"`
	Exposure string            `json:"exposure,omitempty"`
}

// LaunchInput is one launch request.
type LaunchInput struct {
	Project   Project
	Home      string
	AgentsDir string
	MCP       []MCPServer
}

// LaunchResult is the evidence a launch produced.
type LaunchResult struct {
	SpecPath   string
	Rendered   []byte
	Validation *Validation
	Plan       *RunReport
	Up         *RunReport
	Seats      []Seat
}

// Launch renders, seeds, validates, plans, starts and verifies a rig. Every
// seat's credential is checked before any rig command runs.
func Launch(ctx context.Context, adapter *Adapter, in LaunchInput) (*LaunchResult, error) {
	spec, rendered, err := RenderRig(in.Project)
	if err != nil {
		return nil, err
	}
	specDir, err := WriteSpecDir(in.Home, spec, rendered, in.Project.CulturePath, in.Project.GuidancePath, in.AgentsDir)
	if err != nil {
		return nil, err
	}
	result := &LaunchResult{SpecPath: filepath.Join(specDir, "rig.yaml"), Rendered: rendered}
	seeded := make([]SeatPaths, 0, len(spec.Members()))
	for _, pod := range spec.Pods {
		for _, member := range pod.Members {
			paths, err := SeedSeat(in.Home, SeatSeed{
				Session:     spec.SessionName(pod.ID, member.ID),
				ProviderEnv: in.Project.ProviderEnv,
				ProviderKey: in.Project.ProviderKey,
				RepoPath:    in.Project.RepoPath,
				MCP:         in.MCP,
			})
			if err != nil {
				return result, err
			}
			if err := VerifySeat(paths); err != nil {
				return result, err
			}
			seeded = append(seeded, paths)
		}
	}

	specPath := result.SpecPath
	if result.Validation, err = adapter.SpecValidate(ctx, specPath); err != nil {
		return result, err
	}
	if !result.Validation.Valid {
		return result, fmt.Errorf("spec %s is invalid: %s", specPath, strings.Join(result.Validation.Errors, "; "))
	}
	if _, err := adapter.SpecsSync(ctx); err != nil {
		return result, err
	}
	if result.Plan, err = adapter.Plan(ctx, specPath); err != nil {
		return result, err
	}
	if len(result.Plan.Errors) > 0 || (result.Plan.Status != "planned" && result.Plan.Status != "ok") {
		return result, fmt.Errorf("plan refused %s: %s", spec.Name, strings.Join(result.Plan.Errors, "; "))
	}
	if result.Up, err = adapter.Up(ctx, specPath); err != nil {
		return result, err
	}
	if len(result.Up.Errors) > 0 {
		return result, fmt.Errorf("launch failed: %s", strings.Join(result.Up.Errors, "; "))
	}
	if result.Up.Status == "" {
		return result, fmt.Errorf("launch %s: up returned no status", spec.Name)
	}
	seats, err := adapter.SeatsForRig(ctx, spec.Name)
	if err != nil {
		return result, err
	}
	result.Seats = seats
	expected := map[string]bool{}
	for _, pod := range spec.Pods {
		for _, member := range pod.Members {
			expected[spec.SessionName(pod.ID, member.ID)] = false
		}
	}
	for _, seat := range seats {
		if _, ok := expected[seat.Session]; ok && seat.Lifecycle == "running" {
			expected[seat.Session] = true
		}
	}
	for session, running := range expected {
		if !running {
			return result, fmt.Errorf("launch %s: seat %s is not running", spec.Name, session)
		}
	}
	return result, nil
}

// GrowInput is one growth request. The new seat is seeded before it starts.
type GrowInput struct {
	SpecPath    string
	RigName     string
	RigID       string
	Member      string
	Pod         string
	Cwd         string
	Runtime     string
	Home        string
	ProviderEnv string
	ProviderKey string
	RepoPath    string
	MCP         []MCPServer
}

// GrowAndExport seeds the new seat, grows the rig and writes the running
// topology back to the spec.
func GrowAndExport(ctx context.Context, adapter *Adapter, in GrowInput) (*GrowReport, error) {
	if in.Pod == "" || in.Member == "" || in.RigName == "" {
		return nil, fmt.Errorf("grow: pod, member and rig are required")
	}
	paths, err := SeedSeat(in.Home, SeatSeed{
		Session:     in.Pod + "-" + in.Member + "@" + in.RigName,
		ProviderEnv: in.ProviderEnv,
		ProviderKey: in.ProviderKey,
		RepoPath:    in.RepoPath,
		MCP:         in.MCP,
	})
	if err != nil {
		return nil, err
	}
	if err := VerifySeat(paths); err != nil {
		return nil, err
	}
	report, err := adapter.Grow(ctx, in.RigID, in.Member, in.Pod, in.Cwd, in.Runtime)
	if err != nil {
		return nil, err
	}
	if !report.OK {
		return report, fmt.Errorf("grow %s: %s", in.Member, report.Detail)
	}
	if err := adapter.Export(ctx, in.RigName, in.SpecPath); err != nil {
		return report, err
	}
	validation, err := adapter.SpecValidate(ctx, in.SpecPath)
	if err != nil {
		return report, err
	}
	if !validation.Valid {
		return report, fmt.Errorf("exported spec %s is invalid: %s", in.SpecPath, strings.Join(validation.Errors, "; "))
	}
	return report, nil
}

// Teardown refuses while live queue rows exist, then deletes the rig and
// archives its spec directory. It never uses `rig archive`, which keeps the name.
func Teardown(ctx context.Context, adapter *Adapter, home, rigName string) (*DownReport, error) {
	rows, err := adapter.QueueList(ctx)
	if err != nil {
		return nil, err
	}
	suffix := "@" + rigName
	live := 0
	for _, row := range rows {
		switch row.State {
		case "pending", "in-progress", "blocked":
			if strings.HasSuffix(row.Destination, suffix) {
				live++
			}
		}
	}
	if live > 0 {
		return nil, fmt.Errorf("refusing teardown of %s: %d live queue row(s)", rigName, live)
	}
	report, err := adapter.Down(ctx, rigName, true)
	if err != nil {
		return nil, err
	}
	if len(report.Errors) > 0 {
		return report, fmt.Errorf("teardown %s: %s", rigName, strings.Join(report.Errors, "; "))
	}
	if !report.Deleted {
		return report, fmt.Errorf("teardown %s: rig was not deleted (alreadyStopped=%v)", rigName, report.AlreadyStopped)
	}
	specDir := filepath.Join(home, ".openrig", "specs", "rigs", rigName)
	if _, err := os.Stat(specDir); err == nil {
		archive := filepath.Join(home, ".openrig", "specs", "archive")
		if err := os.MkdirAll(archive, 0o755); err != nil {
			return report, fmt.Errorf("archive spec: %w", err)
		}
		target := filepath.Join(archive, fmt.Sprintf("%s-%s", rigName, time.Now().UTC().Format("20060102T150405Z")))
		if err := os.Rename(specDir, target); err != nil {
			return report, fmt.Errorf("archive spec: %w", err)
		}
	}
	return report, nil
}
