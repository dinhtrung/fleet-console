package mirror

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/forge"
)

// The loop's own sequence, recorded rather than slept: four quiet ticks double
// the wait and the fifth busy tick resets it. The recorder cancels the context on
// the wait that follows the last tick, so the test never waits a real interval.
func TestLoopCadenceSequence(t *testing.T) {
	// Millisecond scale: the same ratios as the spec's 1m/2m/4m, but the loop
	// really sleeps, so the test finishes in milliseconds instead of minutes.
	const base = time.Millisecond
	const max = 4 * time.Millisecond

	st := newStore(t)
	id := project(t, st)
	fake := forge.NewFake(forge.KindGitHub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var waits []time.Duration
	var ticks int

	// quiet for the first four ticks, then a finding on the fifth.
	verdicts := []bool{false, false, false, false, true}
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
			done := ticks >= len(verdicts)
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
				return []Project{{ID: id, Key: "app", Forge: fake}}, nil
			},
			Cadence{Base: base, Max: max}, 0,
			func(TickResult) bool {
				mu.Lock()
				defer mu.Unlock()
				ticks++
				if ticks <= len(verdicts) {
					return verdicts[ticks-1]
				}
				return true
			})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("loop did not stop")
	}

	mu.Lock()
	got := append([]time.Duration(nil), waits...)
	gotLogs := append([]string(nil), logs...)
	mu.Unlock()

	// The first wait follows tick 1 and is the base. Each later quiet tick
	// doubles, capped at max; the busy tick returns to base.
	want := []time.Duration{base, 2 * base, max, max, base}
	if len(got) < len(want) {
		t.Fatalf("waits = %v, want at least %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("wait %d = %v, want %v (all waits: %v)", i+1, got[i], w, got)
		}
	}

	// One line per change: the first wait equals the base so it logs nothing,
	// the two doublings log, the second wait at the ceiling repeats so it logs
	// nothing, and the reset logs. Four waits, three lines — the silence at the
	// ceiling is the behaviour the spec asks for.
	if len(gotLogs) != 3 {
		t.Fatalf("cadence log lines = %d (%v), want 3", len(gotLogs), gotLogs)
	}
	for i, line := range gotLogs[:2] {
		if !containsAll(line, "cadence", "quiet") {
			t.Fatalf("backoff line %d %q does not name the wait and the streak", i+1, line)
		}
	}
	// The streak names how many consecutive quiet ticks have happened by the
	// time the wait doubles: the tick that takes 1ms to 2ms is the second one.
	if !containsAll(gotLogs[0], "2ms", "quiet 2") {
		t.Fatalf("first backoff line %q does not name 2ms and quiet 2", gotLogs[0])
	}
	if !containsAll(gotLogs[1], "4ms", "quiet 3") {
		t.Fatalf("second backoff line %q does not name 4ms and quiet 3", gotLogs[1])
	}
	if last := gotLogs[2]; !containsAll(last, "cadence", "reset", "1ms") {
		t.Fatalf("reset line %q does not name the base wait and the reason", last)
	}
}

// A tick whose source could not be read counts as busy: a blind read must not
// earn a longer wait. The sequence here is error, error, quiet, so the first
// wait stays at base and only the third tick doubles.
func TestLoopCadenceTreatsAFailedReadAsBusy(t *testing.T) {
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

	m := &Mirror{
		Store: st,
		OnWait: func(wait time.Duration) {
			mu.Lock()
			waits = append(waits, wait)
			done := reads >= 3
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
				if n < 3 {
					return nil, errors.New("sources: boom")
				}
				return []Project{{ID: id, Key: "app", Forge: fake}}, nil
			},
			Cadence{Base: base, Max: max}, 0, nil)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("loop did not stop")
	}

	mu.Lock()
	got := append([]time.Duration(nil), waits...)
	mu.Unlock()

	want := []time.Duration{base, base, base}
	if len(got) < len(want) {
		t.Fatalf("waits = %v, want at least %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("wait %d = %v, want %v (all waits: %v)", i+1, got[i], w, got)
		}
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
