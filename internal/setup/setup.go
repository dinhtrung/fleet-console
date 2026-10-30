// Package setup runs the allowlisted steps that bring a project's checkout and
// rig into existence: mkdir_base, clone, apply_rig, up_rig.
//
// Two rules shape it. Idempotency is the point, not a nicety: every step reports
// skipped when its work is already done, so running the whole chain twice is
// four skipped steps and a second run is never a second clone. And nothing here
// logs in or writes a credential: the steps prepare a directory, a checkout, a
// rendered spec and a running rig, and a human does the rest.
package setup

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// Step ids, in the fixed order they run. The list is closed: a request naming
// anything else is refused rather than ignored.
const (
	StepMkdirBase = "mkdir_base"
	StepClone     = "clone"
	StepApplyRig  = "apply_rig"
	StepUpRig     = "up_rig"
)

// Order is the only order the steps run in.
var Order = []string{StepMkdirBase, StepClone, StepApplyRig, StepUpRig}

// State is what a step did.
type State string

const (
	StateDone    State = "done"
	StateSkipped State = "skipped"
	StateFailed  State = "failed"
)

// Result is one step's outcome: the command it ran and the last line it printed.
type Result struct {
	Step    string `json:"step"`
	State   State  `json:"state"`
	Command string `json:"command"`
	Output  string `json:"output"`
}

// Input is everything the chain needs, resolved before it starts.
type Input struct {
	BaseDir   string // paths.main: the checkout
	RepoURL   string // what to clone
	RigName   string
	Project   rig.Project // rendered into the rig spec by apply_rig
	Home      string      // where ~/.openrig/specs lives
	Culture   string      // copied into the spec dir when apply_rig writes
	Guidance  string
	AgentsDir string
	RigBinary string
}

// Rig is the rig surface the chain uses. Tests supply a fake; the app uses
// Adapter.
type Rig interface {
	// LiveSeats reports how many seats a rig currently has: zero means it is not
	// up, which is what up_rig checks before launching.
	LiveSeats(ctx context.Context, rigName string) (int, error)
	SpecsSync(ctx context.Context) error
	Up(ctx context.Context, specPath string) error
}

// Runner runs the chain. Every field is a seam so a test needs no real git, no
// real rig and no real home directory.
type Runner struct {
	Store *store.Store
	Rig   Rig
	// Exec runs one external command and returns its combined output. It is
	// never used for a login: no step below builds such a command.
	Exec func(ctx context.Context, name string, args ...string) (string, error)
	Now  func() time.Time
}

// Run executes steps in the allowlisted order and stops at the first failure,
// because the next step would only fail worse. An empty steps slice means the
// whole list.
func (r *Runner) Run(ctx context.Context, in Input, steps []string) ([]Result, error) {
	chosen, err := allowlisted(steps)
	if err != nil {
		return nil, err
	}
	results := make([]Result, 0, len(chosen))
	for _, step := range chosen {
		result, err := r.one(ctx, in, step)
		results = append(results, result)
		if r.Store != nil && result.Step != "" {
			// The audit row is written for every step that ran, including a
			// failure: the failing command is the one an operator needs.
			if _, auditErr := r.Store.AppendEvent(ctx, store.Event{
				Actor: "setup", Kind: "setup.step",
				Subject: result.Step, Detail: auditDetail(result),
			}); auditErr != nil {
				return results, fmt.Errorf("setup: audit %s: %w", result.Step, auditErr)
			}
		}
		if err != nil || result.State == StateFailed {
			return results, nil
		}
	}
	return results, nil
}

// allowlisted returns the requested steps in canonical order, refusing any id
// outside the four.
func allowlisted(steps []string) ([]string, error) {
	if len(steps) == 0 {
		return append([]string(nil), Order...), nil
	}
	wanted := make(map[string]bool, len(steps))
	for _, step := range steps {
		step = strings.TrimSpace(step)
		if !known(step) {
			return nil, fmt.Errorf("setup: %q is not one of %s", step, strings.Join(Order, ", "))
		}
		wanted[step] = true
	}
	out := make([]string, 0, len(wanted))
	for _, step := range Order {
		if wanted[step] {
			out = append(out, step)
		}
	}
	return out, nil
}

func known(step string) bool {
	for _, candidate := range Order {
		if candidate == step {
			return true
		}
	}
	return false
}

// one runs a single step. A step reports its own state; the error return is only
// for a failure the step could not describe as a Result.
func (r *Runner) one(ctx context.Context, in Input, step string) (Result, error) {
	switch step {
	case StepMkdirBase:
		return r.mkdirBase(in), nil
	case StepClone:
		return r.clone(ctx, in), nil
	case StepApplyRig:
		return r.applyRig(ctx, in), nil
	case StepUpRig:
		return r.upRig(ctx, in), nil
	default:
		return Result{}, fmt.Errorf("setup: unknown step %q", step)
	}
}

// mkdirBase creates the checkout directory. An existing directory is skipped:
// the operator may already have pointed paths.main at a real checkout.
func (r *Runner) mkdirBase(in Input) Result {
	result := Result{Step: StepMkdirBase, Command: "mkdir -p " + in.BaseDir}
	if info, err := os.Stat(in.BaseDir); err == nil {
		if info.IsDir() {
			result.State = StateSkipped
			result.Output = in.BaseDir + " exists"
			return result
		}
		result.State = StateFailed
		result.Output = in.BaseDir + " exists and is not a directory"
		return result
	}
	if err := os.MkdirAll(in.BaseDir, 0o755); err != nil {
		result.State = StateFailed
		result.Output = err.Error()
		return result
	}
	result.State = StateDone
	result.Output = "created " + in.BaseDir
	return result
}

// clone fills an empty directory. A non-empty directory is skipped, never
// cloned over: a second clone into a checkout with work in it is the one thing
// this step must never do.
func (r *Runner) clone(ctx context.Context, in Input) Result {
	result := Result{Step: StepClone, Command: fmt.Sprintf("git clone %s %s", in.RepoURL, in.BaseDir)}
	empty, err := emptyDir(in.BaseDir)
	if err != nil {
		result.State = StateFailed
		result.Output = err.Error()
		return result
	}
	if !empty {
		result.State = StateSkipped
		result.Output = in.BaseDir + " is not empty; not cloning over it"
		return result
	}
	if strings.TrimSpace(in.RepoURL) == "" {
		result.State = StateFailed
		result.Output = "no repository URL is configured"
		return result
	}
	output, err := r.exec(ctx, "git", "clone", in.RepoURL, in.BaseDir)
	result.Output = lastLine(output)
	if err != nil {
		result.State = StateFailed
		if result.Output == "" {
			result.Output = err.Error()
		}
		return result
	}
	result.State = StateDone
	return result
}

// applyRig renders the rig spec and syncs it. Identical rendered bytes are
// skipped before anything is written, so a second run does not re-render and a
// hand-edited spec is left alone until the operator asks for a reapply.
func (r *Runner) applyRig(ctx context.Context, in Input) Result {
	result := Result{Step: StepApplyRig, Command: "rig specs sync"}
	// CulturePath is the file the rig copies into its spec dir, so a caller that
	// supplied one under `Culture` has said everything the renderer needs.
	project := in.Project
	if strings.TrimSpace(project.CulturePath) == "" {
		project.CulturePath = strings.TrimSpace(in.Culture)
	}
	// Nothing here may invent a culture file — the culture editor's rule is that
	// a missing one is explained, not created. So when there is none, name the
	// field the operator has to set instead of passing on the renderer's "culture
	// file is required", which gives them nothing to act on.
	if strings.TrimSpace(project.CulturePath) == "" {
		result.State = StateFailed
		result.Output = "no culture file: set culture_file on the project, or create CULTURE.md under its rig's spec directory; the app never invents one"
		return result
	}
	spec, rendered, err := rig.RenderRig(project)
	if err != nil {
		result.State = StateFailed
		result.Output = err.Error()
		return result
	}
	dir := filepath.Join(in.Home, ".openrig", "specs", "rigs", spec.Name)
	target := filepath.Join(dir, "rig.yaml")
	if existing, readErr := os.ReadFile(target); readErr == nil && string(existing) == string(rendered) {
		result.State = StateSkipped
		result.Output = target + " is already current"
		return result
	}
	path, err := rig.WriteSpecDir(in.Home, spec, rendered, in.Culture, in.Guidance, in.AgentsDir)
	if err != nil {
		result.State = StateFailed
		result.Output = err.Error()
		return result
	}
	if r.Rig != nil {
		if err := r.Rig.SpecsSync(ctx); err != nil {
			result.State = StateFailed
			result.Command = in.RigBinary + " specs sync"
			result.Output = lastLine(err.Error())
			return result
		}
	}
	result.State = StateDone
	result.Output = "wrote " + path
	return result
}

// upRig launches the rig. A rig that already has seats is skipped: launching it
// again would either fail or duplicate a fleet.
func (r *Runner) upRig(ctx context.Context, in Input) Result {
	result := Result{Step: StepUpRig, Command: fmt.Sprintf("%s up %s", in.RigBinary, in.RigName)}
	if r.Rig == nil {
		result.State = StateFailed
		result.Output = "no rig adapter is configured"
		return result
	}
	seats, err := r.Rig.LiveSeats(ctx, in.RigName)
	if err != nil {
		result.State = StateFailed
		result.Output = lastLine(err.Error())
		return result
	}
	if seats > 0 {
		result.State = StateSkipped
		result.Output = fmt.Sprintf("%s is already up (%d seats)", in.RigName, seats)
		return result
	}
	specPath := filepath.Join(in.Home, ".openrig", "specs", "rigs", in.RigName, "rig.yaml")
	if err := r.Rig.Up(ctx, specPath); err != nil {
		result.State = StateFailed
		result.Output = lastLine(err.Error())
		return result
	}
	result.State = StateDone
	return result
}

func (r *Runner) exec(ctx context.Context, name string, args ...string) (string, error) {
	if r.Exec != nil {
		return r.Exec(ctx, name, args...)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	combined, err := cmd.CombinedOutput()
	if err != nil {
		return string(combined), fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(combined), nil
}

// emptyDir reports whether dir is absent or holds nothing.
func emptyDir(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}
	return len(entries) == 0, nil
}

// lastLine is the one line a human reads: the command's own last non-empty
// line, not a summary of it.
func lastLine(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if trimmed := strings.TrimSpace(lines[index]); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// auditDetail is what the event row carries: the command and the last line, so
// the trail answers "what ran and what did it say".
//
// A skipped step names no command, because it ran none — a `git clone` in the
// trail means a clone happened, and a reader counting clones must not have to
// filter by state to find out.
func auditDetail(result Result) string {
	if result.State == StateSkipped {
		if result.Output == "" {
			return "skipped"
		}
		return "skipped: " + result.Output
	}
	if result.Output == "" {
		return result.Command
	}
	return result.Command + " -> " + result.Output
}
