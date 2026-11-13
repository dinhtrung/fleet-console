package mirror

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// The daemon's own sentence, measured on the box (epic #320 §0).
const daemonDownSentence = "Error: Daemon not running.\n  This command needs a running daemon.\n" +
	"  Run 'rig up' (it auto-starts the daemon), or 'rig daemon start'.\n"

func downRunner() runnerFunc {
	return runnerFunc(func(_ context.Context, args ...string) ([]byte, error) {
		return nil, errors.New("rig " + strings.Join(args, " ") + ": " + daemonDownSentence)
	})
}

// TestRigDown: a rig read that failed because no daemon answered is carried as
// TickResult.RigDown beside RigStale, and rides the stored status row as
// rig_down so the board can show it. Every other rig failure — the timeout a
// busy box produces, a broken shape — stays stale-only: only a missing daemon
// counts as down.
func TestRigDown(t *testing.T) {
	ctx := context.Background()

	t.Run("the daemon's own sentence is down and stored", func(t *testing.T) {
		st := newStore(t)
		id := downProject(t, st)
		m := &Mirror{Store: st, Rig: rig.NewAdapter(downRunner())}
		read, err := m.Tick(ctx, []Project{{ID: id, Key: "app", Forge: forge.NewFake(forge.KindGitHub)}})
		if err != nil {
			t.Fatalf("tick: %v", err)
		}
		if !read.RigDown || !read.RigStale || read.RigError == "" {
			t.Fatalf("rig_down=%v rig_stale=%v error=%q, want down and stale", read.RigDown, read.RigStale, read.RigError)
		}
		status, err := st.MirrorStatus(ctx, id)
		if err != nil {
			t.Fatalf("mirror status: %v", err)
		}
		if !status.RigDown || !status.RigStale {
			t.Fatalf("stored status rig_down=%v rig_stale=%v, want both carried", status.RigDown, status.RigStale)
		}
	})

	t.Run("a refused local connection is down", func(t *testing.T) {
		st := newStore(t)
		id := downProject(t, st)
		runner := runnerFunc(func(_ context.Context, args ...string) ([]byte, error) {
			return nil, errors.New("rig " + strings.Join(args, " ") + ": dial unix /tmp/o.sock: connect: connection refused")
		})
		read, err := (&Mirror{Store: st, Rig: rig.NewAdapter(runner)}).Tick(ctx,
			[]Project{{ID: id, Key: "app", Forge: forge.NewFake(forge.KindGitHub)}})
		if err != nil {
			t.Fatalf("tick: %v", err)
		}
		if !read.RigDown {
			t.Fatalf("rig_down = false, want the refused connection read as down")
		}
	})

	t.Run("a timeout is stale but never down", func(t *testing.T) {
		st := newStore(t)
		id := downProject(t, st)
		runner := runnerFunc(func(_ context.Context, args ...string) ([]byte, error) {
			return nil, errors.New("rig " + strings.Join(args, " ") + ": timed out after 20s")
		})
		read, err := (&Mirror{Store: st, Rig: rig.NewAdapter(runner)}).Tick(ctx,
			[]Project{{ID: id, Key: "app", Forge: forge.NewFake(forge.KindGitHub)}})
		if err != nil {
			t.Fatalf("tick: %v", err)
		}
		if read.RigDown || !read.RigStale {
			t.Fatalf("rig_down=%v rig_stale=%v, want stale-only", read.RigDown, read.RigStale)
		}
		status, err := st.MirrorStatus(ctx, id)
		if err != nil {
			t.Fatalf("mirror status: %v", err)
		}
		if status.RigDown {
			t.Fatalf("stored rig_down = true, want a timeout to stay out of the column")
		}
	})

	t.Run("an answering read is neither stale nor down", func(t *testing.T) {
		st := newStore(t)
		id := downProject(t, st)
		runner := runnerFunc(func(_ context.Context, args ...string) ([]byte, error) {
			return []byte("[]"), nil
		})
		read, err := (&Mirror{Store: st, Rig: rig.NewAdapter(runner)}).Tick(ctx,
			[]Project{{ID: id, Key: "app", Forge: forge.NewFake(forge.KindGitHub)}})
		if err != nil {
			t.Fatalf("tick: %v", err)
		}
		if read.RigDown || read.RigStale {
			t.Fatalf("rig_down=%v rig_stale=%v, want a clean read to set neither", read.RigDown, read.RigStale)
		}
	})
}

// TestBusyWhenRigDown pins the cadence fact (epic #320 task 1.3): a down
// daemon already reads as busy — the poll stays at the base interval and the
// reset line names the rig read failure — so a later refactor cannot quietly
// slow the reaction to a started daemon down to idle_poll_max.
func TestBusyWhenRigDown(t *testing.T) {
	read := TickResult{
		Projects: map[string]ProjectResult{"app": {}},
		RigStale: true, RigDown: true,
		RigError: "rig queue list: " + daemonDownSentence,
	}
	busy, reason := Busy(read)
	if !busy {
		t.Fatalf("Busy = false, want a down daemon read as busy")
	}
	if !strings.Contains(reason, "rig read failed") || !strings.Contains(reason, "Daemon not running") {
		t.Fatalf("reason = %q, want it to name the rig read failure", reason)
	}
	// And the cadence holds: a busy tick resets the wait to the base, never
	// the idle ceiling.
	cadence := Cadence{Base: 15 * 1000, Max: 300 * 1000}
	_ = cadence.Next(true) // walk into the backoff
	_ = cadence.Next(true)
	if got := cadence.Next(false); got != cadence.Base {
		t.Fatalf("busy tick wait = %v, want the base interval %v", got, cadence.Base)
	}
}

// downProject creates one enabled project the tick can read.
func downProject(t *testing.T, st *store.Store) int64 {
	t.Helper()
	id, err := st.CreateProject(context.Background(), store.ProjectInput{
		Key: "app", Name: "app", ForgeKind: "github", ForgeProject: "o/app", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return id
}
