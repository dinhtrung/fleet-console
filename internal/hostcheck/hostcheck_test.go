package hostcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeExe(t *testing.T, dir, name string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestMissing(t *testing.T) {
	dir := t.TempDir()
	writeExe(t, dir, "rig", 0o755)
	writeExe(t, dir, "git", 0o755)
	writeExe(t, dir, "glab", 0o755)
	writeExe(t, dir, "tmux", 0o644) // not executable

	missing := Missing(dir)
	if len(missing) != 1 || !strings.HasPrefix(missing[0], "tmux") {
		t.Fatalf("missing = %v, want only tmux", missing)
	}
}

func TestMissingWithEmptyPath(t *testing.T) {
	missing := Missing("")
	if len(missing) != len(required) {
		t.Fatalf("missing = %v, want all %d requirements", missing, len(required))
	}
}

func TestForgeAcceptsEitherCLI(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"rig", "gh", "git", "tmux"} {
		writeExe(t, dir, name, 0o755)
	}
	if missing := Missing(dir); len(missing) != 0 {
		t.Fatalf("missing = %v, want none with gh present", missing)
	}
}
