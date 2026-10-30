package preflight

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Runtimes are the four values a project may name. The list is closed: a rig
// that starts with a runtime nobody can authenticate is three wasted seats, so
// config load and rig render refuse anything else and name these four.
const (
	RuntimePi     = "pi"
	RuntimeOMP    = "omp"
	RuntimeCodex  = "codex"
	RuntimeClaude = "claude"
)

// DefaultRuntime applies only when the project row says nothing.
const DefaultRuntime = RuntimeOMP

// Runtimes lists the accepted values in the order the surfaces show them.
var Runtimes = []string{RuntimePi, RuntimeOMP, RuntimeCodex, RuntimeClaude}

// KnownRuntime reports whether id is one of the four, case-insensitively.
func KnownRuntime(id string) bool {
	_, ok := runtimeProfileFor(id)
	return ok
}

// runtimeProfile is where one runtime's binary, credential and login live. The
// file list is a probe, never a read: a hit becomes a boolean and the bytes are
// never loaded, so no credential can reach a report.
type runtimeProfile struct {
	id        string
	loginHint string
	loginDoc  string
	envs      []string
	files     []string // relative to the home directory
}

var runtimeProfiles = map[string]runtimeProfile{
	RuntimePi: {
		id:        RuntimePi,
		loginHint: "pi auth",
		loginDoc:  "pi keeps provider credentials in ~/.pi/agent/auth.json; `pi auth` prints or checks them.",
		envs:      []string{"PI_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "LITELLM_API_KEY"},
		files:     []string{".pi/agent/auth.json"},
	},
	RuntimeOMP: {
		id:        RuntimeOMP,
		loginHint: "omp login",
		loginDoc:  "omp keeps provider credentials under ~/.omp (and per-profile when --profile is used); `omp login` is its terminal counterpart of /login.",
		// omp reads provider credentials from its store or from the environment;
		// a proxy key is a credential too, which is why the litellm variable is on
		// the list.
		envs:  []string{"OMP_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "ANTHROPIC_OAUTH_TOKEN", "LITELLM_API_KEY"},
		files: []string{".omp/agent/auth.json", ".omp/credentials.json"},
	},
	RuntimeCodex: {
		id:        RuntimeCodex,
		loginHint: "codex login",
		loginDoc:  "codex keeps its credential in ~/.codex/auth.json; `codex login` writes it and `codex logout` removes it.",
		envs:      []string{"OPENAI_API_KEY"},
		files:     []string{".codex/auth.json"},
	},
	RuntimeClaude: {
		id:        RuntimeClaude,
		loginHint: "claude auth",
		loginDoc:  "claude keeps its credential under ~/.claude; `claude auth` manages it and the in-app /login is the same store.",
		envs:      []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"},
		files:     []string{".claude/.credentials.json", ".claude.json"},
	},
}

// lookupRuntime normalizes a value to one of the four. It returns the profile
// and true only for an accepted id.
func runtimeProfileFor(id string) (runtimeProfile, bool) {
	profile, ok := runtimeProfiles[strings.ToLower(strings.TrimSpace(id))]
	return profile, ok
}

// resolveRuntime decides which runtime the checks talk about: the project row's
// value when it names one, the runtime the rig spec declares when the row is
// silent, and the default when neither says anything.
func resolveRuntime(p Project, spec *specFile) string {
	if value := strings.ToLower(strings.TrimSpace(p.Runtime)); value != "" {
		return value
	}
	if declared := spec.declaredRuntime(); declared != "" {
		return declared
	}
	return DefaultRuntime
}

// runtimeInfo probes the host for one runtime. It answers three booleans and
// two sentences; it never answers with a credential.
func runtimeInfo(deps Deps, id string) (Runtime, error) {
	profile, known := runtimeProfileFor(id)
	if !known {
		// An unknown value is not a runtime this package can describe. Say so
		// with the list rather than pretending to probe it.
		return Runtime{
			ID:        strings.TrimSpace(id),
			LoginDoc:  "unknown runtime; accepted values are pi, omp, codex and claude",
			LoginHint: "",
		}, nil
	}
	runtime := Runtime{
		ID:        profile.id,
		LoginHint: profile.loginHint,
		LoginDoc:  profile.loginDoc,
	}
	if _, err := deps.LookPath(profile.id); err == nil {
		runtime.Supported = true
	}
	for _, name := range profile.envs {
		if strings.TrimSpace(deps.Env(name)) != "" {
			runtime.AuthPresent = true
		}
	}
	// A credential store that cannot be read is not the same fact as an empty
	// one: the check reports unknown and names the directory to look at, rather
	// than telling the operator to log in when they already have.
	var probeErr error
	if !runtime.AuthPresent && deps.Home != "" {
		for _, name := range profile.files {
			path := filepath.Join(deps.Home, name)
			info, err := os.Stat(path)
			switch {
			case err == nil && info.Size() > 0:
				runtime.AuthPresent = true
			case err != nil && !os.IsNotExist(err) && probeErr == nil:
				probeErr = fmt.Errorf("%s: %w", path, err)
			}
		}
	}
	return runtime, probeErr
}
