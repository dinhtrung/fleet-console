// Package rig reads the OpenRig CLI through one adapter function per command
// and returns domain types. No raw map crosses this package boundary.
package rig

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// DefaultTimeout bounds one rig CLI call. A rig command reads a local daemon;
// a wedged daemon must fail the caller, not hang the request or the tick.
const DefaultTimeout = 20 * time.Second

// Runner executes one rig command and returns its stdout.
type Runner interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

// ExecRunner runs the installed rig binary. An empty Path means "rig" on PATH;
// a zero Timeout means DefaultTimeout. A shorter parent deadline wins. Env is
// additive over the parent's environment, for the child commands that need an
// identity the daemon does not have.
type ExecRunner struct {
	Path    string
	Timeout time.Duration
	Env     []string
}

func (r ExecRunner) limit() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return DefaultTimeout
}

// AsSession returns a runner for the same binary that speaks as one seat: the
// child environment carries OPENRIG_SESSION_NAME on top of the parent's. The
// rig CLI refuses the verbs that act as a seat without it (#253).
func (r ExecRunner) AsSession(session string) ExecRunner {
	r.Env = append(append([]string(nil), r.Env...), "OPENRIG_SESSION_NAME="+session)
	return r
}

// Run implements Runner.
func (r ExecRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	path := r.Path
	if path == "" {
		path = "rig"
	}
	limit := r.limit()
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < limit {
			limit = remaining
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()

	cmd := exec.CommandContext(runCtx, path, args...)
	if len(r.Env) > 0 {
		cmd.Env = append(os.Environ(), r.Env...)
	}
	// A killed CLI can leave a child holding the output pipes; WaitDelay keeps
	// Run from waiting on that child after the deadline.
	cmd.WaitDelay = 200 * time.Millisecond
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	command := strings.TrimSpace(path + " " + strings.Join(args, " "))
	if err := cmd.Run(); err != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return stdout.Bytes(), fmt.Errorf("%s: timed out after %s", command, limit.Round(time.Millisecond))
		}
		if errors.Is(runCtx.Err(), context.Canceled) {
			return stdout.Bytes(), fmt.Errorf("%s: canceled before the deadline: %w", command, err)
		}
		msg := strings.TrimSpace(stderr.String())
		// Keep stdout: commands such as `spec validate` report their verdict
		// as JSON and still exit non-zero.
		if msg == "" {
			return stdout.Bytes(), fmt.Errorf("%s: %w", command, err)
		}
		return stdout.Bytes(), fmt.Errorf("%s: %w: %s", command, err, msg)
	}
	return stdout.Bytes(), nil
}
