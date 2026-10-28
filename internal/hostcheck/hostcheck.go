// Package hostcheck verifies the host tools the binary drives. The binary
// declares these dependencies; it never bundles them.
package hostcheck

import (
	"os"
	"path/filepath"
	"strings"
)

type requirement struct {
	name     string
	binaries []string
}

var required = []requirement{
	{name: "rig", binaries: []string{"rig"}},
	{name: "forge CLI", binaries: []string{"gh", "glab"}},
	{name: "git", binaries: []string{"git"}},
	{name: "tmux", binaries: []string{"tmux"}},
}

// Missing returns one entry per requirement that has no executable in pathEnv.
// The text names the requirement and its accepted binaries.
func Missing(pathEnv string) []string {
	dirs := filepath.SplitList(pathEnv)
	var missing []string
	for _, req := range required {
		if !anyExecutable(dirs, req.binaries) {
			missing = append(missing, req.name+" ("+strings.Join(req.binaries, " or ")+")")
		}
	}
	return missing
}

func anyExecutable(dirs, names []string) bool {
	for _, name := range names {
		for _, dir := range dirs {
			if dir == "" {
				dir = "."
			}
			info, err := os.Stat(filepath.Join(dir, name))
			if err == nil && !info.IsDir() && info.Mode().Perm()&0o111 != 0 {
				return true
			}
		}
	}
	return false
}
