// Package git reads origin state. The only question the decide loop asks is which
// feature/<n>-* branches exist, so a dispatch can adopt or refuse existing work.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DefaultTimeout bounds one git call. ls-remote crosses the network; a hung
// origin must fail the tick, not stall it.
const DefaultTimeout = 20 * time.Second

// Runner executes one git command in a repository.
type Runner interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

// ExecRunner runs git in RepoPath. A zero Timeout means DefaultTimeout; a
// shorter parent deadline wins.
type ExecRunner struct {
	RepoPath string
	Timeout  time.Duration
}

func (r ExecRunner) limit() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return DefaultTimeout
}

// Run implements Runner.
func (r ExecRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	limit := r.limit()
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < limit {
			limit = remaining
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()

	full := append([]string{"-C", r.RepoPath}, args...)
	cmd := exec.CommandContext(runCtx, "git", full...)
	cmd.WaitDelay = 200 * time.Millisecond
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		command := "git " + strings.Join(args, " ")
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("%s: timed out after %s", command, limit.Round(time.Millisecond))
		}
		if errors.Is(runCtx.Err(), context.Canceled) {
			return nil, fmt.Errorf("%s: canceled before the deadline: %w", command, err)
		}
		return nil, fmt.Errorf("%s: %w: %s", command, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// Branch is one origin head that belongs to an item.
type Branch struct {
	Name string `json:"name"`
	Item int    `json:"item"`
}

var featureBranch = regexp.MustCompile(`^refs/heads/feature/(\d+)-`)

// ParseRemoteHeads reads `git ls-remote --heads origin` output. Only feature/<n>-*
// heads are returned, sorted by name.
func ParseRemoteHeads(stdout []byte) []Branch {
	var out []Branch
	for _, line := range strings.Split(string(stdout), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		ref := fields[len(fields)-1]
		match := featureBranch.FindStringSubmatch(ref)
		if match == nil {
			continue
		}
		number, err := strconv.Atoi(match[1])
		if err != nil || number <= 0 {
			continue
		}
		out = append(out, Branch{Name: strings.TrimPrefix(ref, "refs/heads/"), Item: number})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Index reads the origin branch index.
func Index(ctx context.Context, runner Runner) ([]Branch, error) {
	out, err := runner.Run(ctx, "ls-remote", "--heads", "origin")
	if err != nil {
		return nil, err
	}
	return ParseRemoteHeads(out), nil
}
