package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/act"
	"github.com/dinhtrung/fleet-console/internal/rig"
)

// errDaemonDown is the shape the rig CLI returns outside a running daemon,
// recorded from the production boot on 2026-11-04.
var errDaemonDown = errors.New(
	"exit status 1: Error: Daemon not running.\n  This command needs a running daemon.\n  Run 'rig up' (it auto-starts the daemon), or 'rig daemon start'.")

// flakyRunner answers like a rig CLI whose daemon comes up late: every call
// fails until allow is set, then the recorded 0.7.0 fixtures are served. It
// models the cold-boot race the board lost, where the probe ran a minute before
// the daemon existed.
type flakyRunner struct {
	mu    sync.Mutex
	allow bool
	data  map[string][]byte
}

func (f *flakyRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.allow {
		return nil, errDaemonDown
	}
	data, ok := f.data[strings.Join(args, " ")]
	if !ok {
		return nil, os.ErrNotExist
	}
	return data, nil
}

func (f *flakyRunner) startDaemon() {
	f.mu.Lock()
	f.allow = true
	f.mu.Unlock()
}

func probeFixtures(t *testing.T) map[string][]byte {
	t.Helper()
	read := func(name string) []byte {
		data, err := os.ReadFile(filepath.Join("..", "..", "internal", "rig", "testdata", rig.Floor, name))
		if err != nil {
			t.Fatalf("read fixture %s: %v", name, err)
		}
		return data
	}
	return map[string][]byte{
		"--version":                   []byte("0.7.0 (c99450f3)\n"),
		"queue list -A -a --json":     read("queue-list.json"),
		"ps --nodes -A --full --json": read("ps-nodes.json"),
	}
}

// TestCompatReopensWhenTheRigComesUp is the acceptance for #130: a probe that
// fails at boot and succeeds later must leave the gate open on the next read,
// with no restart. The first read is counted as the transition, and the reopen
// is logged exactly once however many times the state is read afterwards.
func TestCompatReopensWhenTheRigComesUp(t *testing.T) {
	runner := &flakyRunner{data: probeFixtures(t)}
	var logs bytes.Buffer
	state := newCompatState(rig.NewProbe(runner, func(string, ...any) {}), &logs)

	// The board's boot read: the daemon is not up yet, so the gate refuses.
	if report := state.current(); !report.Blocked() {
		t.Fatalf("boot report OK = %v want blocked; error %q", report.OK, report.Error)
	}
	if err := act.NewGate(state.current()).Check(); err == nil {
		t.Fatal("gate allowed the act loop while the daemon was down")
	}

	// The mirror's next tick: the daemon answers now. The gate was still closed,
	// so the read re-probes and the refusal must not survive the daemon.
	runner.startDaemon()
	report := state.current()
	if !report.OK {
		t.Fatalf("report after the daemon came up = %q (%s), want ok", report.Status, report.Error)
	}
	if err := act.NewGate(report).Check(); err != nil {
		t.Fatalf("gate = %v, want open once the probe passes", err)
	}

	// Once open the state is a no-op: the reopen line is logged exactly once,
	// and later reads serve the recovered report, never the boot one.
	for i := range 3 {
		if got := state.current(); !got.OK {
			t.Fatalf("read %d after reopen = %q, want the recovered report", i, got.Status)
		}
	}
	if n := strings.Count(logs.String(), "act loop open"); n != 1 {
		t.Fatalf("reopen logged %d times, want exactly 1:\n%s", n, logs.String())
	}
}

// TestCompatStaysClosedForAnIncompatibleRig is the other arm: a rig that answers
// but whose shape is wrong — the renamed-field case runtime-compat names — must
// never let the gate open, and every read must still name the failing command
// and field. Re-probing must not turn a real incompatibility into a pass.
func TestCompatStaysClosedForAnIncompatibleRig(t *testing.T) {
	data := probeFixtures(t)
	renamed, err := os.ReadFile(filepath.Join("..", "..", "internal", "rig", "testdata", rig.Floor, "queue-list-renamed-pickup.json"))
	if err != nil {
		t.Fatalf("read renamed fixture: %v", err)
	}
	data["queue list -A -a --json"] = renamed
	runner := &flakyRunner{data: data, allow: true}
	state := newCompatState(rig.NewProbe(runner, func(string, ...any) {}), &bytes.Buffer{})

	for i := range 3 {
		report := state.current()
		if !report.Blocked() {
			t.Fatalf("read %d opened the gate for a rig with a renamed field", i)
		}
		if !strings.Contains(report.Error, "pickup_state") {
			t.Fatalf("read %d error = %q, want the renamed field named", i, report.Error)
		}
		if err := act.NewGate(report).Check(); err == nil {
			t.Fatal("gate allowed the act loop for a rig with a renamed field")
		}
	}
}
