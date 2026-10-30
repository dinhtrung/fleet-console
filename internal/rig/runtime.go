package rig

import "strings"

// The four runtimes a project may name. The list is closed: a rig that starts
// with a runtime nobody can authenticate is three wasted seats, so config load
// and RenderRig both refuse anything else and name these four. It lives here
// because every package that needs it already depends on rig, and one list
// cannot drift from another.
const (
	RuntimePi     = "pi"
	RuntimeOMP    = "omp"
	RuntimeCodex  = "codex"
	RuntimeClaude = "claude"
)

// DefaultRuntime applies when the project names none, so an existing config
// keeps working.
const DefaultRuntime = RuntimeOMP

// Runtimes lists the accepted values in the order the surfaces show them.
var Runtimes = []string{RuntimePi, RuntimeOMP, RuntimeCodex, RuntimeClaude}

// RuntimesText is the four values as a sentence fragment, for an error that has
// to name them.
const RuntimesText = "pi, omp, codex, claude"

// KnownRuntime reports whether id is one of the four, case-insensitively, and
// returns the canonical lowercase form to store.
func KnownRuntime(id string) (string, bool) {
	value := strings.ToLower(strings.TrimSpace(id))
	for _, candidate := range Runtimes {
		if value == candidate {
			return candidate, true
		}
	}
	return "", false
}
