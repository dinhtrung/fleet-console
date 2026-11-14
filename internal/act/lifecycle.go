package act

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dinhtrung/fleet-console/internal/config"
	"github.com/dinhtrung/fleet-console/internal/preflight"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// LifecycleActionKind is one lifecycle verb.
type LifecycleActionKind string

const (
	// LifecycleStart launches a stopped rig that has work.
	LifecycleStart LifecycleActionKind = "start"
	// LifecycleStop stops a rig whose projects have all been quiet.
	LifecycleStop LifecycleActionKind = "stop"
)

// LifecycleAction is one start or stop, with everything the one log line and
// the one audit event need.
type LifecycleAction struct {
	Kind LifecycleActionKind
	Rig  string
	// Spec is the rig.yaml path a start launches.
	Spec string
	// Reason is why a start happened, for the log line.
	Reason string
	// Quiet is how long the rig had been quiet when a stop was decided.
	Quiet time.Duration
}

// LifecycleProject is one project bound to a rig, as the decision reads it.
type LifecycleProject struct {
	Key string
	// Managed is the project's effective `lifecycle:` value. An opted-out
	// project vetoes both the stop and the start of its rig (design D9).
	Managed bool
	// Fresh is false when this tick's forge read failed or was stale. A stale
	// read has no demand to offer and cannot open a stop window (design D3).
	Fresh bool
	// Ready is decide's dispatchable count on this project (decide.Result.Ready),
	// counted before the concurrency limit.
	Ready int
}

// LifecycleRig is one rig and every fact the decision needs about it. Nothing
// here is remembered from an earlier tick except QuietSince.
type LifecycleRig struct {
	Name string
	// SpecPath is <home>/.openrig/specs/rigs/<rig>/rig.yaml, the convention
	// internal/preflight already uses.
	SpecPath string
	// SeatsKnown is false when the rig read failed: with no roster this tick
	// cannot tell a stopped rig from an unread one, so it decides nothing.
	SeatsKnown bool
	// Running is the number of seats running on the rig: the only fact about
	// up or down (design D2).
	Running int
	// LiveRows counts queue rows that are neither terminal nor blocked on the
	// operator and whose destination is a seat on this rig. A live row holds the
	// rig open, and on a stopped rig it is the second arm of the start trigger
	// (design D1, D3).
	LiveRows int
	// QuietSince is the tick that opened the current quiet streak, zero when the
	// rig was busy on the previous tick.
	QuietSince time.Time
	// RigDown is the tick's daemon fact (epic #320, D2): the rig read failed
	// with ErrDaemonDown, so the rig cannot be up or quiet — a daemon that is
	// not running can hold no fleet. The start branch treats it as a stopped
	// rig with a known roster; the stop branch refuses it.
	RigDown bool
	// QuietStopped is true when this app's own quiet rule stopped the rig and
	// has not started it since (design D2, #403). The daemon cannot answer this:
	// `rig down` and `rig seat stop` are the same verbs the quiet rule uses, so
	// a stopped rig looks identical whoever stopped it. The app's ledger can —
	// only the executor writes `rig.stopped` — and only a rig it stopped is one
	// it may revive. A rig stopped by hand is the operator's decision: it stays
	// down until they say otherwise, because starting it would overrule them.
	QuietStopped bool
	// Projects are the projects bound to this rig.
	Projects []LifecycleProject
}

// LifecycleInput is one tick's whole lifecycle fact set: no clock, no I/O, no
// global state, so a table test owns the entire rule.
type LifecycleInput struct {
	Enabled   bool
	IdleAfter time.Duration
	Now       time.Time
	Rigs      []LifecycleRig
}

// LifecycleProjectFacts is one project's tick facts, as the lifecycle builder
// reads them: no forge call, no rig call, only what the tick already read.
type LifecycleProjectFacts struct {
	Key     string
	Rigs    []string
	Managed bool
	Fresh   bool
	Ready   int
	Rows    []rig.QueueRow
	Seats   []rig.Seat
}

// BuildLifecycleRigs groups one tick's project facts by rig and counts the seats
// and live rows on each. A rig two projects share gets one entry carrying both
// projects, so one busy project and one quiet project cannot produce a stop
// (design D3). It is pure; the queue is counted once per row, not once per
// project, so a shared rig's row is not counted twice. quietStops is the app's
// own stop ledger (`store.QuietStoppedRigs`), the only fact that says whether a
// stopped rig is ours to start again or the operator's to keep down (#403).
func BuildLifecycleRigs(projects []LifecycleProjectFacts, prevQuiet map[string]time.Time, seatsKnown bool, quietStops map[string]bool) []LifecycleRig {
	return buildLifecycleRigs(projects, prevQuiet, seatsKnown, false, quietStops)
}

// buildLifecycleRigs is the builder with the daemon-down fact. A down daemon is
// not an unread roster (epic #320, D1): nothing can be running on a daemon that
// does not exist, so the tick reports the roster as known, zero seats running,
// and marks the rig down. The start branch reads that as "stopped rig, decide
// on demand"; the stop branch keeps its own guard so it stays dark while down.
func buildLifecycleRigs(projects []LifecycleProjectFacts, prevQuiet map[string]time.Time, seatsKnown, rigDown bool, quietStops map[string]bool) []LifecycleRig {
	order := make([]string, 0, len(projects))
	byRig := make(map[string]*LifecycleRig, len(projects))
	link := func(rigName string) *LifecycleRig {
		entry, ok := byRig[rigName]
		if !ok {
			entry = &LifecycleRig{
				Name: rigName, SeatsKnown: seatsKnown, QuietSince: prevQuiet[rigName],
				QuietStopped: quietStops[rigName],
			}
			if rigDown {
				entry.SeatsKnown = true
				entry.RigDown = true
			}
			byRig[rigName] = entry
			order = append(order, rigName)
		}
		return entry
	}
	for _, project := range projects {
		for _, rigName := range project.Rigs {
			entry := link(rigName)
			entry.Projects = append(entry.Projects, LifecycleProject{
				Key: project.Key, Managed: project.Managed, Fresh: project.Fresh, Ready: project.Ready,
			})
		}
	}
	// A rig bound to two projects is read by both, so seats and rows are counted
	// by identity once: a doubled seat count would read as a live fleet, and a
	// doubled row would fool the claimable arm (design D3).
	countedSeat := make(map[string]bool)
	countedRow := make(map[string]bool)
	for _, project := range projects {
		for _, seat := range project.Seats {
			entry, ok := byRig[seat.Rig]
			if !ok || countedSeat[seat.Session] {
				continue
			}
			countedSeat[seat.Session] = true
			// Only a running seat is a live fleet. A stopped rig keeps its node
			// rows — they report exited, recoverable or attention_required — so
			// counting a row is how a stopped rig read as up and never started on
			// demand (#403). The spec's "running seats are the only fact about
			// whether a rig is up" is this line.
			if !seat.Running() {
				continue
			}
			entry.Running++
		}
		for _, row := range project.Rows {
			entry, ok := byRig[rigFromSeat(row.Destination)]
			if !ok || countedRow[row.ID] || !LiveRow(row) {
				continue
			}
			countedRow[row.ID] = true
			entry.LiveRows++
		}
	}
	out := make([]LifecycleRig, 0, len(order))
	for _, rigName := range order {
		out = append(out, *byRig[rigName])
	}
	return out
}

// Lifecycle is the tick's coordinator. It owns the one piece of process memory
// the rule needs — the tick each rig went quiet — and drives the executor. A
// disabled block makes Decide a no-op, so nothing else in the tick runs.
type Lifecycle struct {
	Settings config.LifecycleSettings
	Executor *LifecycleExecutor
	// quiet is memory-only: a restart forgets the streak, which can only delay a
	// stop, never hasten one (design D6).
	quiet map[string]time.Time
}

// NewLifecycle builds the coordinator from the resolved settings. The executor
// launches specs and stops rigs through the adapter, which is the only writer
// of `rig up` and `rig down`.
func NewLifecycle(settings config.LifecycleSettings, st *store.Store, rigs RigLifecycle, logf func(format string, args ...any)) *Lifecycle {
	return &Lifecycle{
		Settings: settings,
		Executor: &LifecycleExecutor{Store: st, Rigs: rigs, Logf: logf},
	}
}

// Decide builds this tick's actions and advances the quiet windows from the
// same predicate that decided them, so a failed start cannot look like a busy
// tick. The caller runs the starts before the dispatch phase and the stops
// after it. rigDown is the tick's daemon fact (epic #320, D2): a daemon that
// is not running means every rig on it is down, and the start branch decides
// on demand while the stop half stays dark. quietStops is the app's own stop
// ledger from `store.QuietStoppedRigs`: it is the fact that tells a rig this
// rule stopped from one the operator stopped (#403).
func (l *Lifecycle) Decide(projects []LifecycleProjectFacts, seatsKnown, rigDown bool, quietStops map[string]bool, now time.Time) []LifecycleAction {
	if l == nil || l.Executor == nil || !l.Settings.Enabled {
		return nil
	}
	if l.quiet == nil {
		l.quiet = make(map[string]time.Time)
	}
	rigs := buildLifecycleRigs(projects, l.quiet, seatsKnown, rigDown, quietStops)
	actions := DecideLifecycle(LifecycleInput{
		Enabled: l.Settings.Enabled, IdleAfter: l.Settings.IdleAfter, Now: now, Rigs: rigs,
	})
	for _, entry := range rigs {
		if entry.SeatsKnown && RigQuiet(entry) {
			l.quiet[entry.Name] = NextQuietSince(l.quiet[entry.Name], now, true)
			continue
		}
		delete(l.quiet, entry.Name)
	}
	return actions
}

// Start executes the start actions and returns the rigs whose launch failed,
// keyed by rig name, so the dispatch step skips their work (design D5).
func (l *Lifecycle) Start(ctx context.Context, actions []LifecycleAction) map[string]string {
	if l == nil || l.Executor == nil {
		return nil
	}
	return l.Executor.Start(ctx, actions)
}

// Stop executes the stop actions after the dispatch phase (task 3.3).
func (l *Lifecycle) Stop(ctx context.Context, actions []LifecycleAction) {
	if l == nil || l.Executor == nil {
		return
	}
	l.Executor.Stop(ctx, actions)
}

// DecideLifecycle is the pure lifecycle rule: which rigs to start because they
// have work and no seat, and which to stop because every project on them has
// been quiet for idle_after. It reads facts the tick already has; the one piece
// of memory it consults is the previous tick's QuietSince.
func DecideLifecycle(in LifecycleInput) []LifecycleAction {
	if !in.Enabled {
		return nil
	}
	var actions []LifecycleAction
	for _, rigEntry := range in.Rigs {
		// No roster: the tick can neither launch (which could duplicate a fleet
		// that is already up) nor stop (which could stop a live one). A daemon
		// that is not running is the exception on the start half only: nothing
		// can be running on it, so the roster is known and empty (epic #320, D2).
		if !rigEntry.SeatsKnown {
			continue
		}
		// An opted-out project keeps its rig warm: no start, no stop.
		if !RigManaged(rigEntry) {
			continue
		}
		if rigEntry.Running == 0 || rigEntry.RigDown {
			// A stopped rig is started before anything is written for it, and
			// only when work exists. An idle stopped rig stays stopped.
			//
			// Who stopped it decides whether we may start it at all (#403):
			// only a rig this app stopped for being quiet is ours to revive.
			// `rig down` and `rig seat stop` use the same daemon verb, so the
			// daemon cannot name the author — the app's own `rig.stopped` event
			// is the one record that can, and a rig with no such stop was
			// stopped by a person. Starting that would overrule a decision the
			// human made on purpose. A down daemon is exempt: nothing a person
			// did takes a daemon down, and the accepted spec starts the rig
			// when demand exists.
			if !rigEntry.RigDown && !rigEntry.QuietStopped {
				continue
			}
			// A rig whose daemon is down joins this arm with its demand decided
			// the same way; the stop arm below stays out of reach because the
			// down guard comes first here and RigQuiet cannot hold while down.
			if reason, ok := startReason(rigEntry); ok {
				actions = append(actions, LifecycleAction{
					Kind: LifecycleStart, Rig: rigEntry.Name, Spec: rigEntry.SpecPath, Reason: reason,
				})
			}
			continue
		}
		// A seat may be working: the rig stays up whatever the window says.
		if !RigQuiet(rigEntry) {
			continue
		}
		// The window opens on the first quiet tick, so a streak shorter than
		// idle_after is not yet a stop (design D6).
		if rigEntry.QuietSince.IsZero() || in.Now.Sub(rigEntry.QuietSince) < in.IdleAfter {
			continue
		}
		actions = append(actions, LifecycleAction{
			Kind: LifecycleStop, Rig: rigEntry.Name, Quiet: in.Now.Sub(rigEntry.QuietSince),
		})
	}
	return actions
}

// RigManaged reports whether the app manages a rig's lifecycle at all: the
// global opt-in is on and at least one bound project did not opt out.
func RigManaged(rigEntry LifecycleRig) bool {
	for _, project := range rigEntry.Projects {
		if project.Managed {
			return true
		}
	}
	return false
}

// RigQuiet reports whether this tick found the rig quiet: no live row on it and
// every bound project fresh, managed, with no demand. An opted-out project is
// never quiet, which is what keeps its rig warm. A rig whose daemon is down is
// never quiet either (epic #320, D2): a fleet that does not exist cannot be
// quiet, and a window opened while down would stop the rig the moment the
// daemon came back.
func RigQuiet(rigEntry LifecycleRig) bool {
	if rigEntry.RigDown || rigEntry.LiveRows > 0 || len(rigEntry.Projects) == 0 {
		return false
	}
	for _, project := range rigEntry.Projects {
		if !project.Managed || !project.Fresh || project.Ready > 0 {
			return false
		}
	}
	return true
}

// NextQuietSince advances the quiet window after one tick: a busy tick closes
// it, the first quiet tick opens it, and a continuing streak keeps its opening
// stamp. It shares RigQuiet's predicate, so the window and the stop cannot
// disagree about which tick was quiet.
func NextQuietSince(prev, now time.Time, quiet bool) time.Time {
	if !quiet {
		return time.Time{}
	}
	if prev.IsZero() {
		return now
	}
	return prev
}

// startReason names why a stopped rig should come up, or false when nothing
// needs it. Demand is a fresh managed project with ready items and no seat to
// take them; the other arm is a live row naming one of the rig's seats, which
// no plan intent carries (design D1).
func startReason(rigEntry LifecycleRig) (string, bool) {
	ready := 0
	for _, project := range rigEntry.Projects {
		// A stale read cannot vouch for demand: an empty item list from a
		// broken read is not an empty board.
		if !project.Managed || !project.Fresh {
			continue
		}
		ready += project.Ready
	}
	if ready > 0 {
		if rigEntry.RigDown {
			return fmt.Sprintf("daemon down; %d ready item(s)", ready), true
		}
		return fmt.Sprintf("stopped_with_demand: %d ready item(s), no running seat", ready), true
	}
	if rigEntry.LiveRows > 0 {
		return "a claimable row names a seat and no seat is running", true
	}
	return "", false
}

// LiveRow reports whether a queue row is live for the lifecycle rule: neither
// terminal nor blocked on the operator. A row blocked on another row is live,
// because a seat can still act on it; a row on a person is not, because no seat
// can, and it stays in the daemon's queue either way.
func LiveRow(row rig.QueueRow) bool {
	state := strings.ToLower(row.PickupState)
	if state == "" {
		state = strings.ToLower(row.State)
	}
	if _, terminal := reconcileTerminal[state]; terminal {
		return false
	}
	return !rig.WaitsOnHuman(row.State, row.Destination, row.BlockedOn)
}

// RigLifecycle is the rig verbs the lifecycle drives. *rig.Adapter implements
// it; a test supplies a recorder so no real fleet is touched.
type RigLifecycle interface {
	Up(ctx context.Context, specPath string) (*rig.RunReport, error)
	Down(ctx context.Context, rigName string, deleteRig bool) (*rig.DownReport, error)
	// Export writes the rig's spec to specPath (#431): a rig created from a
	// bundle exists only in the database, and the start needs a spec file.
	Export(ctx context.Context, rigName, specPath string) error
}

// LifecycleExecutor applies lifecycle actions: it launches a spec, stops a rig
// without --delete, and writes one log line and one audit event per action. A
// failure is logged once per change (the mirror.note pattern) and never claims
// a success it did not have.
type LifecycleExecutor struct {
	Store *store.Store
	Rigs  RigLifecycle
	// Home is where the rig specs live; it defaults to the process home.
	Home string
	Logf func(format string, args ...any)
	// seen remembers the last failure message per key, so a permanently broken
	// `rig up` does not fill the journal.
	seen map[string]string
	// lastStart remembers each rig's last attempt (success or failure), so a
	// failing start is not hammered every tick (epic #320, D4). Memory-only: a
	// restart forgets it, which can only delay a start by one window.
	lastStart map[string]time.Time
	// Now pins the clock for tests; the zero value reads time.Now.
	Now func() time.Time
}

// StartBackoff is the window one rig's start attempt holds (epic #320, D4): at
// a 15 s tick cadence a failing `rig up` must not be retried every tick. A
// constant, not a config key — the timer is not an operator decision.
const StartBackoff = 5 * time.Minute

// Start applies every start action and returns, per rig, the launch failure, so
// the dispatch step can skip that rig's work. It is the only writer of `rig up`.
// An attempt inside StartBackoff of the rig's last attempt is refused with one
// note-deduplicated line, and the deferral is not a failure: the map stays
// empty, the dispatch step does not skip the rig's work on it.
func (e *LifecycleExecutor) Start(ctx context.Context, actions []LifecycleAction) map[string]string {
	failed := make(map[string]string)
	for _, action := range actions {
		if action.Kind != LifecycleStart {
			continue
		}
		if e.deferred(action.Rig) {
			continue
		}
		if err := e.start(ctx, action); err != nil {
			failed[action.Rig] = err.Error()
		}
	}
	if len(failed) == 0 {
		return nil
	}
	return failed
}

// deferred reports whether the rig is inside its start backoff window, and
// logs the wait once per change when it is. Whether deferred or attempted,
// every tick that reaches the window's edge with demand still standing
// records the attempt time.
func (e *LifecycleExecutor) deferred(rig string) bool {
	now := e.now()
	if last, ok := e.lastStart[rig]; ok {
		if elapsed := now.Sub(last); elapsed < StartBackoff {
			e.note("rig "+rig+" up deferred",
				fmt.Sprintf("%s up deferred — last attempt %s ago", rig, elapsed.Round(time.Second)))
			return true
		}
	}
	if e.lastStart == nil {
		e.lastStart = make(map[string]time.Time)
	}
	e.lastStart[rig] = now
	return false
}

func (e *LifecycleExecutor) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *LifecycleExecutor) start(ctx context.Context, action LifecycleAction) error {
	spec := action.Spec
	if spec == "" {
		home := e.Home
		if home == "" {
			home, _ = os.UserHomeDir()
		}
		spec = preflight.SpecPath(home, action.Rig)
	}
	if _, err := os.Stat(spec); err != nil {
		if !os.IsNotExist(err) {
			e.note("rig "+action.Rig+" up", fmt.Sprintf("%s up failed: %v", action.Rig, err))
			return err
		}
		// A rig imported from a bundle (#389) has a database row and no spec
		// file at the conventional path. Export it on demand instead of leaving
		// the rig unstartable — a missing file is a resolvable fact, not a
		// verdict (#431).
		var resolveErr error
		if mkErr := os.MkdirAll(filepath.Dir(spec), 0o755); mkErr != nil {
			resolveErr = fmt.Errorf("no rig spec at %s: %w", spec, mkErr)
		} else if exportErr := e.Rigs.Export(ctx, action.Rig, spec); exportErr != nil {
			resolveErr = fmt.Errorf("no rig spec at %s and export failed: %w", spec, exportErr)
		} else if _, statErr := os.Stat(spec); statErr != nil {
			resolveErr = fmt.Errorf("no rig spec at %s (export wrote nothing)", spec)
		}
		if resolveErr != nil {
			e.note("rig "+action.Rig+" up", fmt.Sprintf("%s up failed: %v", action.Rig, resolveErr))
			return resolveErr
		}
	}
	if _, err := e.Rigs.Up(ctx, spec); err != nil {
		e.note("rig "+action.Rig+" up", fmt.Sprintf("%s up failed: %v", action.Rig, err))
		return err
	}
	e.clearNote("rig " + action.Rig + " up")
	e.logf("rig-lifecycle: %s started — %s", action.Rig, action.Reason)
	if _, err := e.Store.AppendEvent(ctx, store.Event{
		Actor: "act", Kind: "rig.started", Subject: action.Rig, Detail: action.Reason,
	}); err != nil {
		e.note("rig "+action.Rig+" started audit", fmt.Sprintf("%s started, audit gap: %v", action.Rig, err))
	}
	return nil
}

// Stop applies every stop action with `rig down <name>` and never `--delete`
// (design D4). It is the only writer of `rig down`.
func (e *LifecycleExecutor) Stop(ctx context.Context, actions []LifecycleAction) {
	for _, action := range actions {
		if action.Kind != LifecycleStop {
			continue
		}
		if _, err := e.Rigs.Down(ctx, action.Rig, false); err != nil {
			e.note("rig "+action.Rig+" down", fmt.Sprintf("%s down failed: %v", action.Rig, err))
			continue
		}
		e.clearNote("rig " + action.Rig + " down")
		quiet := action.Quiet.Round(time.Second)
		e.logf("rig-lifecycle: %s stopped — quiet %s", action.Rig, quiet)
		if _, err := e.Store.AppendEvent(ctx, store.Event{
			Actor: "act", Kind: "rig.stopped", Subject: action.Rig, Detail: "quiet " + quiet.String(),
		}); err != nil {
			e.note("rig "+action.Rig+" stopped audit", fmt.Sprintf("%s stopped, audit gap: %v", action.Rig, err))
		}
	}
}

func (e *LifecycleExecutor) logf(format string, args ...any) {
	if e.Logf != nil {
		e.Logf(format, args...)
	}
}

// note logs one failure once per change. A recovered key forgets its message,
// so a recurrence is logged again.
func (e *LifecycleExecutor) note(key, message string) {
	if e.seen == nil {
		e.seen = make(map[string]string)
	}
	if e.seen[key] == message {
		return
	}
	e.seen[key] = message
	e.logf("rig-lifecycle: %s", message)
}

func (e *LifecycleExecutor) clearNote(key string) {
	delete(e.seen, key)
}
