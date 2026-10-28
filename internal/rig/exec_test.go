package rig

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestRunTimesOut is the regression for a wedged local CLI: the call must return
// the deadline error, not hang the request.
func TestRunTimesOut(t *testing.T) {
	runner := ExecRunner{Path: "/bin/sh", Timeout: 100 * time.Millisecond}
	start := time.Now()
	_, err := runner.Run(context.Background(), "-c", "sleep 5")
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

// TestRunHonorsShorterParentDeadline: a caller's earlier deadline is the one the
// error names.
func TestRunHonorsShorterParentDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	runner := ExecRunner{Path: "/bin/sh", Timeout: 5 * time.Second}
	_, err := runner.Run(ctx, "-c", "sleep 5")
	if err == nil || !strings.Contains(err.Error(), "timed out after 80ms") {
		t.Fatalf("error = %v, want the parent's 80ms deadline", err)
	}
}

func TestRunReturnsOutputUnderTheDeadline(t *testing.T) {
	runner := ExecRunner{Path: "/bin/echo", Timeout: time.Second}
	out, err := runner.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.TrimSpace(string(out)) != "hello" {
		t.Fatalf("stdout = %q", out)
	}
}

func TestDefaultTimeout(t *testing.T) {
	if got := (ExecRunner{}).limit(); got != DefaultTimeout {
		t.Fatalf("limit = %s, want %s", got, DefaultTimeout)
	}
	if got := (ExecRunner{Timeout: 3 * time.Second}).limit(); got != 3*time.Second {
		t.Fatalf("limit = %s, want the configured 3s", got)
	}
}
