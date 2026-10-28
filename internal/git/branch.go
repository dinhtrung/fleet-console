// Package git reads origin state. The only question the decide loop asks is which
// feature/<n>-* branches exist, so a dispatch can adopt or refuse existing work.
package git

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Runner executes one git command in a repository.
type Runner interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

// ExecRunner runs git in RepoPath.
type ExecRunner struct {
	RepoPath string
}

// Run implements Runner.
func (r ExecRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	full := append([]string{"-C", r.RepoPath}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
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
