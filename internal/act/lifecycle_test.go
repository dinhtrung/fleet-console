package act

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/config"
	"github.com/dinhtrung/fleet-console/internal/rig"
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
			name: "start on Ready>0 and no running seat",
			input: func() LifecycleInput {
				entry := managedRig("r")
				entry.Running = 0
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

	rigs := BuildLifecycleRigs(projects, nil, true)

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

	if actions := lifecycle.Decide(facts, true, t0); len(actions) != 0 {
		t.Fatalf("first quiet tick actions = %+v, want none", actions)
	}
	if got := lifecycle.quiet["r"]; !got.Equal(t0) {
		t.Fatalf("window = %s, want it opened at %s", got, t0)
	}
	if actions := lifecycle.Decide(facts, true, t0.Add(time.Minute)); len(actions) != 0 {
		t.Fatalf("second quiet tick actions = %+v, want none yet", actions)
	}
	if got := lifecycle.quiet["r"]; !got.Equal(t0) {
		t.Fatalf("window moved to %s, want it held at %s", got, t0)
	}
	if actions := lifecycle.Decide(facts, true, t0.Add(11*time.Minute)); len(actions) != 1 || actions[0].Kind != LifecycleStop {
		t.Fatalf("actions after the window = %+v, want one stop", actions)
	}
}

// TestLifecycleDisabledDecidesNothing: with the block absent the box behaves
// exactly as today.
func TestLifecycleDisabledDecidesNothing(t *testing.T) {
	lifecycle := &Lifecycle{Settings: config.LifecycleSettings{}, Executor: &LifecycleExecutor{}}
	facts := []LifecycleProjectFacts{{Key: "app", Rigs: []string{"r"}, Managed: true, Fresh: true, Ready: 5}}
	if actions := lifecycle.Decide(facts, true, time.Now()); actions != nil {
		t.Fatalf("actions = %+v, want none while disabled", actions)
	}
}

// TestRigLifecycleExecutorLogsOncePerChange pins D8: a permanently broken `rig
// up` writes one line, not one per tick.
func TestRigLifecycleExecutorLogsOncePerChange(t *testing.T) {
	st := newStore(t)
	var lines []string
	rigs := &fakeLifecycleRig{upErr: errString("boom")}
	executor := &LifecycleExecutor{Store: st, Rigs: rigs, Logf: func(format string, args ...any) {
		lines = append(lines, format)
	}}
	spec := writeSpec(t)
	action := LifecycleAction{Kind: LifecycleStart, Rig: "r", Spec: spec, Reason: "demand"}

	if failed := executor.Start(context.Background(), []LifecycleAction{action}); failed == nil {
		t.Fatal("a failed Up was reported as a success")
	}
	if failed := executor.Start(context.Background(), []LifecycleAction{action}); failed == nil {
		t.Fatal("the retry reported a success")
	}
	if len(lines) != 1 {
		t.Fatalf("log lines = %v, want one for the unchanged failure", lines)
	}
	if rigs.upCalls != 2 {
		t.Fatalf("up calls = %d, want 2 (the next tick retries)", rigs.upCalls)
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
	if lines[len(lines)-1] != "rig-lifecycle: %s stopped — quiet %s" {
		t.Fatalf("stop line = %q", lines[len(lines)-1])
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
func TestRigLifecycleMissingSpecNamesThePath(t *testing.T) {
	var lines []string
	executor := &LifecycleExecutor{
		Store: newStore(t), Rigs: &fakeLifecycleRig{}, Home: t.TempDir(),
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
	upErr      error
	upCalls    int
	downNames  []string
	downDelete bool
}

func (f *fakeLifecycleRig) Up(context.Context, string) (*rig.RunReport, error) {
	f.upCalls++
	if f.upErr != nil {
		return nil, f.upErr
	}
	return &rig.RunReport{Status: "ok"}, nil
}

func (f *fakeLifecycleRig) Down(_ context.Context, name string, deleteRig bool) (*rig.DownReport, error) {
	f.downNames = append(f.downNames, name)
	f.downDelete = deleteRig
	return &rig.DownReport{RigName: name}, nil
}
