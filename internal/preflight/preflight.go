// Package preflight answers four questions about a project before the app tries
// to dispatch work for it: does the repository answer, is the checkout in place,
// is the rig running the shape the project declares, and can this host
// authenticate the runtime that rig uses.
//
// Two rules shape every check:
//
//   - A check that cannot decide reports unknown and names the command that
//     would answer it. A green lie is worse than an honest unknown, because the
//     operator stops looking.
//   - Nothing here writes: no clone, no render, no launch, no login. It reads
//     facts and says what a human could do about them. The allowlisted setup
//     steps live in internal/setup, and they take the step ids this package
//     offers.
package preflight

import (
	"context"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/dinhtrung/fleet-console/internal/config"
	"github.com/dinhtrung/fleet-console/internal/rig"
)

// State is one check's verdict. Ordering matters where checks are folded into
// Ready: unknown and fail both stop a dispatch, warn does not.
type State string

const (
	OK      State = "ok"
	Warn    State = "warn"
	Fail    State = "fail"
	Unknown State = "unknown"
)

// The allowlisted setup step ids. A check's Fix names one when the app is
// allowed to perform the repair itself; internal/setup runs exactly these.
const (
	StepMkdirBase = "mkdir_base"
	StepClone     = "clone"
	StepApplyRig  = "apply_rig"
	StepUpRig     = "up_rig"
)

// The four check ids, in the order the report lists them.
const (
	CheckGitURL  = "git_url"
	CheckBaseDir = "base_dir"
	CheckRig     = "rig"
	CheckRuntime = "runtime"
)

// DefaultTimeout bounds one check. A check that outlives it is unknown, not ok.
const DefaultTimeout = 20 * time.Second

// Fix is how a human repairs a check, in the order that helps most: the exact
// command they can paste, and — when the app may do it — the allowlisted step.
type Fix struct {
	Command string // exact shell command; "" when none applies
	Step    string // one of the Step consts; "" when the app must not do it
	Why     string // one sentence, plain language
}

// Check is one fact and its verdict.
type Check struct {
	ID     string // one of the Check consts
	Label  string
	State  State
	Detail string // the fact found, with the failing command's own last line
	Fix    Fix
}

// Runtime is the chosen runtime and whether this host can authenticate it. It
// carries a boolean for the credential, never the credential.
type Runtime struct {
	ID          string // pi | omp | codex | claude
	Supported   bool   // a binary for it is on PATH
	AuthPresent bool   // a credential for it exists on this host
	LoginHint   string // the command the operator runs themselves
	LoginDoc    string // one line: where the CLI keeps it
}

// Report is the whole answer for one project.
type Report struct {
	Project   string
	Checks    []Check
	Ready     bool // every check is ok or warn
	Runtime   Runtime
	CheckedAt time.Time
}

// Project is the row facts the checks read. It is deliberately not
// config.Project: the checks need a checkout path, a rig name, a repository and
// a runtime, and keeping the shape local means a draft (a form body with no row
// yet) is checked by the same code.
type Project struct {
	Key      string
	Rig      string
	Forge    string // github | gitlab
	Repo     string // owner/name
	MainPath string // paths.main, already expanded by config.Load
	Runtime  string // pi | omp | codex | claude; "" defaults to omp
	Draft    bool   // the project has no row yet, so its rig cannot exist
	// CloneURL overrides the URL derived from Forge and Repo. A self-hosted
	// forge, or a local bare repository in a test, is reached this way.
	CloneURL      string
	DefaultBranch string // the branch dispatch works from; "" skips the branch check
	Worktrees     string // paths.worktrees, where per-item worktrees are cut
}

// FromConfig copies the row facts out of a resolved config project. The row
// carries no runtime yet — that field belongs to the setup change — so the
// runtime is left empty here and RuntimeID's default applies.
func FromConfig(p config.Project) Project {
	rigName := strings.TrimSpace(p.Rig)
	if rigName == "" && len(p.Rigs) > 0 {
		rigName = strings.TrimSpace(p.Rigs[0])
	}
	return Project{
		Key:           p.Key,
		Rig:           rigName,
		Forge:         p.Forge,
		Repo:          p.Repo,
		MainPath:      p.Paths.Main,
		DefaultBranch: p.DefaultBranch,
		Worktrees:     p.Paths.Worktrees,
	}
}

// Deps is the host's edges. Every field has a working default; a test replaces
// the ones it wants to decide.
type Deps struct {
	Home     string                       // default: the user's home
	Env      func(string) string          // default: os.Getenv
	LookPath func(string) (string, error) // default: exec.LookPath
	Git      func(ctx context.Context, args ...string) (stdout, stderr string, err error)
	Seats    func(ctx context.Context, rigName string) ([]rig.Seat, error)
	Now      func() time.Time
	Timeout  time.Duration
}

// Run checks a project against the resolved config. It is the call every
// surface makes; RunWith exists so tests can pin the host.
func Run(ctx context.Context, p Project, eff config.Effective) Report {
	return RunWith(ctx, p, eff, Deps{})
}

// RunWith runs the four checks with the injected edges.
func RunWith(ctx context.Context, p Project, eff config.Effective, deps Deps) Report {
	deps = deps.withDefaults(eff)
	checkedAt := deps.Now().UTC()

	// The spec is read once: the rig check compares against it and the runtime
	// check reports the runtime the rig actually declares.
	spec, specErr := readSpec(deps, p.Rig)

	// One probe answers both the runtime check and the report's Runtime block,
	// so the two can never disagree about this host.
	runtime, probeErr := runtimeInfo(deps, resolveRuntime(p, spec))
	checks := []Check{
		checkGitURL(ctx, deps, p),
		checkBaseDir(ctx, deps, p),
		checkRig(ctx, deps, p, spec, specErr),
		checkRuntime(ctx, deps, p, runtime, probeErr),
	}
	report := Report{Project: p.Key, Checks: checks, Runtime: runtime, CheckedAt: checkedAt}
	report.Ready = true
	for _, check := range checks {
		if check.State != OK && check.State != Warn {
			report.Ready = false
		}
	}
	return report
}

// withDefaults fills every unset edge. eff supplies the rig binary the rest of
// the app already resolved, so preflight asks the same CLI everything else does.
func (d Deps) withDefaults(eff config.Effective) Deps {
	if d.Home == "" {
		if home, err := os.UserHomeDir(); err == nil {
			d.Home = home
		} else if u, uerr := user.Current(); uerr == nil {
			d.Home = u.HomeDir
		}
	}
	if d.Env == nil {
		d.Env = os.Getenv
	}
	if d.LookPath == nil {
		d.LookPath = exec.LookPath
	}
	if d.Git == nil {
		d.Git = execGit
	}
	if d.Seats == nil {
		adapter := rig.NewAdapter(rig.ExecRunner{Path: eff.RigBinary})
		d.Seats = adapter.SeatsForRig
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Timeout <= 0 {
		d.Timeout = DefaultTimeout
	}
	return d
}

// specDir is where a rig's rendered spec lives: WriteSpecDir puts it here and
// teardown archives from here.
func specDir(home, rigName string) string {
	return filepath.Join(home, ".openrig", "specs", "rigs", rigName)
}

// SpecPath is the rig.yaml a launch names, `<home>/.openrig/specs/rigs/<rig>/
// rig.yaml`. It is the one convention WriteSpecDir writes and teardown archives
// from, so a start that cannot find it is a missing spec rather than a wrong path.
func SpecPath(home, rigName string) string {
	return filepath.Join(specDir(home, rigName), "rig.yaml")
}
