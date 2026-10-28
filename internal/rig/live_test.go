//go:build live

package rig

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLiveThrowawayRig launches a real three-seat rig, grows it, exports the
// topology back, and removes it. It is opt-in: it consumes real provider
// capacity and must not run in CI.
//
//	FLEET_CONSOLE_LIVE_RIG=1 go test -tags live -run TestLiveThrowawayRig -v -timeout 20m ./internal/rig
func TestLiveThrowawayRig(t *testing.T) {
	if os.Getenv("FLEET_CONSOLE_LIVE_RIG") == "" {
		t.Skip("set FLEET_CONSOLE_LIVE_RIG=1 to run a real rig")
	}
	key := os.Getenv("DEEPSEEK_API_KEY")
	if key == "" {
		t.Fatal("DEEPSEEK_API_KEY is required for the live launch")
	}
	home := "/home/dinhtrung"
	agentsDir := "/home/dinhtrung/.local/lib/node_modules/@openrig/cli/daemon/specs/agents"
	if _, err := os.Stat(filepath.Join(agentsDir, "development", "implementer")); err != nil {
		t.Fatalf("agents dir: %v", err)
	}
	base := filepath.Join(home, "develop", "fc-throwaway-4")
	rigName := "fc-throwaway-4"
	for _, dir := range []string{
		base,
		filepath.Join(base, "repo"),
		filepath.Join(base, "worktrees", "wt-fc4-1"),
		filepath.Join(base, "worktrees", "wt-fc4-2"),
		filepath.Join(base, "worktrees", "wt-fc4-3"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	culture := filepath.Join(base, "CULTURE.md")
	guidance := filepath.Join(base, "first-move.md")
	mustWrite(t, culture, "# throwaway culture\n")
	mustWrite(t, guidance, "# first move\nWait for a message.\n")

	adapter := NewAdapter(ExecRunner{})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	// A stale rig from an interrupted run would block the launch by name.
	_, _ = adapter.Down(ctx, rigName, true)

	project := Project{
		Key:           "fc4",
		Rig:           rigName,
		Summary:       "throwaway launch for fleet-console issue 4",
		RepoPath:      filepath.Join(base, "repo"),
		WorktreeRoot:  filepath.Join(base, "worktrees"),
		Builders:      2,
		Runtime:       "omp",
		BuilderModel:  "deepseek/deepseek-v4-flash",
		ReviewerModel: "deepseek/deepseek-v4-pro",
		ProviderEnv:   "DEEPSEEK_API_KEY",
		ProviderKey:   key,
		CulturePath:   culture,
		GuidancePath:  guidance,
	}
	result, err := Launch(ctx, adapter, LaunchInput{Project: project, Home: home, AgentsDir: agentsDir})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	rendered := strings.ReplaceAll(string(result.Rendered), key, "<redacted>")
	t.Logf("RENDERED SPEC\n%s", rendered)
	t.Logf("VALIDATE %s", mustJSON(t, result.Validation))
	t.Logf("PLAN %s", mustJSON(t, result.Plan))
	t.Logf("UP %s", mustJSON(t, result.Up))
	t.Logf("SEATS %s", mustJSON(t, result.Seats))

	seats, err := adapter.SeatsForRig(ctx, rigName)
	if err != nil {
		t.Fatalf("ps after launch: %v", err)
	}
	if len(seats) != 3 {
		t.Fatalf("seats after launch = %d, want 3", len(seats))
	}
	rigID := seats[0].RigID
	grow, err := GrowAndExport(ctx, adapter, GrowInput{
		SpecPath: result.SpecPath, RigName: rigName, RigID: rigID, Member: "build-3", Pod: "dev",
		Cwd: filepath.Join(base, "worktrees", "wt-fc4-3"), Runtime: "omp",
		Home: home, ProviderEnv: "DEEPSEEK_API_KEY", ProviderKey: key,
		RepoPath: filepath.Join(base, "repo"),
	})
	if err != nil {
		t.Fatalf("grow: %v", err)
	}
	t.Logf("GROW %s", mustJSON(t, grow))
	after, err := adapter.SeatsForRig(ctx, rigName)
	if err != nil {
		t.Fatalf("ps after grow: %v", err)
	}
	t.Logf("SEATS AFTER GROW %s", mustJSON(t, after))
	found := false
	for _, seat := range after {
		if seat.Session == "dev-build-3@"+rigName {
			found = true
		}
	}
	if !found {
		t.Fatalf("grown seat not in ps: %+v", after)
	}
	exported, err := os.ReadFile(result.SpecPath)
	if err != nil {
		t.Fatalf("read exported spec: %v", err)
	}
	t.Logf("EXPORTED SPEC\n%s", strings.ReplaceAll(string(exported), key, "<redacted>"))

	down, err := Teardown(ctx, adapter, home, rigName)
	if err != nil {
		t.Fatalf("teardown: %v", err)
	}
	t.Logf("DOWN %s", mustJSON(t, down))
	remaining, err := adapter.SeatsForRig(ctx, rigName)
	if err != nil {
		t.Logf("ps after teardown error (expected if scoped rig is gone): %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("seats still listed after teardown: %+v", remaining)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("marshal: %v", err)
	}
	return string(data)
}
