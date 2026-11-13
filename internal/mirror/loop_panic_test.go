package mirror

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/forge"
)

// TestLoopSurvivesAPanickingTick is epic #325 T2 (design D3): a panic anywhere
// in a tick used to end Mirror.Loop's goroutine — and with it the whole
// process, leaving the control plane dark until a restart. Here the source
// panics on the first call and answers normally on the second: the loop must
// run the second tick, leave one readable journal line naming the panic (with
// its stack), count the panicked tick as busy so the cadence resets to the
// base interval, and return only on context cancel. No audit row, no queue
// row, no decision, no metric — the journal is the surface (design D4).
func TestLoopSurvivesAPanickingTick(t *testing.T) {
	const base = time.Millisecond
	const max = 4 * time.Millisecond

	st := newStore(t)
	id := project(t, st)
	fake := forge.NewFake(forge.KindGitHub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var waits []time.Duration
	var reads int
	var logs []string

	m := &Mirror{
		Store: st,
		Logf: func(format string, args ...any) {
			mu.Lock()
			defer mu.Unlock()
			logs = append(logs, fmt.Sprintf(format, args...))
		},
		OnWait: func(wait time.Duration) {
			mu.Lock()
			waits = append(waits, wait)
			done := reads >= 4
			mu.Unlock()
			if done {
				cancel()
			}
		},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.Loop(ctx,
			func(context.Context) ([]Project, error) {
				mu.Lock()
				reads++
				n := reads
				mu.Unlock()
				// Two quiet ticks first: the wait doubles off the base, so the
				// panic's reset is a change the cadence actually logs. A panic
				// on the very first tick would leave the wait at base with no
				// line to assert — the loop still survives, but deliverable 4's
				// reset line needs the backoff to have happened.
				if n == 3 {
					panic("the forge exploded")
				}
				return []Project{{ID: id, Key: "app", Forge: fake}}, nil
			},
			Cadence{Base: base, Max: max}, 0, nil, nil)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("loop did not stop")
	}

	mu.Lock()
	gotWaits := append([]time.Duration(nil), waits...)
	gotLogs := append([]string(nil), logs...)
	mu.Unlock()

	// The journal as the operator would read it — the evidence the row asks
	// the PR body to carry.
	for _, line := range gotLogs {
		t.Logf("journal: %s", line)
	}

	if reads < 4 {
		t.Fatalf("source reads = %d, want the loop to run a tick after the panic", reads)
	}

	// The panicked tick counts as busy: the wait after it is the base interval,
	// never a doubling — a broken tick must be retried at the normal rate. The
	// sequence is quiet, quiet, panic, quiet: base, doubled, reset to base.
	want := []time.Duration{base, 2 * base, base, base}
	if len(gotWaits) < len(want) {
		t.Fatalf("waits = %v, want at least %v", gotWaits, want)
	}
	for i, w := range want {
		if gotWaits[i] != w {
			t.Fatalf("wait %d = %v, want %v (all waits: %v)", i+1, gotWaits[i], w, gotWaits)
		}
	}

	// One readable journal line per panic: the value, then the stack. The
	// cadence reset line names the panic too ("reset: tick: panicked: …"), so
	// the raw panic entry is told apart by its prefix.
	var panicLines []string
	for _, line := range gotLogs {
		if strings.HasPrefix(line, "tick: panicked:") {
			panicLines = append(panicLines, line)
		}
	}
	if len(panicLines) != 1 {
		t.Fatalf("panic journal lines = %d (%v), want exactly one", len(panicLines), gotLogs)
	}
	if !strings.Contains(panicLines[0], "the forge exploded") {
		t.Fatalf("panic line %q does not carry the panicked value", panicLines[0])
	}
	if !strings.Contains(panicLines[0], "goroutine") || !strings.Contains(panicLines[0], "loop_panic_test.go") {
		t.Fatalf("panic line %q does not carry the stack", panicLines[0])
	}

	// The cadence line names the reset and its reason, so the operator reads
	// why the poll went back to the base interval.
	reset := false
	for _, line := range gotLogs {
		if strings.Contains(line, "reset: tick: panicked:") {
			reset = true
		}
	}
	if !reset {
		t.Fatalf("cadence logs %v, want a reset line naming the panic", gotLogs)
	}
}
