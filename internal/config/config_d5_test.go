package config

import (
	"os"
	"testing"

)

func TestRigAndRigs(t *testing.T) {
	// Both set → error
	_, err := Load(Options{
		Cwd: "/tmp",
		Env: func(s string) string { return "" },
		Flags: map[string]string{},
		Home: "/tmp",
	})
	// Minimal config with both rig and rigs
	d := t.TempDir()
	f := d + "/config.yaml"
	content := `
projects:
  - key: p1
    forge: github
    repo: r/o
    rig: r1
    rigs: [r2]
`
	os.WriteFile(f, []byte(content), 0600)
	_, err = Load(Options{FlagPath: f, Env: func(s string) string { return "" }})
	if err == nil {
		t.Fatal("expected error for both rig and rigs")
	}
}
