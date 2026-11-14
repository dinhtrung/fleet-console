package act

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/config"
	"github.com/dinhtrung/fleet-console/internal/preflight"
	"github.com/dinhtrung/fleet-console/internal/rig"
)

// unknownRosterRig is the entry an unread-roster tick builds: the rig read
// failed, this process's last read saw `last` running seats, and the project's
// forge read (which does not depend on the rig) carries the demand.
func unknownRosterRig(name string, last int, ready int) LifecycleRig {
	return LifecycleRig{
		Name:          name,
		SpecPath:      "/specs/" + name + "/rig.yaml",
		SeatsKnown:    false,
		RosterUnknown: true,
		LastRunning:   last,
		QuietStopped:  true,
		Projects:      []LifecycleProject{{Key: "app", Managed: true, Fresh: true, Ready: ready}},
	}
}

// TestDecideLifecycleUnreadRoster is change-set task 1.5: a failed rig read is
// not "decide nothing" for the start half (design D1). A rig this process last
// saw stopped and a fresh ready item plans one start whose reason names the
// unknown roster; a last-seen-live rig is never started on unknown data; no
// demand starts nothing; the stop half stays dark.
func TestDecideLifecycleUnreadRoster(t *testing.T) {
	now := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	input := func(entry LifecycleRig) LifecycleInput {
		return LifecycleInput{Enabled: true, IdleAfter: 10 * time.Minute, Now: now, Rigs: []LifecycleRig{entry}}
	}

	t.Run("last seen stopped and demand", func(t *testing.T) {
		actions := DecideLifecycle(input(unknownRosterRig("r", 0, 3)))
		wantActions(t, actions, LifecycleStart)
		if actions[0].Reason != "roster unknown; 3 ready item(s)" {
			t.Fatalf("reason = %q, want the unknown-roster wording", actions[0].Reason)
		}
	})

	t.Run("last seen live, never duplicated on unknown data", func(t *testing.T) {
		if actions := DecideLifecycle(input(unknownRosterRig("r", 2, 3))); len(actions) != 0 {
			t.Fatalf("actions = %+v, want none: a live fleet must not be duplicated", actions)
		}
	})

	t.Run("no demand", func(t *testing.T) {
		if actions := DecideLifecycle(input(unknownRosterRig("r", 0, 0))); len(actions) != 0 {
			t.Fatalf("actions = %+v, want none without demand", actions)
		}
	})

	t.Run("a claimable row is enough", func(t *testing.T) {
		entry := unknownRosterRig("r", 0, 0)
		entry.LiveRows = 1
		actions := DecideLifecycle(input(entry))
		wantActions(t, actions, LifecycleStart)
		if !strings.Contains(actions[0].Reason, "roster unknown") {
			t.Fatalf("reason = %q, want roster unknown", actions[0].Reason)
		}
	})

	t.Run("stop half stays dark", func(t *testing.T) {
		entry := unknownRosterRig("r", 0, 0)
		entry.QuietSince = now.Add(-20 * time.Minute)
		// Even with a quiet streak and zero ready items, an unread roster is
		// never stopped: a fleet the tick cannot see cannot be judged quiet.
		if actions := DecideLifecycle(input(entry)); len(actions) != 0 {
			t.Fatalf("actions = %+v, want no stop on an unread roster", actions)
		}
	})

	t.Run("a rig this process never read is left alone", func(t *testing.T) {
		entry := unknownRosterRig("r", 0, 3)
		entry.RosterUnknown = false
		if actions := DecideLifecycle(input(entry)); len(actions) != 0 {
			t.Fatalf("actions = %+v, want none: no last known state to decide from", actions)
		}
	})

	t.Run("an operator-stopped rig is still not started", func(t *testing.T) {
		entry := unknownRosterRig("r", 0, 3)
		entry.QuietStopped = false
		if actions := DecideLifecycle(input(entry)); len(actions) != 0 {
			t.Fatalf("actions = %+v, want none: the ledger does not name it ours", actions)
		}
	})
}

// TestLifecycleRemembersLastRunning is change-set tasks 1.1/1.2 through the
// coordinator: the count is remembered only on a tick whose read succeeded, and
// read only on a tick whose read failed. A process that never read the rig
// decides nothing rather than starting a fleet it cannot see (design D2).
func TestLifecycleRemembersLastRunning(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	st := newStore(t)
	home := t.TempDir()
	writeSpecAt(t, home, "r", preflight.SpecPath)
	rigs := &fakeLifecycleRig{}
	var lines []string
	lifecycle := &Lifecycle{
		Settings: config.LifecycleSettings{Enabled: true, IdleAfter: 10 * time.Minute},
		Executor: &LifecycleExecutor{Store: st, Rigs: rigs, Home: home, Now: func() time.Time { return now },
			Logf: func(format string, args ...any) { lines = append(lines, fmt.Sprint(fmt.Sprintf(format, args...))) }},
	}

	// Tick 1: a good read with one running seat. The count is remembered.
	live := []LifecycleProjectFacts{{
		Key: "app", Rigs: []string{"r"}, Managed: true, Fresh: true, Ready: 3,
		Seats: []rig.Seat{{Rig: "r", Session: "dev-build-1@r", Lifecycle: rig.LifecycleRunning}},
	}}
	if actions := lifecycle.Decide(live, true, false, nil, now); len(actions) != 0 {
		t.Fatalf("actions = %+v, want none while a seat runs", actions)
	}
	if got := lifecycle.lastRunning["r"]; got != 1 {
		t.Fatalf("lastRunning = %d, want 1 from the good read", got)
	}

	// Tick 2: the read fails. The last count was live, so nothing starts.
	stoppedSeats := []LifecycleProjectFacts{{
		Key: "app", Rigs: []string{"r"}, Managed: true, Fresh: true, Ready: 3,
		Seats: []rig.Seat{{Rig: "r", Session: "dev-build-1@r", Lifecycle: "exited"}},
	}}
	if actions := lifecycle.Decide(stoppedSeats, false, false, map[string]bool{"r": true}, now); len(actions) != 0 {
		t.Fatalf("actions = %+v, want none: the last read saw a live fleet", actions)
	}

	// Tick 3: a good read now shows the seats exited — our quiet stop, with
	// demand. One start, with the known-stopped wording.
	actions := lifecycle.Decide(stoppedSeats, true, false, map[string]bool{"r": true}, now)
	wantActions(t, actions, LifecycleStart)
	if !strings.HasPrefix(actions[0].Reason, "stopped_with_demand:") {
		t.Fatalf("reason = %q, want the known-stopped wording", actions[0].Reason)
	}
	if got := lifecycle.lastRunning["r"]; got != 0 {
		t.Fatalf("lastRunning = %d, want 0 after the exited read", got)
	}

	// Tick 4: the read fails again, and the last count is zero. The start half
	// still decides from demand, and names the unknown roster.
	actions = lifecycle.Decide(stoppedSeats, false, false, map[string]bool{"r": true}, now)
	wantActions(t, actions, LifecycleStart)
	if actions[0].Reason != "roster unknown; 3 ready item(s)" {
		t.Fatalf("reason = %q, want roster unknown", actions[0].Reason)
	}
	lifecycle.Start(ctx, actions)

	// Tick 5: a fresh process has no memory of the rig. It decides nothing —
	// a delay, never a start against a fleet it has not seen (design D2).
	fresh := &Lifecycle{
		Settings: lifecycle.Settings,
		Executor: &LifecycleExecutor{Store: st, Rigs: rigs, Home: home, Now: func() time.Time { return now },
			Logf: func(format string, args ...any) { lines = append(lines, fmt.Sprint(fmt.Sprintf(format, args...))) }},
	}
	if actions := fresh.Decide(stoppedSeats, false, false, map[string]bool{"r": true}, now); len(actions) != 0 {
		t.Fatalf("actions = %+v, want none: a restart forgets the count", actions)
	}
}

// TestLifecycleLine pins design D3's line for every state the rule can decide:
// a rig gets one line per tick, including `none`, and the line carries the
// numbers the decision used.
func TestLifecycleLine(t *testing.T) {
	quiet := managedRig("r") // one running seat, no demand
	cases := []struct {
		name   string
		entry  LifecycleRig
		action *LifecycleAction
		want   string
	}{
		{"running quiet", quiet, nil, "rig-lifecycle: r none — running=1 ready=0 seats_known=true"},
		{
			"stopped with demand", func() LifecycleRig {
				entry := managedRig("r")
				entry.Running = 0
				entry.QuietStopped = true
				entry.Projects[0].Ready = 3
				return entry
			}(),
			&LifecycleAction{Kind: LifecycleStart, Rig: "r", Reason: "stopped_with_demand: 3 ready item(s), no running seat"},
			"rig-lifecycle: r start — stopped_with_demand: 3 ready item(s), no running seat — running=0 ready=3 seats_known=true",
		},
		{
			"stopped with no demand", func() LifecycleRig {
				entry := managedRig("r")
				entry.Running = 0
				return entry
			}(),
			nil,
			"rig-lifecycle: r none — running=0 ready=0 seats_known=true",
		},
		{
			"unknown roster", unknownRosterRig("r", 0, 2), nil,
			"rig-lifecycle: r none — running=0 ready=2 seats_known=false roster=unknown",
		},
		{
			"stop", quiet,
			&LifecycleAction{Kind: LifecycleStop, Rig: "r", Quiet: 12*time.Minute + 3*time.Second},
			"rig-lifecycle: r stop — quiet 12m3s — running=1 ready=0 seats_known=true",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := LifecycleLine(tc.entry, tc.action); got != tc.want {
				t.Fatalf("line = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestLifecycleDecideLogsOneLinePerRig is task 2 end to end: every rig the rule
// considered appears in the journal each tick — once — and a rig it acted on is
// the executor's line, not a second decision line.
func TestLifecycleDecideLogsOneLinePerRig(t *testing.T) {
	now := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	st := newStore(t)
	var lines []string
	lifecycle := &Lifecycle{
		Settings: config.LifecycleSettings{Enabled: true, IdleAfter: 10 * time.Minute},
		Executor: &LifecycleExecutor{Store: st, Rigs: &fakeLifecycleRig{},
			Logf: func(format string, args ...any) { lines = append(lines, fmt.Sprint(fmt.Sprintf(format, args...))) }},
	}
	facts := []LifecycleProjectFacts{
		{Key: "warm", Rigs: []string{"warm"}, Managed: true, Fresh: true,
			Seats: []rig.Seat{{Rig: "warm", Session: "dev-build-1@warm", Lifecycle: rig.LifecycleRunning}}},
		{Key: "cold", Rigs: []string{"cold"}, Managed: true, Fresh: true, Ready: 2},
	}
	actions := lifecycle.Decide(facts, true, false, map[string]bool{"cold": true}, now)
	wantActions(t, actions, LifecycleStart)
	if len(lines) != 1 || !strings.Contains(lines[0], "rig-lifecycle: warm none") {
		t.Fatalf("lines = %v, want exactly one `warm none` line", lines)
	}
}

// writeSpecAt writes a launchable spec at the convention path so a start can
// find it.
func writeSpecAt(t *testing.T, home, rigName string, specFor func(home, rigName string) string) string {
	t.Helper()
	path := specFor(home, rigName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("spec dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("name: "+rigName+"\n"), 0o600); err != nil {
		t.Fatalf("spec: %v", err)
	}
	return path
}
