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
