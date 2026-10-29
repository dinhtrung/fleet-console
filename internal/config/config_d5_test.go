package config

import (
	"strings"
	"testing"
)

func TestRigsListAndSingleRigForms(t *testing.T) {
	dir := t.TempDir()
	list := writeConfig(t, dir, "projects:\n  - key: a\n    forge: github\n    repo: o/a\n    rigs: [rig-a, rig-b]\n")
	eff, err := Load(Options{FlagPath: list, Env: envMap(nil), Home: dir, Cwd: dir})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(eff.Projects) != 1 || len(eff.Projects[0].Rigs) != 2 ||
		eff.Projects[0].Rigs[0] != "rig-a" || eff.Projects[0].Rigs[1] != "rig-b" {
		t.Fatalf("rigs = %+v", eff.Projects)
	}
	printed, err := eff.Print()
	if err != nil {
		t.Fatalf("print: %v", err)
	}
	if !strings.Contains(string(printed), "rigs:") || !strings.Contains(string(printed), "rig-a") {
		t.Fatalf("print lacks the resolved list:\n%s", printed)
	}

	single := writeConfig(t, dir, "projects:\n  - key: a\n    forge: github\n    repo: o/a\n    rig: rig-a\n")
	eff, err = Load(Options{FlagPath: single, Env: envMap(nil), Home: dir, Cwd: dir})
	if err != nil {
		t.Fatalf("single: %v", err)
	}
	if len(eff.Projects[0].Rigs) != 1 || eff.Projects[0].Rigs[0] != "rig-a" {
		t.Fatalf("single rigs = %+v", eff.Projects[0].Rigs)
	}
	if eff.Projects[0].Rig != "" {
		t.Fatalf("Rig = %q, want the resolved list only", eff.Projects[0].Rig)
	}

	bad := map[string]string{
		"both":      "projects:\n  - {key: a, forge: github, repo: o/a, rig: rig-a, rigs: [rig-b]}\n",
		"empty":     "projects:\n  - {key: a, forge: github, repo: o/a, rigs: []}\n",
		"none":      "projects:\n  - {key: a, forge: github, repo: o/a}\n",
		"blank":     "projects:\n  - {key: a, forge: github, repo: o/a, rigs: [\"\"]}\n",
		"duplicate": "projects:\n  - {key: a, forge: github, repo: o/a, rigs: [rig-a, rig-a]}\n",
		"shared":    "projects:\n  - {key: a, forge: github, repo: o/a, rigs: [rig-a]}\n  - {key: b, forge: github, repo: o/b, rigs: [rig-a]}\n",
	}
	for name, body := range bad {
		path := writeConfig(t, dir, body)
		if _, err := Load(Options{FlagPath: path, Env: envMap(nil), Home: dir, Cwd: dir}); err == nil {
			t.Fatalf("%s: want an error", name)
		}
	}
}
