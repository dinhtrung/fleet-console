package rig

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestStatusReadsTheDaemonPort: the panel links to the UI, so the URL comes from
// the daemon's own answer rather than a guess (#429).
func TestStatusReadsTheDaemonPort(t *testing.T) {
	runner := &scriptedRunner{fn: func(args []string) ([]byte, error) {
		if strings.Join(args, " ") != "status --json" {
			return nil, errors.New("unexpected call: " + strings.Join(args, " "))
		}
		return []byte(`{"daemon":{"state":"running","reachable":true,"port":7433},
			"kernel":{"state":"ready"},"rigs":[{"name":"r"}]}`), nil
	}}
	report, err := NewAdapter(runner).Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if got := report.UIURL(); got != "http://127.0.0.1:7433" {
		t.Fatalf("UIURL = %q, want the daemon's own port", got)
	}
	if report.Daemon.State != "running" || !report.Daemon.Reachable {
		t.Fatalf("report = %+v, want the daemon block", report)
	}
}

// TestStatusWithoutAPortHasNoURL: an older rig (or one that does not listen)
// prints no port, and the surface then has no link to invent.
func TestStatusWithoutAPortHasNoURL(t *testing.T) {
	runner := &scriptedRunner{fn: func([]string) ([]byte, error) {
		return []byte(`{"daemon":{"state":"stopped","reachable":false}}`), nil
	}}
	report, err := NewAdapter(runner).Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if got := report.UIURL(); got != "" {
		t.Fatalf("UIURL = %q, want none without a port", got)
	}
}

// TestStatusClassifiesADeadDaemon: the read keeps the one classification every
// other call has, so a caller can tell "no daemon" from "bad answer".
func TestStatusClassifiesADeadDaemon(t *testing.T) {
	runner := &scriptedRunner{fn: func([]string) ([]byte, error) {
		// ExecRunner embeds the CLI's stderr in the error text, which is what
		// the one classification rule reads.
		return nil, fmt.Errorf("exit status 1: Error: Daemon not running. This command needs a running daemon.")
	}}
	_, err := NewAdapter(runner).Status(context.Background())
	if !errors.Is(err, ErrDaemonDown) {
		t.Fatalf("status error = %v, want ErrDaemonDown", err)
	}
}
