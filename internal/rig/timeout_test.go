package rig

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// deadlineRunner records the deadline the adapter hands the runner, so a test
// sees the budget one verb actually gets instead of trusting a constant.
type deadlineRunner struct {
	mu          sync.Mutex
	hasDeadline bool
	deadline    time.Time
}

func (r *deadlineRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	deadline, ok := ctx.Deadline()
	r.deadline, r.hasDeadline = deadline, ok
	// The verdict of the call is irrelevant here; the recorded deadline is the
	// fact the test reads.
	return nil, errors.New("no answer")
}

// TestUpTimeout pins epic #320 D5: a cold launch runs with StartTimeout
// (5 minutes), not the read budget, because `rig up` boots a daemon, the
// kernel and every seat, and a 20 s timeout there is the worst outcome — the
// fleet comes up while the app records a failure.
func TestUpTimeout(t *testing.T) {
	runner := &deadlineRunner{}
	if _, err := NewAdapter(runner).Up(context.Background(), "/tmp/spec.yaml"); err == nil {
		t.Fatal("Up returned nil, want the fake runner's error")
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if !runner.hasDeadline {
		t.Fatal("Up handed the runner no deadline, want StartTimeout")
	}
	until := time.Until(runner.deadline)
	if until <= StartTimeout-time.Minute || until > StartTimeout {
		t.Fatalf("Up's deadline is %s from now, want within a minute of StartTimeout (%s)", until, StartTimeout)
	}
}

// TestReadTimeoutUnchanged pins the other half of D5: every verb but Up keeps
// the read budget. The adapter adds no deadline of its own to the read verbs,
// so their 20 s comes from ExecRunner's configured limit — and an explicit
// context deadline is the authority in both directions, which is what lets Up
// carry five minutes at all.
func TestReadTimeoutUnchanged(t *testing.T) {
	for _, verb := range []struct {
		name string
		run  func(a *Adapter) error
	}{
		{"queue", func(a *Adapter) error { _, err := a.QueueList(context.Background()); return err }},
		{"ps", func(a *Adapter) error { _, err := a.Seats(context.Background()); return err }},
		{"down", func(a *Adapter) error { _, err := a.Down(context.Background(), "r", false); return err }},
	} {
		t.Run(verb.name, func(t *testing.T) {
			runner := &deadlineRunner{}
			if err := verb.run(NewAdapter(runner)); err == nil {
				t.Fatalf("%s returned nil, want the fake runner's error", verb.name)
			}
			runner.mu.Lock()
			defer runner.mu.Unlock()
			if runner.hasDeadline {
				t.Fatalf("%s handed the runner a deadline; the adapter must add none, so ExecRunner's 20 s read budget applies", verb.name)
			}
		})
	}
	// The budget itself: no caller deadline means the configured (default 20 s)
	// limit; a shorter caller deadline wins; a longer one — Up's case — is
	// honoured rather than clamped back to the read budget.
	if got := (ExecRunner{}).resolveLimit(context.Background()); got != DefaultTimeout {
		t.Fatalf("resolveLimit with no deadline = %s, want the 20 s read budget", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if got := (ExecRunner{Timeout: DefaultTimeout}).resolveLimit(ctx); got > 10*time.Second {
		t.Fatalf("resolveLimit with a 10s parent = %s, want the parent's shorter deadline", got)
	}
	long, cancelLong := context.WithTimeout(context.Background(), StartTimeout)
	defer cancelLong()
	if got := (ExecRunner{}).resolveLimit(long); got <= StartTimeout-time.Minute {
		t.Fatalf("resolveLimit with a StartTimeout parent = %s, want the caller's explicit budget", got)
	}
}
