package rig

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSeedSeatRefusesMissingKeyNamingTheFile(t *testing.T) {
	home := t.TempDir()
	paths, err := SeedSeat(home, SeatSeed{Session: "dev-build-1@demo-rig", ProviderEnv: "TEST_API_KEY"})
	if err == nil {
		t.Fatal("seed without a key: want error")
	}
	if !strings.Contains(err.Error(), paths.Config) {
		t.Fatalf("error = %q, want it to name %s", err, paths.Config)
	}
}

func TestSeedSeatWritesCredentialAndMCP(t *testing.T) {
	home := t.TempDir()
	paths, err := SeedSeat(home, SeatSeed{
		Session: "dev-build-1@demo-rig", ProviderEnv: "TEST_API_KEY", ProviderKey: "secret-value",
		RepoPath: "/srv/repo",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	for _, path := range []string{paths.Config, paths.Env, paths.MCP} {
		info, err := os.Stat(path)
		if err != nil || info.Size() == 0 {
			t.Fatalf("%s: %v", path, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o, want 600", path, info.Mode().Perm())
		}
	}
	config, _ := os.ReadFile(paths.Config)
	if !strings.Contains(string(config), "deepseek") && !strings.Contains(string(config), "test") {
		t.Fatalf("config = %s", config)
	}
	var mcp struct {
		Servers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	data, _ := os.ReadFile(paths.MCP)
	if err := json.Unmarshal(data, &mcp); err != nil {
		t.Fatalf("mcp json: %v", err)
	}
	if _, ok := mcp.Servers["serena"]; !ok {
		t.Fatalf("serena missing from %s", data)
	}
}

func TestLaunchRefusesMissingCredentialBeforeAnyRigCall(t *testing.T) {
	p := demoProject(t)
	p.ProviderKey = ""
	runner := &scriptedRunner{fn: func([]string) ([]byte, error) {
		return nil, fmt.Errorf("rig must not be called")
	}}
	result, err := Launch(context.Background(), NewAdapter(runner), LaunchInput{Project: p, Home: t.TempDir()})
	if err == nil {
		t.Fatal("launch without a key: want error")
	}
	if !strings.Contains(err.Error(), "config.yml") {
		t.Fatalf("error = %q, want it to name config.yml", err)
	}
	if runner.count() != 0 {
		t.Fatalf("rig calls = %d, want 0", runner.count())
	}
	if result == nil || result.SpecPath == "" {
		t.Fatal("result must carry the rendered spec path")
	}
}

func runningSeats(rigName string, sessions ...string) []byte {
	var seats []map[string]string
	for _, session := range sessions {
		seats = append(seats, map[string]string{
			"nodeId": "n-" + session, "rigId": "rig-1", "rigName": rigName,
			"canonicalSessionName": session, "logicalId": "dev.x",
			"lifecycleState": "running", "sessionStatus": "running",
		})
	}
	data, _ := json.Marshal(seats)
	return data
}

func TestLaunchVerifiesEverySeat(t *testing.T) {
	p := demoProject(t)
	home := t.TempDir()
	sessions := []string{"dev-build-1@demo-rig", "dev-build-2@demo-rig", "dev-review@demo-rig"}
	runner := &scriptedRunner{fn: func(args []string) ([]byte, error) {
		line := strings.Join(args, " ")
		switch {
		case strings.HasPrefix(line, "spec validate"):
			return []byte(`{"valid":true,"errors":[]}`), nil
		case strings.HasPrefix(line, "specs sync"):
			return []byte(`[]`), nil
		case strings.Contains(line, "--plan"):
			return []byte(`{"runId":"run-1","status":"planned","errors":[],"stages":[{"stage":"preflight","status":"ok"}]}`), nil
		case strings.HasPrefix(line, "up "):
			return []byte(`{"runId":"run-1","status":"running","errors":[]}`), nil
		case strings.HasPrefix(line, "ps "):
			return runningSeats("demo-rig", sessions...), nil
		}
		return nil, fmt.Errorf("unexpected call %s", line)
	}}
	result, err := Launch(context.Background(), NewAdapter(runner), LaunchInput{Project: p, Home: home})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if result.Plan == nil || result.Up == nil || len(result.Seats) != 3 {
		t.Fatalf("result = %+v", result)
	}

	// Drop the reviewer: the launch must fail naming it.
	runner.fn = func(args []string) ([]byte, error) {
		line := strings.Join(args, " ")
		switch {
		case strings.HasPrefix(line, "spec validate"):
			return []byte(`{"valid":true,"errors":[]}`), nil
		case strings.HasPrefix(line, "specs sync"):
			return []byte(`[]`), nil
		case strings.Contains(line, "--plan"):
			return []byte(`{"runId":"run-1","status":"planned"}`), nil
		case strings.HasPrefix(line, "up "):
			return []byte(`{"runId":"run-1","status":"running"}`), nil
		case strings.HasPrefix(line, "ps "):
			return runningSeats("demo-rig", sessions[:2]...), nil
		}
		return nil, fmt.Errorf("unexpected call %s", line)
	}
	_, err = Launch(context.Background(), NewAdapter(runner), LaunchInput{Project: p, Home: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "dev-review@demo-rig") {
		t.Fatalf("error = %v, want the stopped reviewer named", err)
	}
}

func TestLaunchRefusesInvalidSpec(t *testing.T) {
	p := demoProject(t)
	runner := &scriptedRunner{fn: func(args []string) ([]byte, error) {
		if strings.HasPrefix(strings.Join(args, " "), "spec validate") {
			return []byte(`{"valid":false,"errors":["pods[0].label: required"]}`), nil
		}
		return nil, fmt.Errorf("unexpected call %v", args)
	}}
	_, err := Launch(context.Background(), NewAdapter(runner), LaunchInput{Project: p, Home: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "pods[0].label") {
		t.Fatalf("error = %v, want the validation error", err)
	}
	if runner.saw("up ") {
		t.Fatal("an invalid spec must not be launched")
	}
}

func TestTeardownRefusesLiveRows(t *testing.T) {
	rows := []byte(`[{"qitemId":"q1","sourceSession":"a","destinationSession":"dev-build-1@demo-rig","state":"pending","pickup":{"state":"unclaimed"}}]`)
	runner := &scriptedRunner{fn: func(args []string) ([]byte, error) {
		if strings.HasPrefix(strings.Join(args, " "), "queue list") {
			return rows, nil
		}
		return nil, fmt.Errorf("unexpected call %v", args)
	}}
	_, err := Teardown(context.Background(), NewAdapter(runner), t.TempDir(), "demo-rig")
	if err == nil || !strings.Contains(err.Error(), "live queue row") {
		t.Fatalf("error = %v, want a live-row refusal", err)
	}
	if runner.saw("down ") {
		t.Fatal("teardown must not run with live rows")
	}
}

func TestTeardownArchivesSpec(t *testing.T) {
	home := t.TempDir()
	specDir := filepath.Join(home, ".openrig", "specs", "rigs", "demo-rig")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &scriptedRunner{fn: func(args []string) ([]byte, error) {
		line := strings.Join(args, " ")
		if strings.HasPrefix(line, "queue list") {
			return []byte(`[]`), nil
		}
		if strings.HasPrefix(line, "down ") {
			return []byte(`{"sessionsKilled":3,"deleted":true,"errors":[]}`), nil
		}
		return nil, fmt.Errorf("unexpected call %s", line)
	}}
	if _, err := Teardown(context.Background(), NewAdapter(runner), home, "demo-rig"); err != nil {
		t.Fatalf("teardown: %v", err)
	}
	if _, err := os.Stat(specDir); !os.IsNotExist(err) {
		t.Fatalf("spec dir still present: %v", err)
	}
	archived, err := filepath.Glob(filepath.Join(home, ".openrig", "specs", "archive", "demo-rig-*"))
	if err != nil || len(archived) != 1 {
		t.Fatalf("archive = %v, %v", archived, err)
	}
}

func TestGrowAndExport(t *testing.T) {
	p := demoProject(t)
	specPath := filepath.Join(t.TempDir(), "rig.yaml")
	runner := &scriptedRunner{fn: func(args []string) ([]byte, error) {
		line := strings.Join(args, " ")
		switch {
		case strings.HasPrefix(line, "grow "):
			return []byte(`{"ok":true,"rigId":"rig-1","rigName":"demo-rig","seats":["dev.build-3"],"nodes":[{"logicalId":"dev.build-3","status":"launched"}]}`), nil
		case strings.HasPrefix(line, "export "):
			if err := os.WriteFile(specPath, []byte("version: \"0.2\"\n"), 0o600); err != nil {
				return nil, err
			}
			return []byte(``), nil
		case strings.HasPrefix(line, "spec validate"):
			return []byte(`{"valid":true,"errors":[]}`), nil
		}
		return nil, fmt.Errorf("unexpected call %s", line)
	}}
	in := GrowInput{
		SpecPath: specPath, RigName: p.Rig, RigID: "rig-1", Member: "build-3", Pod: "dev",
		Cwd: "/tmp/wt-3", Runtime: "omp", Home: t.TempDir(),
		ProviderEnv: "TEST_API_KEY", ProviderKey: "test-key", RepoPath: "/tmp/repo",
	}
	report, err := GrowAndExport(context.Background(), NewAdapter(runner), in)
	if err != nil {
		t.Fatalf("grow: %v", err)
	}
	if !report.OK || len(report.Seats) != 1 {
		t.Fatalf("report = %+v", report)
	}
	if !runner.saw("grow rig-1 build-3 --json --pod dev --cwd /tmp/wt-3 --runtime omp") {
		t.Fatalf("grow argv missing: %v", runner.calls)
	}
	if !runner.saw("export demo-rig -o " + specPath) {
		t.Fatalf("export argv missing: %v", runner.calls)
	}
}
