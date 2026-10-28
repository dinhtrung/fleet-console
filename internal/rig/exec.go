// Package rig reads the OpenRig CLI through one adapter function per command
// and returns domain types. No raw map crosses this package boundary.
package rig

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Runner executes one rig command and returns its stdout.
type Runner interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

// ExecRunner runs the installed rig binary. An empty Path means "rig" on PATH.
type ExecRunner struct {
	Path string
}

// Run implements Runner.
func (r ExecRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	path := r.Path
	if path == "" {
		path = "rig"
	}
	cmd := exec.CommandContext(ctx, path, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		// Keep stdout: commands such as `spec validate` report their verdict
		// as JSON and still exit non-zero.
		if msg == "" {
			return stdout.Bytes(), fmt.Errorf("%s %s: %w", path, strings.Join(args, " "), err)
		}
		return stdout.Bytes(), fmt.Errorf("%s %s: %w: %s", path, strings.Join(args, " "), err, msg)
	}
	return stdout.Bytes(), nil
}
