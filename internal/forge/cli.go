package forge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// DefaultTimeout bounds one forge CLI call. A forge read crosses the network; it
// gets more room than a local rig command, but never an unbounded wait.
const DefaultTimeout = 60 * time.Second

// MarkerLabel is the in-flight marker the app owns on both forges.
const MarkerLabel = "status::ongoing"

// CLIRunner executes the forge CLI with the parent environment, so gh/glab use their
// own credential store. The app never reads a token. A zero Timeout means
// DefaultTimeout; a shorter parent deadline wins.
type CLIRunner struct {
	Timeout time.Duration
}

func (r CLIRunner) limit() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return DefaultTimeout
}

// Run executes argv[0] with the rest as arguments and returns its streams.
func (r CLIRunner) Run(ctx context.Context, argv ...string) (string, string, error) {
	if len(argv) == 0 {
		return "", "", errors.New("forge: empty command")
	}
	limit := r.limit()
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < limit {
			limit = remaining
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()

	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
	// A killed CLI can leave a child holding the output pipes; WaitDelay keeps
	// Run from waiting on that child after the deadline.
	cmd.WaitDelay = 200 * time.Millisecond
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return stdout.String(), stderr.String(), fmt.Errorf(
			"%s: timed out after %s", strings.Join(argv, " "), limit.Round(time.Millisecond))
	}
	if errors.Is(runCtx.Err(), context.Canceled) {
		return stdout.String(), stderr.String(), fmt.Errorf(
			"%s: canceled before the deadline: %w", strings.Join(argv, " "), err)
	}
	return stdout.String(), stderr.String(), err
}

var http5xx = regexp.MustCompile(`HTTP 5\d\d`)

// cli is one adapter's connection: the CLI name, its runner, and this tick's budget.
type cli struct {
	name   string
	runner Runner
	budget *Budget
}

func (c *cli) run(ctx context.Context, args ...string) ([]byte, error) {
	if c.budget != nil {
		if err := c.budget.Take(); err != nil {
			return nil, err
		}
	}
	argv := append([]string{c.name}, args...)
	stdout, stderr, err := c.runner.Run(ctx, argv...)
	if err != nil && http5xx.MatchString(stderr) {
		// One retry on a server error. The budget still counted the first attempt.
		if c.budget != nil {
			if budgetErr := c.budget.Take(); budgetErr != nil {
				return nil, budgetErr
			}
		}
		stdout, stderr, err = c.runner.Run(ctx, argv...)
	}
	if err != nil {
		return nil, classify(stderr, err)
	}
	return []byte(stdout), nil
}

// api runs `gh api`/`glab api` with the given arguments.
func (c *cli) api(ctx context.Context, args ...string) ([]byte, error) {
	return c.run(ctx, append([]string{"api"}, args...)...)
}

func classify(stderr string, err error) error {
	if errors.Is(err, ErrStale) || errors.Is(err, ErrBudget) {
		return err
	}
	message := strings.ToLower(stderr + " " + err.Error())
	switch {
	case strings.Contains(message, "http 401"), strings.Contains(message, "http 403"),
		strings.Contains(message, "rate limit"), http5xx.MatchString(strings.ToUpper(stderr)):
		return fmt.Errorf("%w: %s", ErrStale, strings.TrimSpace(stderr))
	}
	return fmt.Errorf("forge: %w: %s", err, strings.TrimSpace(stderr))
}
