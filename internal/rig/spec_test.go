package rig

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

func demoProject(t *testing.T) Project {
	t.Helper()
	dir := t.TempDir()
	culture := filepath.Join(dir, "CULTURE.md")
	guidance := filepath.Join(dir, "first-move.md")
	if err := os.WriteFile(culture, []byte("# culture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(guidance, []byte("# first move\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return Project{
		Key:           "fleet",
		Rig:           "demo-rig",
		Summary:       "Two omp implementers and one reviewer.",
		RepoPath:      "/srv/repo",
		WorktreeRoot:  "/srv/worktrees",
		Builders:      2,
		Runtime:       "omp",
		BuilderModel:  "test/builder",
		ReviewerModel: "test/reviewer",
		ProviderEnv:   "TEST_API_KEY",
		ProviderKey:   "test-key-not-real",
		CulturePath:   culture,
		GuidancePath:  guidance,
	}
}

func TestRenderSpec(t *testing.T) {
	_, rendered, err := RenderRig(demoProject(t))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	golden := filepath.Join("testdata", "rig.golden.yaml")
	if *update {
		if err := os.WriteFile(golden, rendered, 0o644); err != nil {
			t.Fatalf("update golden: %v", err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden: %v (run with -update)", err)
	}
	if string(rendered) != string(want) {
		t.Fatalf("rendered spec differs from %s:\n%s", golden, rendered)
	}
}

func TestRenderRefusesZeroBuilders(t *testing.T) {
	p := demoProject(t)
	p.Builders = 0
	if _, _, err := RenderRig(p); err == nil {
		t.Fatal("render with zero builders: want error")
	}
}

func TestWriteSpecDir(t *testing.T) {
	out := os.Getenv("FLEET_RENDER_OUT")
	if out == "" {
		out = t.TempDir()
	}
	p := demoProject(t)
	p.WorktreeRoot = filepath.Join(out, "worktrees")
	p.RepoPath = filepath.Join(out, "repo")
	for _, dir := range []string{p.WorktreeRoot, p.RepoPath} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	spec, rendered, err := RenderRig(p)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	dir, err := WriteSpecDir(out, spec, rendered, p.CulturePath, p.GuidancePath, os.Getenv("FLEET_AGENTS_DIR"))
	if err != nil {
		t.Fatalf("write spec: %v", err)
	}
	for _, name := range []string{"rig.yaml", "CULTURE.md", "guidance/first-move.md"} {
		if info, err := os.Stat(filepath.Join(dir, name)); err != nil || info.Size() == 0 {
			t.Fatalf("%s missing or empty: %v", name, err)
		}
	}
	if os.Getenv("FLEET_AGENTS_DIR") != "" {
		if target, err := os.Readlink(filepath.Join(dir, "agents")); err != nil || target != os.Getenv("FLEET_AGENTS_DIR") {
			t.Fatalf("agents symlink = %q, %v", target, err)
		}
	}
}
