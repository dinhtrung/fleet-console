package configfile

import (
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/config"
)

func TestUpsertProjectCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	p := Project{
		Key:            "proj",
		Name:           "Project",
		Forge:          "github",
		Repo:           "owner/repo",
		DefaultBranch:  "main",
		Rigs:           []string{"rig-1"},
		RigTemplate:    "default",
		ConcurrencyCap: 2,
		GateCmd:        "",
		Labels:         map[string]string{},
		OnHold:         nil,
		Paths: struct {
			Main      string `yaml:"main"`
			Worktrees string `yaml:"worktrees"`
		}{Main: "", Worktrees: ""},
	}

	outcome, err := UpsertProject(path, p, nil)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if outcome != OutcomeCreated {
		t.Fatalf("outcome = %q, want Created", outcome)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("file is empty")
	}
}

func TestUpsertProjectPreservesCommentsAndOtherProjects(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	// A config with a header comment and two existing projects
	original := `# fleet-console configuration
# This file is managed by the board; edits may be overwritten.
listen: 127.0.0.1:8787
database: fleet-console.db
token_file: ~/.config/fleet-console/token
rig_binary: rig
gh_binary: gh
glab_binary: glab
tick_interval: 60s
projects:
  - key: existing-1
    name: Existing One
    forge: github
    repo: owner/one
    default_branch: main
    rigs:
      - rig-a
    rig_template: default
    concurrency_cap: 2
    gate_cmd: ""
    labels: {}
    onhold: false
    paths:
      main: ""
      worktrees: ""
  - key: existing-2
    name: Existing Two
    forge: gitlab
    repo: group/two
    default_branch: main
    rigs:
      - rig-b
    rig_template: default
    concurrency_cap: 1
    gate_cmd: ""
    labels: {}
    onhold: false
    paths:
      main: ""
      worktrees: ""
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("write original: %v", err)
	}

	p := Project{
		Key:            "new-proj",
		Name:           "New Project",
		Forge:          "github",
		Repo:           "owner/new",
		DefaultBranch:  "main",
		Rigs:           []string{"rig-c"},
		RigTemplate:    "default",
		ConcurrencyCap: 2,
		GateCmd:        "",
		Labels:         map[string]string{},
		OnHold:         nil,
		Paths: struct {
			Main      string `yaml:"main"`
			Worktrees string `yaml:"worktrees"`
		}{Main: "", Worktrees: ""},
	}

	outcome, err := UpsertProject(path, p, nil)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if outcome != OutcomeCreated {
		t.Fatalf("outcome = %q, want Created", outcome)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	result := string(data)
	// The promise is byte-level: everything the operator wrote survives, and the
	// new entry is the only addition.
	if !strings.HasPrefix(result, original) {
		t.Fatalf("the original bytes were rewritten:\n--- want prefix ---\n%s\n--- got ---\n%s", original, result)
	}
	added := strings.TrimPrefix(result, original)
	if !containsAll(added, []string{"new-proj", "New Project", "rig-c"}) {
		t.Fatalf("the addition is not the new project:\n%s", added)
	}
	if !strings.HasPrefix(added, "  - key: new-proj") {
		t.Fatalf("the new entry does not use the file's own indentation: %q", added)
	}

	// Header comment preserved
	if !contains(result, "# fleet-console configuration") || !contains(result, "# This file is managed by the board") {
		t.Fatalf("header comments not preserved:\n%s", result)
	}
}

func TestUpsertProjectUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	p := Project{
		Key:            "proj",
		Name:           "Project",
		Forge:          "github",
		Repo:           "owner/repo",
		DefaultBranch:  "main",
		Rigs:           []string{"rig-1"},
		RigTemplate:    "default",
		ConcurrencyCap: 2,
		GateCmd:        "",
		Labels:         map[string]string{},
		OnHold:         nil,
		Paths: struct {
			Main      string `yaml:"main"`
			Worktrees string `yaml:"worktrees"`
		}{Main: "", Worktrees: ""},
	}

	// First write
	if _, err := UpsertProject(path, p, nil); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// Second write with same data
	outcome, err := UpsertProject(path, p, nil)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if outcome != OutcomeUnchanged {
		t.Fatalf("outcome = %q, want Unchanged", outcome)
	}
}

func TestUpsertProjectRefusesDuplicateKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	p := Project{
		Key:            "proj",
		Name:           "Project",
		Forge:          "github",
		Repo:           "owner/repo",
		DefaultBranch:  "main",
		Rigs:           []string{"rig-1"},
		RigTemplate:    "default",
		ConcurrencyCap: 2,
		GateCmd:        "",
		Labels:         map[string]string{},
		OnHold:         nil,
		Paths: struct {
			Main      string `yaml:"main"`
			Worktrees string `yaml:"worktrees"`
		}{Main: "", Worktrees: ""},
	}

	if _, err := UpsertProject(path, p, nil); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// Try to add another with same key but different name
	p2 := p
	p2.Name = "Different Name"
	_, err := UpsertProject(path, p2, nil)
	if err == nil {
		t.Fatal("expected error for duplicate key")
	}
	if !contains(err.Error(), "duplicate project key") {
		t.Fatalf("error = %q, want duplicate key message", err.Error())
	}

	// Verify file is unchanged
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// Should only have one project entry
	if countLines(string(data), "  - key:") != 1 {
		t.Fatalf("file should have exactly one project, got:\n%s", string(data))
	}
}

func TestUpsertProjectStaleReadGuard(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	p := Project{
		Key:            "proj",
		Name:           "Project",
		Forge:          "github",
		Repo:           "owner/repo",
		DefaultBranch:  "main",
		Rigs:           []string{"rig-1"},
		RigTemplate:    "default",
		ConcurrencyCap: 2,
		GateCmd:        "",
		Labels:         map[string]string{},
		OnHold:         nil,
		Paths: struct {
			Main      string `yaml:"main"`
			Worktrees string `yaml:"worktrees"`
		}{Main: "", Worktrees: ""},
	}
	if _, err := UpsertProject(path, p, nil); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Read the file and compute hash for stale-read check
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read for hash: %v", err)
	}
	var h [32]byte
	h = sha256.Sum256(original)

	// Touch the file (simulate another writer) - add a comment to change content
	modified := string(original) + "\n# modified by another writer\n"
	if err := os.WriteFile(path, []byte(modified), 0o644); err != nil {
		t.Fatalf("write for touch: %v", err)
	}

	// Try to upsert again with the original hash - should refuse
	p2 := p
	p2.Name = "Updated"
	_, err = UpsertProject(path, p2, &h)
	if err == nil {
		t.Fatal("expected stale-read error")
	}
	if !contains(err.Error(), "config.yaml changed on disk") {
		t.Fatalf("error = %q, want stale-read message", err.Error())
	}
}

func TestUpsertProjectAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	// Write an original file
	original := `listen: 127.0.0.1:8787
database: fleet-console.db
projects: []
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("write original: %v", err)
	}

	// We can't easily test a forced encode failure without mocking,
	// but we can verify that on normal success no temp file remains
	p := Project{
		Key:            "proj",
		Name:           "Project",
		Forge:          "github",
		Repo:           "owner/repo",
		DefaultBranch:  "main",
		Rigs:           []string{"rig-1"},
		RigTemplate:    "default",
		ConcurrencyCap: 2,
		GateCmd:        "",
		Labels:         map[string]string{},
		OnHold:         nil,
		Paths: struct {
			Main      string `yaml:"main"`
			Worktrees string `yaml:"worktrees"`
		}{Main: "", Worktrees: ""},
	}

	if _, err := UpsertProject(path, p, nil); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Check no temp files remain in the directory
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "config.yaml" {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

// TestUpsertProjectRefusesRigCollisionBeforeWriting: the whole-file check runs
// before the rename, so a project that steals another project's rig is refused
// with the file exactly as the operator left it.
func TestUpsertProjectRefusesRigCollisionBeforeWriting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := `projects:
  - key: existing-1
    forge: github
    repo: org/one
    rigs: [rig-a]
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var err error
	if _, err = UpsertProject(path, Project{
		Key: "new-proj", Forge: "github", Repo: "org/new", Rigs: []string{"rig-a"},
	}, nil); err == nil {
		t.Fatal("want the rig collision refused")
	}
	var invalid *config.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("error = %T %v, want *config.ValidationError", err, err)
	}
	if message := invalid.Fields()["rigs"]; !strings.Contains(message, `rig "rig-a" is assigned to both`) {
		t.Fatalf("message = %q, want it to name the rig and both projects", message)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("read: %v", readErr)
	}
	if string(data) != original {
		t.Fatalf("the refused write changed the file:\n%s", data)
	}
}

// TestUpsertProjectCreatesMissingSequence: a config with no projects key gains
// one, and the rest of the file is untouched.
func TestUpsertProjectCreatesMissingSequence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := "# header\nlisten: 127.0.0.1:8787\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	outcome, err := UpsertProject(path, Project{
		Key: "proj", Forge: "github", Repo: "o/r", Rigs: []string{"rig-1"},
	}, nil)
	if err != nil || outcome != OutcomeCreated {
		t.Fatalf("upsert = %q, %v; want Created", outcome, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.HasPrefix(string(data), original) {
		t.Fatalf("the existing bytes were rewritten:\n%s", data)
	}
	if !containsAll(string(data), []string{"projects:", "- key: proj"}) {
		t.Fatalf("the projects key was not created:\n%s", data)
	}
}

// TestUpsertProjectFillsEmptySequence covers `projects: []`, the shape the
// installer writes on a box with no projects yet.
func TestUpsertProjectFillsEmptySequence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("listen: 127.0.0.1:8787\nprojects: []\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := UpsertProject(path, Project{
		Key: "proj", Forge: "github", Repo: "o/r", Rigs: []string{"rig-1"},
	}, nil); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.HasPrefix(string(data), "listen: 127.0.0.1:8787\n") {
		t.Fatalf("the listen line was rewritten:\n%s", data)
	}
	if strings.Contains(string(data), "[]") || !strings.Contains(string(data), "- key: proj") {
		t.Fatalf("the empty sequence was not filled:\n%s", data)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || contains(s[1:], substr)))
}

func containsAll(s string, substrs []string) bool {
	for _, sub := range substrs {
		if !contains(s, sub) {
			return false
		}
	}
	return true
}

func countLines(s, substr string) int {
	count := 0
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			count++
		}
	}
	return count
}
