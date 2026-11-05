package mirror

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/forge"
)

// A wake ends the idle cadence. The loop starts quiet and doubles to idle_poll_max;
// a wake then runs one tick before the timer would have fired and the wait that
// follows it is tick_interval, not the ceiling the idle streak had reached.
//
// The proof is ordering plus arithmetic: the tick count rises while the loop is
// parked at a wait far longer than the test's patience, and the next recorded wait
// is base rather than max. A wake that only nudged the timer would show neither.
func TestLoopWakeRunsTickBeforeTimerAndResetsCadence(t *testing.T) {
	const base = 20 * time.Millisecond
	const max = 2 * time.Second // long enough that only a wake can end the wait

	st := newStore(t)
	id := project(t, st)
	fake := forge.NewFake(forge.KindGitHub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var waits []time.Duration
	var ticks int

	waker := NewWaker()

	// firstWait hands control back to the test after the first arm, so the test
	// knows the loop is running rather than guessing with a sleep.
	firstWait := make(chan time.Duration, 1)

	m := &Mirror{
		Store: st,
		OnWait: func(wait time.Duration) {
			mu.Lock()
			waits = append(waits, wait)
			n := len(waits)
			mu.Unlock()
			if n == 1 {
				firstWait <- wait
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
				ticks++
				mu.Unlock()
				return false // always quiet: the cadence doubles toward Max
			},
			waker.Channel())
	}()

	// Wait for the first wait to be armed. It is base, the pre-wake value.
	select {
	case w := <-firstWait:
		if w != base {
			t.Fatalf("first wait = %v, want %v", w, base)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("loop never armed its first wait")
	}

	// Let the loop run quiet ticks until it reaches the ceiling. Each quiet tick
	// doubles: base, 2*base, ... up to max. Wait for the recorded wait to hit max.
	deadline := time.After(3 * time.Second)
	for {
		mu.Lock()
		last := waits[len(waits)-1]
		mu.Unlock()
		if last == max {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("cadence never reached max; waits = %v", waits)
		default:
			time.Sleep(2 * time.Millisecond)
		}
	}

	mu.Lock()
	before := ticks
	armed := len(waits)
	mu.Unlock()

	// The loop is now parked at max (2s). A wake must run a tick long before that.
	if !waker.Wake() {
		t.Fatal("wake = false, want true: nothing was pending")
	}

	// Poll for the wait recorded after the wake-triggered tick. A stale earlier
	// wait would make this assertion pass for the wrong reason, so the count
	// snapshot is what makes the check meaningful.
	var afterWait time.Duration
	found := false
	limit := time.After(500 * time.Millisecond)
	for !found {
		mu.Lock()
		if len(waits) > armed {
			afterWait = waits[len(waits)-1]
			found = true
		}
		mu.Unlock()
		if found {
			break
		}
		select {
		case <-limit:
			mu.Lock()
			all := append([]time.Duration(nil), waits...)
			mu.Unlock()
			t.Fatalf("no tick ran within 500ms while parked at a 2s wait: the wake arm did not fire; waits=%v", all)
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if afterWait != base {
		t.Fatalf("wait after wake = %v, want %v (tick_interval): the wake must end the idle cadence", afterWait, base)
	}

	mu.Lock()
	after := ticks
	mu.Unlock()
	if after <= before {
		t.Fatalf("ticks did not increase on the wake: before=%d after=%d", before, after)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not stop")
	}
}

// A wake while the loop is already at base still runs the tick, and the following
// wait stays base. The arm must not be a no-op when there was no streak to clear.
func TestLoopWakeAtBaseStillTicks(t *testing.T) {
	const base = 20 * time.Millisecond
	const max = 2 * time.Second

	st := newStore(t)
	id := project(t, st)
	fake := forge.NewFake(forge.KindGitHub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var ticks int
	waker := NewWaker()
	armed := make(chan struct{}, 1)
	afterWake := make(chan time.Duration, 1)

	m := &Mirror{
		Store: st,
		OnWait: func(wait time.Duration) {
			mu.Lock()
			n := ticks
			mu.Unlock()
			if n == 1 {
				select {
				case armed <- struct{}{}:
				default:
				}
				return
			}
			select {
			case afterWake <- wait:
			default:
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
				ticks++
				mu.Unlock()
				return false
			},
			waker.Channel())
	}()

	select {
	case <-armed:
	case <-time.After(3 * time.Second):
		t.Fatal("loop never armed")
	}
	if !waker.Wake() {
		t.Fatal("wake = false, want true")
	}
	select {
	case w := <-afterWake:
		if w != base {
			t.Fatalf("wait after wake = %v, want %v", w, base)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("wake did not run a tick")
	}
	cancel()
	<-done
}
