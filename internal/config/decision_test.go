package config

import "testing"

// The feature is off until the operator turns it on: an upgraded box with no
// decision_notes key must not start commenting on items (design D8).
func TestDecisionNotesDefaultsOff(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "tick_interval: 15s\n")
	eff, err := Load(Options{FlagPath: path, Env: envMap(nil), Home: dir, Cwd: dir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if eff.DecisionNotes {
		t.Fatal("decision_notes = true with no key; an upgraded box must not comment on items")
	}
	if eff.Sources["decision_notes"] != "default" {
		t.Fatalf("source = %q, want default", eff.Sources["decision_notes"])
	}
	if len(eff.DecisionAnswerers) != 0 {
		t.Fatalf("decision_answerers = %v, want empty (the repo owner is the default)", eff.DecisionAnswerers)
	}
	if eff.Sources["decision_answerers"] != "default" {
		t.Fatalf("source = %q, want default", eff.Sources["decision_answerers"])
	}
}

func TestDecisionNotesFromFile(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "tick_interval: 15s\ndecision_notes: true\ndecision_answerers: [dinhtrung, helper]\n")
	eff, err := Load(Options{FlagPath: path, Env: envMap(nil), Home: dir, Cwd: dir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !eff.DecisionNotes {
		t.Fatal("decision_notes = false; the file set it true")
	}
	if eff.Sources["decision_notes"] != "file" {
		t.Fatalf("source = %q, want file", eff.Sources["decision_notes"])
	}
	want := []string{"dinhtrung", "helper"}
	if len(eff.DecisionAnswerers) != len(want) {
		t.Fatalf("decision_answerers = %v, want %v", eff.DecisionAnswerers, want)
	}
	for i, login := range want {
		if eff.DecisionAnswerers[i] != login {
			t.Fatalf("decision_answerers[%d] = %q, want %q", i, eff.DecisionAnswerers[i], login)
		}
	}
	if eff.Sources["decision_answerers"] != "file" {
		t.Fatalf("source = %q, want file", eff.Sources["decision_answerers"])
	}
}

// The print view carries both keys with their sources, so --print-config answers
// "is the app going to comment on my items?" without reading the file.
func TestDecisionSettingsPrint(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "tick_interval: 15s\ndecision_notes: true\n")
	eff, err := Load(Options{FlagPath: path, Env: envMap(nil), Home: dir, Cwd: dir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	printed, err := eff.Print()
	if err != nil {
		t.Fatalf("print: %v", err)
	}
	for _, want := range []string{"decision_notes: true", "decision_notes: file", "decision_answerers: default"} {
		if !contains(string(printed), want) {
			t.Fatalf("print output missing %q:\n%s", want, printed)
		}
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
