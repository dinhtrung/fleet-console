package configfile_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/config"
	"github.com/dinhtrung/fleet-console/internal/configfile"
)

// The projects route writes a flat rigs list; the loader must read it back as
// tier-0 entries. This is the round-trip the brief asks for, through the real
// writer rather than a hand-built document. It lives in this package because
// configfile imports config, so the reverse would be an import cycle.
func TestRigsRoundTripThroughUpsertProject(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	original := `# hand-written header
projects:
  - key: existing
    forge: github
    repo: o/existing
    rigs: [rig-a]
`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	written := configfile.Project{
		Key:           "app",
		Forge:         "github",
		Repo:          "o/app",
		DefaultBranch: "main",
		Rigs:          []string{"primary", "standby"},
	}
	if _, err := configfile.UpsertProject(path, written, nil); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(raw), "# hand-written header") {
		t.Fatalf("the header comment did not survive the write:\n%s", raw)
	}

	eff, err := config.Load(config.Options{FlagPath: path, Cwd: dir, Home: dir})
	if err != nil {
		t.Fatalf("Load after write: %v\n%s", err, raw)
	}
	var found *config.Project
	for i := range eff.Projects {
		if eff.Projects[i].Key == "app" {
			found = &eff.Projects[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("project app missing after round-trip; projects = %+v", eff.Projects)
	}
	if names := found.Rigs.Names(); len(names) != 2 || names[0] != "primary" || names[1] != "standby" {
		t.Fatalf("rigs = %v, want [primary standby] in order", names)
	}
	for i, ref := range found.Rigs {
		if ref.Tier != 0 || ref.Spill != config.SpillOnFull {
			t.Fatalf("rigs[%d] = %+v, want tier 0 spill on-full from a flat list", i, ref)
		}
	}
}
