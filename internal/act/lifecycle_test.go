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
	"github.com/dinhtrung/fleet-console/internal/store"
)

// managedRig is a rig every case starts from: one managed, fresh project with no
// demand, on a rig with one running seat. A case changes what it is about.
func managedRig(name string) LifecycleRig {
	return LifecycleRig{
		Name:       name,
		SpecPath:   "/specs/" + name + "/rig.yaml",
		SeatsKnown: true,
		Running:    1,
		Projects:   []LifecycleProject{{Key: "app", Managed: true, Fresh: true}},
	}
}

func wantActions(t *testing.T, actions []LifecycleAction, kinds ...LifecycleActionKind) {
	t.Helper()
	if len(actions) != len(kinds) {
		t.Fatalf("actions = %+v, want %v", actions, kinds)
	}
	for index, kind := range kinds {
		if actions[index].Kind != kind {
			t.Fatalf("actions[%d] = %q, want %q", index, actions[index].Kind, kind)
		}
	}
}

func lifecycleInput(rigs ...LifecycleRig) LifecycleInput {
	return LifecycleInput{
		Enabled:   true,
		IdleAfter: 10 * time.Minute,
		Now:       time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC),
		Rigs:      rigs,
	}
}

func TestDecideLifecycleCases(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	longQuiet := now.Add(-20 * time.Minute)
	justQuiet := now.Add(-9 * time.Minute)

	cases := []struct {
		name   string
		input  LifecycleInput
		expect []LifecycleActionKind
	}{
		{
			name: "start on Ready>0 and no running seat after our own quiet stop",
			input: func() LifecycleInput {
				entry := managedRig("r")
				entry.Running = 0
				entry.QuietStopped = true
				entry.Projects[0].Ready = 2
				return lifecycleInput(entry)
			}(),
			expect: []LifecycleActionKind{LifecycleStart},
		},
		{
			name: "no start for a rig the operator stopped by hand",
			input: func() LifecycleInput {
				entry := managedRig("r")
				entry.Running = 0
				entry.Projects[0].Ready = 3
				return lifecycleInput(entry)
			}(),
		},
		{
			name: "a down daemon starts on demand without our own stop record",
			input: func() LifecycleInput {
				entry := managedRig("r")
				entry.Running = 0
				entry.RigDown = true
				entry.Projects[0].Ready = 2
				return lifecycleInput(entry)
			}(),
			expect: []LifecycleActionKind{LifecycleStart},
		},
		{
			name: "no start when a seat is running",
			input: func() LifecycleInput {
				entry := managedRig("r")
				entry.Projects[0].Ready = 2
				return lifecycleInput(entry)
			}(),
		},
		{
			name: "no start when the project opted out",
			input: func() LifecycleInput {
				entry := managedRig("r")
				entry.Running = 0
				entry.Projects[0].Managed = false
				entry.Projects[0].Ready = 3
				return lifecycleInput(entry)
			}(),
		},
		{
			name: "no start on a stale read",
			input: func() LifecycleInput {
				entry := managedRig("r")
				entry.Running = 0
				entry.Projects[0].Fresh = false
				entry.Projects[0].Ready = 4
				return lifecycleInput(entry)
			}(),
		},
		{
			name: "start on a claimable row with no ready item",
			input: func() LifecycleInput {
				entry := managedRig("r")
				entry.Running = 0
				entry.QuietStopped = true
				entry.LiveRows = 1
				return lifecycleInput(entry)
			}(),
			expect: []LifecycleActionKind{LifecycleStart},
		},
		{
			name: "start carries the spec path and the reason",
			input: func() LifecycleInput {
				entry := managedRig("fleet")
				entry.Running = 0
				entry.QuietStopped = true
				entry.Projects[0].Ready = 1
				return lifecycleInput(entry)
			}(),
			expect: []LifecycleActionKind{LifecycleStart},
		},
		{
			name: "stop after the window",
			input: func() LifecycleInput {
				entry := managedRig("r")
				entry.QuietSince = longQuiet
				return lifecycleInput(entry)
			}(),
			expect: []LifecycleActionKind{LifecycleStop},
		},
		{
			name: "no stop one tick before the window",
			input: func() LifecycleInput {
				entry := managedRig("r")
				entry.QuietSince = justQuiet
				return lifecycleInput(entry)
			}(),
		},
		{
			name: "no stop when the window has not opened",
			input: func() LifecycleInput {
				return lifecycleInput(managedRig("r"))
			}(),
		},
		{
			name: "no stop when a live row is addressed to a seat on the rig",
			input: func() LifecycleInput {
				entry := managedRig("r")
				entry.QuietSince = longQuiet
				entry.LiveRows = 1
				return lifecycleInput(entry)
			}(),
		},
		{
			name: "no stop with a stale read",
			input: func() LifecycleInput {
				entry := managedRig("r")
				entry.QuietSince = longQuiet
				entry.Projects[0].Fresh = false
				return lifecycleInput(entry)
			}(),
		},
		{
			name: "no stop with a ready item",
			input: func() LifecycleInput {
				entry := managedRig("r")
				entry.QuietSince = longQuiet
				entry.Projects[0].Ready = 1
				return lifecycleInput(entry)
			}(),
		},
		{
			name: "no stop when the only bound project opted out",
			input: func() LifecycleInput {
				entry := managedRig("r")
				entry.QuietSince = longQuiet
				entry.Projects[0].Managed = false
				return lifecycleInput(entry)
			}(),
		},
		{
			name: "no stop on an unread roster",
			input: func() LifecycleInput {
				entry := managedRig("r")
				entry.QuietSince = longQuiet
				entry.SeatsKnown = false
				return lifecycleInput(entry)
			}(),
		},
		{
			name: "enabled=false decides nothing",
			input: func() LifecycleInput {
				entry := managedRig("r")
				entry.QuietSince = longQuiet
				input := lifecycleInput(entry)
				input.Enabled = false
				return input
			}(),
		},
		{
			name: "two projects on one rig, one quiet and one busy, produces no stop",
			input: func() LifecycleInput {
				entry := managedRig("r")
				entry.QuietSince = longQuiet
				entry.Projects = append(entry.Projects, LifecycleProject{Key: "other", Managed: true, Fresh: true, Ready: 1})
				return lifecycleInput(entry)
			}(),
		},
		{
			name: "two projects on one rig, both quiet, stops once",
			input: func() LifecycleInput {
				entry := managedRig("r")
				entry.QuietSince = longQuiet
				entry.Projects = append(entry.Projects, LifecycleProject{Key: "other", Managed: true, Fresh: true})
				return lifecycleInput(entry)
			}(),
			expect: []LifecycleActionKind{LifecycleStop},
		},
		{
			name: "one rig stopped and busy while a second rig is quiet",
			input: func() LifecycleInput {
				stopped := managedRig("cold")
				stopped.Running = 0
				stopped.QuietStopped = true
				stopped.Projects[0].Ready = 1
				quiet := managedRig("warm")
				quiet.QuietSince = longQuiet
				return lifecycleInput(stopped, quiet)
			}(),
			expect: []LifecycleActionKind{LifecycleStart, LifecycleStop},
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			actions := DecideLifecycle(test.input)
			wantActions(t, actions, test.expect...)
			for _, action := range actions {
				if action.Kind == LifecycleStart {
					if action.Spec == "" {
						t.Fatalf("start action has no spec path: %+v", action)
					}
					if action.Reason == "" {
						t.Fatalf("start action has no reason: %+v", action)
					}
				}
				if action.Kind == LifecycleStop && action.Quiet < 20*time.Minute {
					t.Fatalf("stop action quiet = %s, want the elapsed window: %+v", action.Quiet, action)
				}
			}
		})
	}
}

// TestBuildLifecycleRigsCountsSharedRigOnce pins the grouping: two projects on
// one rig share one seat count and one row count, so a doubled fleet cannot look
// like a live one and a shared row cannot fool the claimable arm.
func TestBuildLifecycleRigsCountsSharedRigOnce(t *testing.T) {
	seats := []rig.Seat{{Rig: "shared", Session: "dev-build-1@shared", Lifecycle: "running"}}
	rows := []rig.QueueRow{{ID: "q1", Destination: "dev-build-1@shared", State: "pending"}}
	projects := []LifecycleProjectFacts{
		{Key: "a", Rigs: []string{"shared"}, Managed: true, Fresh: true, Seats: seats, Rows: rows},
		{Key: "b", Rigs: []string{"shared"}, Managed: true, Fresh: true, Seats: seats, Rows: rows},
	}

	rigs := BuildLifecycleRigs(projects, nil, true, nil)

	if len(rigs) != 1 {
		t.Fatalf("rigs = %+v, want one shared entry", rigs)
	}
	if rigs[0].Running != 1 {
		t.Fatalf("running = %d, want 1 (seats counted once)", rigs[0].Running)
	}
	if rigs[0].LiveRows != 1 {
		t.Fatalf("live rows = %d, want 1 (rows counted once)", rigs[0].LiveRows)
	}
	if len(rigs[0].Projects) != 2 {
		t.Fatalf("projects = %+v, want both bound projects", rigs[0].Projects)
	}
}

// TestBuildLifecycleRigsCountsOnlyRunningSeats is #403: a stopped rig keeps its
// node rows and those rows report another lifecycle, so only a running seat is
// a live fleet. Counting rows made a stopped rig read as up, the start arm
// never fired on demand, and the tick produced neither a start nor a stop.
func TestBuildLifecycleRigsCountsOnlyRunningSeats(t *testing.T) {
	for _, tc := range []struct {
		name     string
		lifecyle []string
		running  int
	}{
		{"running seat", []string{"running"}, 1},
		{"exited seat", []string{"exited"}, 0},
		{"recoverable seat", []string{"recoverable"}, 0},
		{"attention_required seat", []string{"attention_required"}, 0},
		{"empty lifecycle", []string{""}, 0},
		{"mixed roster", []string{"exited", "running", "recoverable"}, 1},
		{"all exited", []string{"exited", "exited"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seats := make([]rig.Seat, 0, len(tc.lifecyle))
			for i, lifecycle := range tc.lifecyle {
				seats = append(seats, rig.Seat{
					Rig: "r", Session: fmt.Sprintf("dev-build-%d@r", i+1), Lifecycle: lifecycle,
				})
			}
			projects := []LifecycleProjectFacts{
				{Key: "app", Rigs: []string{"r"}, Managed: true, Fresh: true, Seats: seats},
			}

			rigs := BuildLifecycleRigs(projects, nil, true, nil)

			if len(rigs) != 1 {
				t.Fatalf("rigs = %+v, want one entry", rigs)
			}
			if rigs[0].Running != tc.running {
				t.Fatalf("running = %d, want %d for %v", rigs[0].Running, tc.running, tc.lifecyle)
			}
		})
	}
}

// TestStoppedRigStartsOnDemand is #403 end to end through the builder and the
// rule: a rig whose seats have all exited and whose project has ready work gets
// one start action naming the demand, and the same rig with no work gets none.
// The once-only half is StartBackoff, pinned by its own test.
func TestStoppedRigStartsOnDemand(t *testing.T) {
	now := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	exited := []rig.Seat{
		{Rig: "r", Session: "dev-build-1@r", Lifecycle: "exited"},
		{Rig: "r", Session: "dev-review@r", Lifecycle: "recoverable"},
	}

	withDemand := BuildLifecycleRigs([]LifecycleProjectFacts{
		{Key: "app", Rigs: []string{"r"}, Managed: true, Fresh: true, Ready: 3, Seats: exited},
	}, nil, true, map[string]bool{"r": true})
	actions := DecideLifecycle(LifecycleInput{
		Enabled: true, IdleAfter: 10 * time.Minute, Now: now, Rigs: withDemand,
	})
	if len(actions) != 1 || actions[0].Kind != LifecycleStart {
		t.Fatalf("actions = %+v, want one start for a stopped rig with demand", actions)
	}
	if !strings.HasPrefix(actions[0].Reason, "stopped_with_demand:") {
		t.Fatalf("reason = %q, want the stopped-with-demand wording", actions[0].Reason)
	}

	noDemand := BuildLifecycleRigs([]LifecycleProjectFacts{
		{Key: "app", Rigs: []string{"r"}, Managed: true, Fresh: true, Seats: exited},
	}, nil, true, map[string]bool{"r": true})
	if actions := DecideLifecycle(LifecycleInput{
		Enabled: true, IdleAfter: 10 * time.Minute, Now: now, Rigs: noDemand,
	}); len(actions) != 0 {
		t.Fatalf("actions = %+v, want a stopped idle rig left alone", actions)
	}

	// A stale read is not demand, even with every seat exited.
	stale := BuildLifecycleRigs([]LifecycleProjectFacts{
		{Key: "app", Rigs: []string{"r"}, Managed: true, Fresh: false, Ready: 3, Seats: exited},
	}, nil, true, map[string]bool{"r": true})
	if actions := DecideLifecycle(LifecycleInput{
		Enabled: true, IdleAfter: 10 * time.Minute, Now: now, Rigs: stale,
	}); len(actions) != 0 {
		t.Fatalf("actions = %+v, want no start on a stale forge read", actions)
	}
}

// TestLiveRowExcludesTerminalAndHumanRows pins the one vocabulary the rule
// shares with the rest of the app: a human-blocked row is not work, a terminal
// row is not work, and a peer-blocked row is.
func TestLiveRowExcludesTerminalAndHumanRows(t *testing.T) {
	cases := []struct {
		name string
		row  rig.QueueRow
		live bool
	}{
		{"pending to a seat", rig.QueueRow{State: "pending", Destination: "dev-build-1@r"}, true},
		{"terminal", rig.QueueRow{State: "pending", PickupState: "terminal", Destination: "dev-build-1@r"}, false},
		{"done", rig.QueueRow{State: "done", Destination: "dev-build-1@r"}, false},
		{"blocked on a person", rig.QueueRow{State: "blocked", BlockedOn: "human@kernel", Destination: "dev-build-1@r"}, false},
		{"addressed to a person", rig.QueueRow{State: "pending", Destination: "human@kernel"}, false},
		{"blocked on another row", rig.QueueRow{State: "blocked", BlockedOn: "qitem-2", Destination: "dev-build-1@r"}, true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := LiveRow(test.row); got != test.live {
				t.Fatalf("LiveRow(%+v) = %v, want %v", test.row, got, test.live)
			}
		})
	}
}

// TestNextQuietSinceOpensAndClosesTheWindow pins the memory rule: the window
// opens on the first quiet tick, survives a continuing streak, and closes the
// moment a busy tick arrives.
func TestNextQuietSinceOpensAndClosesTheWindow(t *testing.T) {
	t0 := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Minute)

	if got := NextQuietSince(time.Time{}, t0, true); !got.Equal(t0) {
		t.Fatalf("first quiet tick = %s, want %s", got, t0)
	}
	if got := NextQuietSince(t0, t1, true); !got.Equal(t0) {
		t.Fatalf("continuing streak = %s, want the opening stamp %s", got, t0)
	}
	if got := NextQuietSince(t0, t1, false); !got.IsZero() {
		t.Fatalf("busy tick = %s, want the zero time", got)
	}
}

// TestLifecycleDecideAdvancesTheWindowOnce is the coordinator's integration
// point: two ticks a minute apart do not stop the rig, the window opens on the
// first, and the second is one tick into the streak.
func TestLifecycleDecideAdvancesTheWindowOnce(t *testing.T) {
	settings := config.LifecycleSettings{Enabled: true, IdleAfter: 10 * time.Minute}
	lifecycle := &Lifecycle{Settings: settings, Executor: &LifecycleExecutor{}}
	t0 := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	facts := []LifecycleProjectFacts{{
		Key: "app", Rigs: []string{"r"}, Managed: true, Fresh: true,
		Seats: []rig.Seat{{Rig: "r", Session: "dev-build-1@r", Lifecycle: "running"}},
	}}

	if actions := lifecycle.Decide(facts, true, false, nil, t0); len(actions) != 0 {
		t.Fatalf("first quiet tick actions = %+v, want none", actions)
	}
	if got := lifecycle.quiet["r"]; !got.Equal(t0) {
		t.Fatalf("window = %s, want it opened at %s", got, t0)
	}
	if actions := lifecycle.Decide(facts, true, false, nil, t0.Add(time.Minute)); len(actions) != 0 {
		t.Fatalf("second quiet tick actions = %+v, want none yet", actions)
	}
	if got := lifecycle.quiet["r"]; !got.Equal(t0) {
		t.Fatalf("window moved to %s, want it held at %s", got, t0)
	}
	if actions := lifecycle.Decide(facts, true, false, nil, t0.Add(11*time.Minute)); len(actions) != 1 || actions[0].Kind != LifecycleStop {
		t.Fatalf("actions after the window = %+v, want one stop", actions)
	}
}

// TestLifecycleDisabledDecidesNothing: with the block absent the box behaves
// exactly as today.
func TestLifecycleDisabledDecidesNothing(t *testing.T) {
	lifecycle := &Lifecycle{Settings: config.LifecycleSettings{}, Executor: &LifecycleExecutor{}}
	facts := []LifecycleProjectFacts{{Key: "app", Rigs: []string{"r"}, Managed: true, Fresh: true, Ready: 5}}
	if actions := lifecycle.Decide(facts, true, false, nil, time.Now()); actions != nil {
		t.Fatalf("actions = %+v, want none while disabled", actions)
	}
}

// TestDecideLifecycleDaemonDown pins the start-only relaxation (epic #320, D2)
// at the rule level: a rig the tick marks RigDown is decided like a stopped rig
// with a known roster — started on demand, never stopped. The four cases are
// the spec's own sentences.
func TestDecideLifecycleDaemonDown(t *testing.T) {
	downRig := func(ready int, fresh bool) LifecycleRig {
		entry := managedRig("r")
		entry.Running = 0
		entry.RigDown = true
		entry.Projects[0].Ready = ready
		entry.Projects[0].Fresh = fresh
		return entry
	}

	t.Run("down with demand starts once", func(t *testing.T) {
		actions := DecideLifecycle(lifecycleInput(downRig(3, true)))
		wantActions(t, actions, LifecycleStart)
		if actions[0].Reason != "daemon down; 3 ready item(s)" {
			t.Fatalf("reason = %q, want the daemon-down wording", actions[0].Reason)
		}
	})
	t.Run("down with no demand starts nothing", func(t *testing.T) {
		actions := DecideLifecycle(lifecycleInput(downRig(0, true)))
		wantActions(t, actions)
	})
	t.Run("down on a stale read starts nothing", func(t *testing.T) {
		actions := DecideLifecycle(lifecycleInput(downRig(3, false)))
		wantActions(t, actions)
	})
	t.Run("down and quiet plans no stop", func(t *testing.T) {
		entry := downRig(0, true)
		entry.QuietSince = lifecycleInput(entry).Now.Add(-20 * time.Minute)
		actions := DecideLifecycle(lifecycleInput(entry))
		wantActions(t, actions)
	})
}

// TestBuildLifecycleRigsRigDown pins the builder's daemon-down shape (D2): the
// fact turns into SeatsKnown=true, Running untouched at zero, RigDown=true —
// and the seats the read could not take are not invented.
func TestBuildLifecycleRigsRigDown(t *testing.T) {
	facts := []LifecycleProjectFacts{{Key: "app", Rigs: []string{"r"}, Managed: true, Fresh: true, Ready: 3}}
	rigs := buildLifecycleRigs(facts, nil, false, true, nil)
	if len(rigs) != 1 {
		t.Fatalf("rigs = %d, want 1", len(rigs))
	}
	entry := rigs[0]
	if !entry.SeatsKnown || !entry.RigDown || entry.Running != 0 {
		t.Fatalf("entry = SeatsKnown %v Running %d RigDown %v, want known/0/true",
			entry.SeatsKnown, entry.Running, entry.RigDown)
	}
}

// TestStartReasonDaemonDown pins the two reason strings side by side: the same
// demand reads as `daemon down; N ready item(s)` when the rig is down and
// `stopped_with_demand: ...` when the rig is merely stopped (tasks §2.2).
func TestStartReasonDaemonDown(t *testing.T) {
	up := managedRig("r")
	up.Running = 0
	up.Projects[0].Ready = 2
	down := up
	down.RigDown = true

	reason, ok := startReason(up)
	if !ok || reason != "stopped_with_demand: 2 ready item(s), no running seat" {
		t.Fatalf("up reason = (%q, %v), want the stopped-with-demand wording", reason, ok)
	}
	reason, ok = startReason(down)
	if !ok || reason != "daemon down; 2 ready item(s)" {
		t.Fatalf("down reason = (%q, %v), want the daemon-down wording", reason, ok)
	}
}

// TestStartBackoff pins one attempt per rig per window (epic #320, D4): three
// ticks inside StartBackoff reach Up once and explain the wait in one
// note-deduplicated line; a fourth after the window reaches Up again. The
// deferral is not a failure — the dispatch step's failed map stays empty.
func TestStartBackoff(t *testing.T) {
	st := newStore(t)
	var lines []string
	rigs := &fakeLifecycleRig{upErr: errString("boom")}
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	executor := &LifecycleExecutor{Store: st, Rigs: rigs, Now: func() time.Time { return now },
		Logf: func(format string, args ...any) {
			lines = append(lines, fmt.Sprint(fmt.Sprintf(format, args...)))
		}}
	spec := writeSpec(t)
	action := LifecycleAction{Kind: LifecycleStart, Rig: "r", Spec: spec, Reason: "demand"}

	// Three ticks inside the window: one attempt, two deferrals with one
	// deduplicated line between them.
	if failed := executor.Start(context.Background(), []LifecycleAction{action}); failed == nil {
		t.Fatal("the first attempt did not report its failure")
	}
	for i := 0; i < 2; i++ {
		if failed := executor.Start(context.Background(), []LifecycleAction{action}); failed != nil {
			t.Fatalf("tick %d reported a deferral as a failure: %v", i+2, failed)
		}
	}
	if rigs.upCalls != 1 {
		t.Fatalf("up calls = %d, want 1 inside the window", rigs.upCalls)
	}
	deferred := 0
	for _, line := range lines {
		if strings.Contains(line, "up deferred — last attempt") {
			deferred++
		}
	}
	if deferred != 1 {
		t.Fatalf("deferred lines = %d (%v), want one deduplicated line", deferred, lines)
	}
	// After the window the rig is tried again — the failing start, that is.
	now = now.Add(StartBackoff + time.Minute)
	if failed := executor.Start(context.Background(), []LifecycleAction{action}); failed == nil {
		t.Fatal("the post-window attempt did not report its failure")
	}
	if rigs.upCalls != 2 {
		t.Fatalf("up calls = %d, want 2 after the window", rigs.upCalls)
	}
}

// TestStartBackoffSuccessRecordsTheAttempt pins tasks §2.3's second sentence: a
// successful start records its attempt time like a failure does, so a rig that
// just came up is not launched again within the window.
func TestStartBackoffSuccessRecordsTheAttempt(t *testing.T) {
	st := newStore(t)
	var lines []string
	rigs := &fakeLifecycleRig{}
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	executor := &LifecycleExecutor{Store: st, Rigs: rigs, Now: func() time.Time { return now },
		Logf: func(format string, args ...any) {
			lines = append(lines, fmt.Sprint(fmt.Sprintf(format, args...)))
		}}
	spec := writeSpec(t)
	action := LifecycleAction{Kind: LifecycleStart, Rig: "r", Spec: spec, Reason: "demand"}

	executor.Start(context.Background(), []LifecycleAction{action})
	if rigs.upCalls != 1 {
		t.Fatalf("up calls = %d, want 1", rigs.upCalls)
	}
	// A second start inside the window is deferred even though the first
	// succeeded: a just-started rig must not be launched again.
	if failed := executor.Start(context.Background(), []LifecycleAction{action}); failed != nil {
		t.Fatalf("the deferred start was reported as a failure: %v", failed)
	}
	if rigs.upCalls != 1 {
		t.Fatalf("up calls = %d, want 1 (the success is inside the window too)", rigs.upCalls)
	}
}

// TestRigLifecycleExecutorLogsOncePerChange pins the dedupe promise over a
// permanently broken `rig up`: the failure writes one line, and the ticks the
// backoff then defers write one line, not one per tick (epic #320, D4). The
// old D8 pin — "the next tick retries" — is the behaviour D4 deliberately
// replaced: inside StartBackoff there is no second Up call at all.
func TestRigLifecycleExecutorLogsOncePerChange(t *testing.T) {
	st := newStore(t)
	var lines []string
	rigs := &fakeLifecycleRig{upErr: errString("boom")}
	executor := &LifecycleExecutor{Store: st, Rigs: rigs, Logf: func(format string, args ...any) {
		lines = append(lines, fmt.Sprint(fmt.Sprintf(format, args...)))
	}}
	spec := writeSpec(t)
	action := LifecycleAction{Kind: LifecycleStart, Rig: "r", Spec: spec, Reason: "demand"}

	if failed := executor.Start(context.Background(), []LifecycleAction{action}); failed == nil {
		t.Fatal("a failed Up was reported as a success")
	}
	// Inside the backoff window the attempt is deferred, not retried: the
	// deferral is not a failure, so the dispatch step does not skip the rig.
	if failed := executor.Start(context.Background(), []LifecycleAction{action}); failed != nil {
		t.Fatalf("a deferred start was reported as a failure: %v", failed)
	}
	if rigs.upCalls != 1 {
		t.Fatalf("up calls = %d, want 1 (the backoff holds inside the window)", rigs.upCalls)
	}
	if len(lines) != 2 {
		t.Fatalf("log lines = %v, want the failure line and one deferred line", lines)
	}
	if !strings.Contains(lines[1], "up deferred — last attempt") {
		t.Fatalf("deferred line = %q, want the deferral wording", lines[1])
	}
}

// TestRigLifecycleExecutorAuditsSuccess pins the audit kinds and the log line.
func TestRigLifecycleExecutorAuditsSuccess(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	var lines []string
	executor := &LifecycleExecutor{Store: st, Rigs: &fakeLifecycleRig{}, Logf: func(format string, args ...any) {
		lines = append(lines, format)
	}}

	executor.Start(ctx, []LifecycleAction{{Kind: LifecycleStart, Rig: "r", Spec: writeSpec(t), Reason: "2 ready item(s)"}})
	if len(lines) != 1 || !strings.Contains(lines[0], "rig-lifecycle: %s started") {
		t.Fatalf("lines = %v, want one start line", lines)
	}
	events, err := st.RecentEvents(ctx, 10)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != "rig.started" {
		t.Fatalf("events = %+v, want one rig.started", events)
	}

	executor.Stop(ctx, []LifecycleAction{{Kind: LifecycleStop, Rig: "r", Quiet: 11 * time.Minute}})
	events, err = st.RecentEvents(ctx, 10)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(events) != 2 || events[0].Kind != "rig.stopped" {
		t.Fatalf("events = %+v, want rig.stopped newest", events)
	}
	if !strings.HasPrefix(lines[len(lines)-1], "rig-lifecycle: %s stopped — quiet %s") {
		t.Fatalf("stop line = %q, want the stopped-with-quiet wording", lines[len(lines)-1])
	}
}

// TestRigLifecycleNeverDeletes pins D4: the idle stop is `rig down`, never
// `rig down --delete`.
func TestRigLifecycleNeverDeletes(t *testing.T) {
	rigs := &fakeLifecycleRig{}
	executor := &LifecycleExecutor{Store: newStore(t), Rigs: rigs}
	executor.Stop(context.Background(), []LifecycleAction{{Kind: LifecycleStop, Rig: "r", Quiet: time.Minute}})
	if len(rigs.downNames) != 1 || rigs.downNames[0] != "r" {
		t.Fatalf("down names = %v, want the rig", rigs.downNames)
	}
	if rigs.downDelete {
		t.Fatal("stop asked for --delete")
	}
}

// TestRigLifecycleMissingSpecNamesThePath: a missing spec is a start failure
// that names the path it looked for, never an invented one.
// TestRigLifecycleMissingSpecNamesThePath: a missing spec is exported on demand
// now (#431); when that cannot resolve it, the failure still names the path and
// logs one line. The export is made to fail so this pins the unresolvable case.
func TestRigLifecycleMissingSpecNamesThePath(t *testing.T) {
	var lines []string
	executor := &LifecycleExecutor{
		Store: newStore(t), Rigs: &fakeLifecycleRig{exportErr: errString("export unavailable")}, Home: t.TempDir(),
		Logf: func(format string, args ...any) { lines = append(lines, format) },
	}
	err := executor.start(context.Background(), LifecycleAction{Kind: LifecycleStart, Rig: "ghost"})
	if err == nil {
		t.Fatal("a missing spec was not a failure")
	}
	if !strings.Contains(err.Error(), "rig.yaml") {
		t.Fatalf("error = %q, want the spec path named", err)
	}
	if len(lines) != 1 {
		t.Fatalf("lines = %v, want the one failure line", lines)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func writeSpec(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rig.yaml")
	if err := os.WriteFile(path, []byte("name: r\n"), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	return path
}

type fakeLifecycleRig struct {
	upErr       error
	upCalls     int
	exportCalls int
	exportErr   error
	downNames   []string
	downDelete  bool
}

func (f *fakeLifecycleRig) Up(context.Context, string) (*rig.RunReport, error) {
	f.upCalls++
	if f.upErr != nil {
		return nil, f.upErr
	}
	return &rig.RunReport{Status: "ok"}, nil
}

func (f *fakeLifecycleRig) Export(_ context.Context, _, specPath string) error {
	f.exportCalls++
	if f.exportErr != nil {
		return f.exportErr
	}
	if err := os.MkdirAll(filepath.Dir(specPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(specPath, []byte("name: r\n"), 0o600)
}

func (f *fakeLifecycleRig) Down(_ context.Context, name string, deleteRig bool) (*rig.DownReport, error) {
	f.downNames = append(f.downNames, name)
	f.downDelete = deleteRig
	return &rig.DownReport{RigName: name}, nil
}

// TestStartExportsAMissingSpec (#431): a rig created from a bundle has a row and
// no spec file. The start exports it on demand, once, and then launches it — a
// missing file is resolvable, not a verdict.
func TestStartExportsAMissingSpec(t *testing.T) {
	st := newStore(t)
	rigs := &fakeLifecycleRig{}
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	executor := &LifecycleExecutor{Store: st, Rigs: rigs, Now: func() time.Time { return now }}
	spec := filepath.Join(t.TempDir(), "specs", "r", "rig.yaml")

	action := LifecycleAction{Kind: LifecycleStart, Rig: "r", Spec: spec, Reason: "demand"}
	if failed := executor.Start(context.Background(), []LifecycleAction{action}); failed != nil {
		t.Fatalf("start failed: %v", failed)
	}
	if rigs.exportCalls != 1 || rigs.upCalls != 1 {
		t.Fatalf("export calls = %d up calls = %d, want one each", rigs.exportCalls, rigs.upCalls)
	}
	if _, err := os.Stat(spec); err != nil {
		t.Fatalf("exported spec missing: %v", err)
	}
}

// TestStartNamesTheMissingSpecWhenExportFails (#431): when the export cannot
// help, the failure names the rig and the path, and Up is never attempted.
func TestStartNamesTheMissingSpecWhenExportFails(t *testing.T) {
	st := newStore(t)
	rigs := &fakeLifecycleRig{exportErr: errString("daemon unreachable")}
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	executor := &LifecycleExecutor{Store: st, Rigs: rigs, Now: func() time.Time { return now }}
	spec := filepath.Join(t.TempDir(), "specs", "r", "rig.yaml")

	failed := executor.Start(context.Background(), []LifecycleAction{
		{Kind: LifecycleStart, Rig: "r", Spec: spec, Reason: "demand"},
	})
	if failed["r"] == "" || !strings.Contains(failed["r"], spec) || !strings.Contains(failed["r"], "export failed") {
		t.Fatalf("failure = %q, want the path and the export", failed["r"])
	}
	if rigs.upCalls != 0 {
		t.Fatalf("up calls = %d, want none without a spec", rigs.upCalls)
	}
}

// TestBuildLifecycleRigsCarriesQuietStops pins the provenance the start rule
// reads (#403): the builder copies the ledger's answer onto the rig entry, and a
// rig the ledger does not name stays false — "somebody else stopped it".
func TestBuildLifecycleRigsCarriesQuietStops(t *testing.T) {
	facts := []LifecycleProjectFacts{
		{Key: "app", Rigs: []string{"ours", "theirs"}, Managed: true, Fresh: true},
	}
	rigs := BuildLifecycleRigs(facts, nil, true, map[string]bool{"ours": true})
	if len(rigs) != 2 {
		t.Fatalf("rigs = %+v, want both", rigs)
	}
	byName := make(map[string]LifecycleRig, len(rigs))
	for _, entry := range rigs {
		byName[entry.Name] = entry
	}
	if !byName["ours"].QuietStopped {
		t.Fatalf("ours = %+v, want QuietStopped from the ledger", byName["ours"])
	}
	if byName["theirs"].QuietStopped {
		t.Fatalf("theirs = %+v, want false: the ledger never stopped it", byName["theirs"])
	}
}

// TestStoppedRigStartNeedsOurOwnStopRecord is #403's third acceptance sentence,
// end to end through the real ledger: `rig down` and `rig seat stop` use the
// same verb the quiet rule uses, so the daemon cannot name the author. The app's
// own `rig.stopped` event can, and only that event makes a stopped rig ours to
// start again — once, inside StartBackoff.
func TestStoppedRigStartNeedsOurOwnStopRecord(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	home := t.TempDir()
	spec := preflight.SpecPath(home, "r")
	if err := os.MkdirAll(filepath.Dir(spec), 0o755); err != nil {
		t.Fatalf("spec dir: %v", err)
	}
	if err := os.WriteFile(spec, []byte("name: r\n"), 0o600); err != nil {
		t.Fatalf("spec: %v", err)
	}
	rigs := &fakeLifecycleRig{}
	now := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	lifecycle := &Lifecycle{
		Settings: config.LifecycleSettings{Enabled: true, IdleAfter: 10 * time.Minute},
		Executor: &LifecycleExecutor{Store: st, Rigs: rigs, Home: home, Now: func() time.Time { return now }},
	}
	// A stopped rig with ready work: every seat row is exited, exactly the
	// shape the incident left behind.
	facts := []LifecycleProjectFacts{{
		Key: "app", Rigs: []string{"r"}, Managed: true, Fresh: true, Ready: 3,
		Seats: []rig.Seat{{Rig: "r", Session: "dev-build-1@r", Lifecycle: "exited"}},
	}}

	// Somebody else stopped it: no rig.stopped event exists, so the tick leaves
	// the operator's decision alone.
	quietStops, err := st.QuietStoppedRigs(ctx)
	if err != nil {
		t.Fatalf("quiet stops: %v", err)
	}
	if actions := lifecycle.Decide(facts, true, false, quietStops, now); len(actions) != 0 {
		t.Fatalf("actions = %+v, want none for a rig the operator stopped", actions)
	}
	lifecycle.Start(ctx, nil)
	if rigs.upCalls != 0 {
		t.Fatalf("up calls = %d, want 0 while the stop is not ours", rigs.upCalls)
	}

	// Our own quiet stop is on the ledger: the rig is ours to revive.
	if _, err := st.AppendEvent(ctx, store.Event{
		Actor: "act", Kind: "rig.stopped", Subject: "r", Detail: "quiet 11m0s",
	}); err != nil {
		t.Fatalf("seed stop: %v", err)
	}
	quietStops, err = st.QuietStoppedRigs(ctx)
	if err != nil {
		t.Fatalf("quiet stops: %v", err)
	}
	actions := lifecycle.Decide(facts, true, false, quietStops, now)
	if len(actions) != 1 || actions[0].Kind != LifecycleStart {
		t.Fatalf("actions = %+v, want one start once the stop is ours", actions)
	}
	if !strings.HasPrefix(actions[0].Reason, "stopped_with_demand:") {
		t.Fatalf("reason = %q, want stopped_with_demand", actions[0].Reason)
	}
	lifecycle.Start(ctx, actions)
	if rigs.upCalls != 1 {
		t.Fatalf("up calls = %d, want 1", rigs.upCalls)
	}

	// The next tick a moment later still sees the demand; the backoff is what
	// keeps it to one launch, and the start it just recorded flips the ledger.
	now = now.Add(30 * time.Second)
	if actions := lifecycle.Decide(facts, true, false, quietStops, now); len(actions) != 1 {
		t.Fatalf("second tick actions = %+v, want the plan to still see demand", actions)
	}
	lifecycle.Start(ctx, actions)
	if rigs.upCalls != 1 {
		t.Fatalf("up calls = %d, want 1: the second tick is inside StartBackoff", rigs.upCalls)
	}
	quietStops, err = st.QuietStoppedRigs(ctx)
	if err != nil {
		t.Fatalf("quiet stops: %v", err)
	}
	if quietStops["r"] {
		t.Fatalf("quietStops = %+v, want the app's own start to clear the stop record", quietStops)
	}
}
