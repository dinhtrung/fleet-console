package forge

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestCLIRunnerTimesOut: a wedged forge CLI returns the deadline error with the
// command and the deadline named.
func TestCLIRunnerTimesOut(t *testing.T) {
	runner := CLIRunner{Timeout: 100 * time.Millisecond}
	start := time.Now()
	_, _, err := runner.Run(context.Background(), "/bin/sh", "-c", "sleep 5")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Run returned nil, want a timeout error")
	}
	t.Logf("timeout error after %s: %v", elapsed, err)
	if !strings.Contains(err.Error(), "timed out after 100ms") {
		t.Fatalf("error = %q, want the deadline", err)
	}
	if !strings.Contains(err.Error(), "sleep 5") {
		t.Fatalf("error = %q, want the command named", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Run took %s, want the 100ms deadline", elapsed)
	}
}

func TestCLIRunnerUsesTheDefaultTimeout(t *testing.T) {
	if got := (CLIRunner{}).limit(); got != DefaultTimeout {
		t.Fatalf("limit = %s, want %s", got, DefaultTimeout)
	}
	if DefaultTimeout <= 0 {
		t.Fatalf("DefaultTimeout = %s, want a positive bound", DefaultTimeout)
	}
}

// TestCLIRunnerEnv pins the env channel (forge-host D2): an env-carrying
// runner's child sees the variable, on top of the parent's environment, and an
// empty Env leaves the child exactly the inheritance it always had.
func TestCLIRunnerEnv(t *testing.T) {
	runner := CLIRunner{Timeout: 5 * time.Second, Env: []string{"GITLAB_HOST=gitlab.horisen.org"}}
	stdout, _, err := runner.Run(context.Background(), "/bin/sh", "-c", `printf %s "$GITLAB_HOST"`)
	if err != nil {
		t.Fatalf("Run with env: %v", err)
	}
	if stdout != "gitlab.horisen.org" {
		t.Fatalf("child saw GITLAB_HOST = %q, want gitlab.horisen.org", stdout)
	}

	bare := CLIRunner{Timeout: 5 * time.Second}
	stdout, _, err = bare.Run(context.Background(), "/bin/sh", "-c", `printf %s "${GITLAB_HOST-unset}"`)
	if err != nil {
		t.Fatalf("Run without env: %v", err)
	}
	if stdout != "unset" {
		t.Fatalf("child without Env saw GITLAB_HOST = %q, want nothing set", stdout)
	}

	// WithEnv copies: the receiver keeps its own env, the copy carries both.
	base := CLIRunner{Env: []string{"A=1"}}
	copied, ok := base.WithEnv([]string{"B=2"}).(CLIRunner)
	if !ok {
		t.Fatalf("WithEnv returned %T, want a CLIRunner value", base.WithEnv([]string{"B=2"}))
	}
	if len(base.Env) != 1 || base.Env[0] != "A=1" {
		t.Fatalf("base env = %v, want unchanged", base.Env)
	}
	if len(copied.Env) != 2 || copied.Env[0] != "A=1" || copied.Env[1] != "B=2" {
		t.Fatalf("copy env = %v, want A=1 then B=2", copied.Env)
	}
}
