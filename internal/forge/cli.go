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
	Binaries map[string]string
	Timeout  time.Duration
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
	if binary := r.Binaries[argv[0]]; binary != "" {
		argv = append([]string{binary}, argv[1:]...)
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
	return c.call(ctx, "", args...)
}

// write runs one forge write call. op is the short operation name the write
// method declares (comment, ready, merge, close, label, create_pr), which a
// permission refusal carries (#258). Reads go through run and keep classify's
// own words; the distinction matters because a write refusal is a permission
// fact where the same status on a read is not.
func (c *cli) write(ctx context.Context, op string, args ...string) ([]byte, error) {
	return c.call(ctx, op, args...)
}

func (c *cli) call(ctx context.Context, op string, args ...string) ([]byte, error) {
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
		return nil, classify(c.forge(), op, operationName(argv), stderr, err)
	}
	return []byte(stdout), nil
}

// forge reports the adapter's kind from the CLI name: gh → github, glab → gitlab.
func (c *cli) forge() string {
	if c.name == "glab" {
		return string(KindGitLab)
	}
	return string(KindGitHub)
}

// operationName is the short form the forbidden error names: the API path for a
// read, the verb and target for a write, never a comment body.
func operationName(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	switch argv[0] {
	case "gh", "glab":
		if len(argv) >= 3 && argv[1] == "api" {
			return argv[0] + " api " + argv[2]
		}
		if len(argv) >= 4 {
			return strings.Join(argv[:4], " ")
		}
	}
	return strings.Join(argv, " ")
}

// api runs `gh api`/`glab api` with the given arguments.
func (c *cli) api(ctx context.Context, args ...string) ([]byte, error) {
	return c.run(ctx, append([]string{"api"}, args...)...)
}

// classify turns one failed CLI call into the error the rest of the app sees.
// A declared write operation (op, empty for a read) is asked of each adapter's
// own permission patterns first: a refusal the token caused is a
// PermissionError (#258). Reads keep the established words — a 401 is stale, a
// 403 is a ForbiddenError the mirror reports in its line.
func classify(forge string, op, operation, stderr string, err error) error {
	if errors.Is(err, ErrStale) || errors.Is(err, ErrBudget) || errors.Is(err, ErrForbidden) {
		return err
	}
	message := strings.ToLower(stderr + " " + err.Error())
	// A GitHub rate limit is a 403, so it must be classified before the
	// forbidden case: retrying after the window can succeed. The same order
	// keeps a rate-limited write from reading as a permission refusal.
	switch {
	case strings.Contains(message, "rate limit"), strings.Contains(message, "abuse detection"):
		return fmt.Errorf("%w: %s", ErrStale, strings.TrimSpace(stderr))
	case http5xx.MatchString(strings.ToUpper(stderr)):
		return fmt.Errorf("%w: %s", ErrStale, strings.TrimSpace(stderr))
	}
	if op != "" && permissionRefusal(forge, stderr, err) {
		detail := strings.TrimSpace(stderr)
		if detail == "" {
			detail = err.Error()
		}
		return &PermissionError{Operation: op, Forge: forge, Detail: detail}
	}
	switch {
	case strings.Contains(message, "http 401"):
		return fmt.Errorf("%w: %s", ErrStale, strings.TrimSpace(stderr))
	case strings.Contains(message, "http 403"), strings.Contains(message, "403 forbidden"):
		return &ForbiddenError{Operation: operation, Detail: strings.TrimSpace(stderr)}
	}
	return fmt.Errorf("forge: %w: %s", err, strings.TrimSpace(stderr))
}

// permissionRefusal answers whether this forge refused a write because the
// credential may not do it. The patterns are each adapter's own words: the
// github list lives in github.go, the gitlab list in gitlab.go, so a forge
// inventing a new refusal sentence is named in the adapter that speaks it.
func permissionRefusal(forge string, stderr string, err error) bool {
	if forge == string(KindGitLab) {
		return gitlabPermissionRefusal(stderr, err)
	}
	return githubPermissionRefusal(stderr, err)
}
