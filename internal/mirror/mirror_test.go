package mirror

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/decide"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "fleet.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func project(t *testing.T, st *store.Store) int64 {
	t.Helper()
	id, err := st.CreateProject(context.Background(), store.ProjectInput{
		Key: "app", Name: "app", ForgeKind: "github", ForgeProject: "dinhtrung/fleet-console",
		Rig: "fleet-console", ConcurrencyCap: 2, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return id
}

func TestProjectTimeoutScalesFromCachedItems(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	id := project(t, st)
	m := &Mirror{Store: st, ProjectTimeout: TimeoutSettings{
		Base: 10 * time.Second, PerItem: time.Second, Ceiling: 20 * time.Second,
	}}
	if got := m.projectTimeoutFor(ctx, id); got != 10*time.Second {
		t.Fatalf("first tick timeout = %s, want the base 10s", got)
	}
	records := make([]store.ItemRecord, 0, 15)
	for number := 1; number <= 15; number++ {
		records = append(records, store.ItemRecord{
			Number: number, Kind: "task", State: "open", Title: "T", URL: "u", UpdatedAt: time.Now().UTC(),
		})
	}
	if err := st.ReplaceItems(ctx, id, records); err != nil {
		t.Fatalf("items: %v", err)
	}
	if got := m.projectTimeoutFor(ctx, id); got != 20*time.Second {
		t.Fatalf("15 items timeout = %s, want the ceiling 20s", got)
	}
	if err := st.ReplaceItems(ctx, id, records[:3]); err != nil {
		t.Fatalf("items: %v", err)
	}
	if got := m.projectTimeoutFor(ctx, id); got != 13*time.Second {
		t.Fatalf("3 items timeout = %s, want base + 3*per_item = 13s", got)
	}
}

func TestMirrorStaleKeepsCachedRowsAndStamp(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	id := project(t, st)
	fake := forge.NewFake(forge.KindGitHub)
	fake.ItemsValue = []forge.Item{{Number: 7, Kind: forge.KindTask, State: forge.StateOpen, Title: "One", URL: "u"}}

	first := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	m := &Mirror{Store: st, Now: func() time.Time { return first }}
	if _, err := m.Tick(ctx, []Project{{ID: id, Key: "app", Forge: fake}}); err != nil {
		t.Fatalf("tick: %v", err)
	}
	items, err := st.Items(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("items = %+v, %v", items, err)
	}
	status, err := st.MirrorStatus(ctx, id)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.ForgeStale || status.LastForgeAt == nil || !status.LastForgeAt.Equal(first) {
		t.Fatalf("status = %+v", status)
	}

	fake.ReadErr = forge.ErrStale
	second := first.Add(time.Minute)
	m.Now = func() time.Time { return second }
	if _, err := m.Tick(ctx, []Project{{ID: id, Key: "app", Forge: fake}}); err != nil {
		t.Fatalf("stale tick: %v", err)
	}
	items, _ = st.Items(ctx)
	if len(items) != 1 || items[0].Number != 7 {
		t.Fatalf("stale tick emptied or changed the item rows: %+v", items)
	}
	status, _ = st.MirrorStatus(ctx, id)
	if !status.ForgeStale || status.ForgeError == "" {
		t.Fatalf("status = %+v, want stale with the error", status)
	}
	if status.LastForgeAt == nil || !status.LastForgeAt.Equal(first) {
		t.Fatalf("stamp = %v, want the previous successful tick %v", status.LastForgeAt, first)
	}
}

func TestMirrorWritesSnapshots(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	id := project(t, st)
	fake := forge.NewFake(forge.KindGitHub)
	fake.ItemsValue = []forge.Item{{Number: 7, Kind: forge.KindTask, State: forge.StateOpen, Title: "One"}}
	gitRunner := runnerFunc(func(context.Context, ...string) ([]byte, error) {
		return []byte("abc\trefs/heads/feature/7-one\n"), nil
	})
	m := &Mirror{Store: st, Now: func() time.Time { return time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC) }}
	if _, err := m.Tick(ctx, []Project{{ID: id, Key: "app", Forge: fake, Git: gitRunner}}); err != nil {
		t.Fatalf("tick: %v", err)
	}
	for _, kind := range []string{"forge", "branches", "status", "rig_queue", "rig_seats"} {
		payload, at, err := st.GetSnapshot(ctx, id, kind)
		if err != nil || len(payload) == 0 || at.IsZero() {
			t.Fatalf("snapshot %s = %s, %v, %v", kind, payload, at, err)
		}
		if kind == "branches" && !strings.Contains(string(payload), "feature/7-one") {
			t.Fatalf("branches payload = %s", payload)
		}
	}
}

type runnerFunc func(context.Context, ...string) ([]byte, error)

func (f runnerFunc) Run(ctx context.Context, args ...string) ([]byte, error) { return f(ctx, args...) }

func TestLoopTicksAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := newStore(t)
	id := project(t, st)
	fake := forge.NewFake(forge.KindGitHub)
	fake.ItemsValue = []forge.Item{{Number: 7, Kind: forge.KindTask, State: forge.StateOpen, Title: "One"}}
	var ticks int32
	done := make(chan struct{})
	m := &Mirror{Store: st}
	go func() {
		m.Loop(ctx,
			func(context.Context) ([]Project, error) { return []Project{{ID: id, Key: "app", Forge: fake}}, nil },
			Cadence{Base: 5 * time.Millisecond, Max: 5 * time.Millisecond}, time.Millisecond,
			func(TickResult) bool {
				// Busy: an open item, so the wait stays at the base and the
				// second tick arrives promptly.
				if atomic.AddInt32(&ticks, 1) >= 2 {
					cancel()
				}
				return true
			}, nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("loop did not stop after cancel")
	}
	if atomic.LoadInt32(&ticks) < 2 {
		t.Fatalf("ticks = %d, want at least 2", ticks)
	}
}

func TestForgeForbiddenIsNamed(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	id := project(t, st)
	fake := forge.NewFake(forge.KindGitHub)
	fake.ItemsValue = []forge.Item{{Number: 7, Kind: forge.KindTask, State: forge.StateOpen, Title: "One"}}

	first := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	m := &Mirror{Store: st, Now: func() time.Time { return first }}
	if _, err := m.Tick(ctx, []Project{{ID: id, Key: "app", Forge: fake}}); err != nil {
		t.Fatalf("tick: %v", err)
	}

	fake.ReadErr = &forge.ForbiddenError{
		Operation: "gh api orgs/github/actions/permissions",
		Detail:    "You must be an org admin. (HTTP 403)",
	}
	m.Now = func() time.Time { return first.Add(time.Minute) }
	if _, err := m.Tick(ctx, []Project{{ID: id, Key: "app", Forge: fake}}); err != nil {
		t.Fatalf("forbidden tick: %v", err)
	}
	status, err := st.MirrorStatus(ctx, id)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.ForgeForbidden {
		t.Fatalf("status = %+v, want forge_forbidden", status)
	}
	if status.ForgeError != fake.ReadErr.Error() {
		t.Fatalf("forge_error = %q, want %q", status.ForgeError, fake.ReadErr.Error())
	}
	if status.LastForgeAt == nil || !status.LastForgeAt.Equal(first) {
		t.Fatalf("stamp = %v, want the previous success", status.LastForgeAt)
	}
}

func TestMirrorKeepsItemsWhenChangeRequestsFail(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	id := project(t, st)
	fake := forge.NewFake(forge.KindGitLab)
	fake.ItemsValue = []forge.Item{{Number: 7, Kind: forge.KindTask, State: forge.StateOpen, Title: "One"}}
	fake.RequestsValue = []forge.ChangeRequest{{Number: 70, State: "open", ClosesItem: 7}}

	first := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	m := &Mirror{Store: st, Now: func() time.Time { return first }}
	if _, err := m.Tick(ctx, []Project{{ID: id, Key: "app", Forge: fake}}); err != nil {
		t.Fatalf("tick: %v", err)
	}

	fake.ItemsValue = append(fake.ItemsValue, forge.Item{Number: 8, Kind: forge.KindTask, State: forge.StateOpen, Title: "Two"})
	fake.RequestsErr = errors.New("forge: stale: HTTP 500")
	m.Now = func() time.Time { return first.Add(time.Minute) }
	if _, err := m.Tick(ctx, []Project{{ID: id, Key: "app", Forge: fake}}); err != nil {
		t.Fatalf("cr-failed tick: %v", err)
	}

	items, err := st.Items(ctx)
	if err != nil {
		t.Fatalf("items: %v", err)
	}
	if len(items) != 2 || items[1].Number != 8 {
		t.Fatalf("items = %+v, want both open items after a CR failure", items)
	}
	status, err := st.MirrorStatus(ctx, id)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.ForgeStale || !strings.Contains(status.ForgeError, "HTTP 500") {
		t.Fatalf("status = %+v, want stale with the CR error", status)
	}
	if status.LastForgeAt == nil || !status.LastForgeAt.Equal(first) {
		t.Fatalf("stamp = %v, want the previous complete read %v", status.LastForgeAt, first)
	}
}

type hangingForge struct{ *forge.Fake }

func (h hangingForge) Items(ctx context.Context, timeout forge.Timeout) ([]forge.Item, error) {
	limitCtx, cancel := context.WithTimeout(ctx, timeout.List())
	defer cancel()
	<-limitCtx.Done()
	return nil, limitCtx.Err()
}

func TestMirrorBoundsOneProjectRead(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	id := project(t, st)
	slow := hangingForge{Fake: forge.NewFake(forge.KindGitLab)}
	m := &Mirror{Store: st, ProjectTimeout: TimeoutSettings{Base: 30 * time.Millisecond, Ceiling: 30 * time.Millisecond}}
	started := time.Now()
	if _, err := m.Tick(ctx, []Project{{ID: id, Key: "app", Forge: slow}}); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("tick took %s, want the project deadline to bound it", elapsed)
	}
	status, err := st.MirrorStatus(ctx, id)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.ForgeStale || !strings.Contains(status.ForgeError, "deadline exceeded") {
		t.Fatalf("status = %+v, want a deadline error", status)
	}
}

func TestMirrorLogsEachStaleReasonOnce(t *testing.T) {
	var lines []string
	m := &Mirror{Logf: func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }}
	seen := map[string]string{}
	result := TickResult{
		Projects: map[string]ProjectResult{"app": {ForgeError: "boom"}},
		RigError: "rig down",
	}
	m.noteProjects(seen, result)
	m.noteProjects(seen, result)
	if len(lines) != 2 {
		t.Fatalf("lines = %v, want one per changed reason", lines)
	}
	result.Projects["app"] = ProjectResult{ForgeError: "boom 2"}
	m.noteProjects(seen, result)
	if len(lines) != 3 {
		t.Fatalf("lines = %v, want the changed reason logged", lines)
	}
	result.Projects["app"] = ProjectResult{}
	result.RigError = ""
	m.noteProjects(seen, result)
	if len(lines) != 3 {
		t.Fatalf("lines = %v, recovery must not log", lines)
	}
	result.Projects["app"] = ProjectResult{ForgeError: "boom"}
	m.noteProjects(seen, result)
	if len(lines) != 4 {
		t.Fatalf("lines = %v, want a recurrence logged after recovery", lines)
	}
}

func TestMirrorPassesAcceptedVerdictSHAs(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	id := project(t, st)
	dispatchID, err := st.CreateDispatch(ctx, store.NewDispatch{
		ProjectID: id, ItemNumber: 7, Attempt: 1, Seat: "s", Branch: "b", Worktree: "w", BaseSHA: "base",
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if _, err := st.AddVerdict(ctx, store.NewVerdict{
		DispatchID: dispatchID, SHA: "ABC123", Decision: "accept", Source: "review",
	}); err != nil {
		t.Fatalf("verdict: %v", err)
	}
	fake := forge.NewFake(forge.KindGitLab)
	fake.ItemsValue = []forge.Item{{Number: 7, Kind: forge.KindTask, State: forge.StateOpen, Title: "One"}}
	m := &Mirror{Store: st}
	if _, err := m.Tick(ctx, []Project{{ID: id, Key: "app", Forge: fake}}); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if _, ok := fake.Accepted["abc123"]; !ok {
		t.Fatalf("accepted shas = %v, want the ACCEPT verdict head lowercased", fake.Accepted)
	}
}

// TestBuildSnapshotIgnoresEndedDispatches: an ended attempt is not a live
// duplicate; reconciliation ends it before planning so the item can be retried.
func TestBuildSnapshotIgnoresEndedDispatches(t *testing.T) {
	at := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	ended := at.Format(time.RFC3339Nano)
	project := store.ProjectRow{ID: 1, Key: "app", ForgeKind: "github", Enabled: true}
	dispatches := []store.DispatchRow{
		{ItemNumber: 5, QItemID: "open", CreatedAt: at.Format(time.RFC3339Nano)},
		{ItemNumber: 6, QItemID: "ended", CreatedAt: at.Format(time.RFC3339Nano), EndedAt: &ended, EndReason: "superseded"},
	}
	snapshot := BuildSnapshot(project, ProjectResult{}, dispatches, nil, nil, nil)
	if len(snapshot.Dispatched) != 1 || snapshot.Dispatched[0] != 5 {
		t.Fatalf("dispatched = %v, want only the open attempt #5", snapshot.Dispatched)
	}
}

func TestSeatsFollowRigOrderAndMissingRigsWarn(t *testing.T) {
	seats := []rig.Seat{
		{Rig: "rig-b", Session: "build@rig-b"},
		{Rig: "other", Session: "build@other"},
		{Rig: "rig-a", Session: "build@rig-a"},
	}
	got := filterSeats(seats, []string{"rig-a", "rig-b"})
	if len(got) != 2 || got[0].Rig != "rig-a" || got[1].Rig != "rig-b" {
		t.Fatalf("filtered seats = %+v, want the configured order", got)
	}
	missing := missingRigs([]string{"rig-a", "ghost"}, seats)
	if len(missing) != 1 || missing[0] != "ghost" {
		t.Fatalf("missing = %v, want [ghost]", missing)
	}
	if len(missingRigs([]string{"rig-a", "rig-b"}, seats)) != 0 {
		t.Fatal("known rigs reported missing")
	}
}

func TestFailedSeatReadNamesNoGhostRig(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	id, err := st.CreateProject(ctx, store.ProjectInput{
		Key: "app", Name: "app", ForgeKind: "github", ForgeProject: "o/app",
		Rigs: []string{"rig-a", "rig-b"}, ConcurrencyCap: 2, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	runner := runnerFunc(func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "ps" {
			return nil, errors.New("rig daemon unreachable")
		}
		return []byte("[]"), nil
	})
	m := &Mirror{Store: st, Rig: rig.NewAdapter(runner)}
	read, err := m.Tick(ctx, []Project{{ID: id, Key: "app", Forge: forge.NewFake(forge.KindGitHub), Rigs: []string{"rig-a", "rig-b"}}})
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	pr := read.Projects["app"]
	if !read.RigStale || read.RigError == "" {
		t.Fatalf("stale=%v error=%q, want the failed read recorded", read.RigStale, read.RigError)
	}
	if len(pr.RigWarnings) != 0 {
		t.Fatalf("warnings = %v, want none: a failed read cannot call rig-a and rig-b ghosts", pr.RigWarnings)
	}

	// The same mirror with an answering read still names the ghost.
	healthy := runnerFunc(func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "ps" {
			return []byte(`[{"nodeId":"n1","rigName":"rig-a","canonicalSessionName":"dev@rig-a","logicalId":"dev","lifecycleState":"running","sessionStatus":"running"}]`), nil
		}
		return []byte("[]"), nil
	})
	m.Rig = rig.NewAdapter(healthy)
	read, err = m.Tick(ctx, []Project{{ID: id, Key: "app", Forge: forge.NewFake(forge.KindGitHub), Rigs: []string{"rig-a", "rig-b"}}})
	if err != nil {
		t.Fatalf("healthy tick: %v", err)
	}
	warnings := read.Projects["app"].RigWarnings
	if len(warnings) != 1 || warnings[0] != "rig-b" {
		t.Fatalf("warnings = %v, want [rig-b]", warnings)
	}
}

// The safety net the object form defeated. A project that declares tiers persists an
// object form in rigs_json; if the name list collapses to the first entry, the mirror's
// own filters drop every row and seat on the later rigs, and missingRigs cannot name a
// rig that is not in the list. This drives a real database round-trip and then the real
// filters, so it fails if the two views of the column ever disagree again.
func TestTierRigsSurviveTheRoundTripAndTheFilters(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	id, err := st.CreateProject(ctx, store.ProjectInput{
		Key: "app", Name: "app", ForgeKind: "github", ForgeProject: "o/r",
		Rig: "primary", Rigs: []string{"primary"},
		RigTiers: []store.RigTier{
			{Name: "primary", Tier: 0, Spill: "on-full"},
			{Name: "standby", Tier: 1, Spill: "on-unavailable"},
			{Name: "cold", Tier: 2, Spill: "never"},
		},
		ConcurrencyCap: 2, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	projects, err := st.Projects(ctx)
	if err != nil {
		t.Fatalf("projects: %v", err)
	}
	if len(projects) != 1 || projects[0].ID != id {
		t.Fatalf("projects = %+v, want the one created", projects)
	}
	project := projects[0]

	if len(project.Rigs) != 3 {
		t.Fatalf("Rigs = %v, want all three names from the object form", project.Rigs)
	}
	want := []string{"primary", "standby", "cold"}
	for i, name := range want {
		if project.Rigs[i] != name {
			t.Fatalf("Rigs = %v, want %v in declaration order", project.Rigs, want)
		}
	}

	// The mirror's filters use that list. Only a tier-0 seat is running, so both
	// later rigs must be reported missing, and a row on a later rig must survive.
	seats := []rig.Seat{{Rig: "primary", Session: "build@primary"}}
	if missing := missingRigs(project.Rigs, seats); len(missing) != 2 {
		t.Fatalf("missingRigs = %v, want standby and cold named", missing)
	}
	rows := []rig.QueueRow{
		{ID: "q1", Destination: "build@primary"},
		{ID: "q2", Destination: "build@standby"},
		{ID: "q9", Destination: "build@elsewhere"},
	}
	if got := filterRows(rows, project.Rigs); len(got) != 2 {
		t.Fatalf("filterRows = %d rows, want 2: a tier-1 row was dropped or a foreign one kept", len(got))
	}
}

// seatNode is one `rig ps --nodes` row: a running builder seat on a rig.
func seatNode(rigName, logicalID string) string {
	return fmt.Sprintf(`{"nodeId":"n-%s-%s","rigName":%q,"canonicalSessionName":"%s@%s","logicalId":%q,"lifecycleState":"running","sessionStatus":"running"}`,
		rigName, logicalID, rigName, "build", rigName, logicalID)
}

// twoRigProject binds app to rig-a and rig-b: the shape #438 is about, where a
// project's capacity is the sum over its rigs.
func twoRigFixture(t *testing.T) (*store.Store, int64) {
	t.Helper()
	st := newStore(t)
	id, err := st.CreateProject(context.Background(), store.ProjectInput{
		Key: "app", Name: "app", ForgeKind: "github", ForgeProject: "o/app",
		Rigs: []string{"rig-a", "rig-b"}, ConcurrencyCap: 2, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return st, id
}

// TestOneUnreadRigDoesNotBlankTheOther is #438's scenario: the fleet-wide roster
// read fails, rig-b cannot answer on its own either, and rig-a's seats must
// survive — capacity comes from the rigs that answered.
func TestOneUnreadRigDoesNotBlankTheOther(t *testing.T) {
	ctx := context.Background()
	st, id := twoRigFixture(t)
	runner := runnerFunc(func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) == 0 || args[0] != "ps" {
			return []byte("[]"), nil
		}
		switch {
		case args[1] == "--nodes" && len(args) > 2 && args[2] == "-A":
			return nil, errors.New("rig ps --nodes -A: exit 1: node listing timed out")
		case args[len(args)-1] == "rig-a":
			return []byte("[" + seatNode("rig-a", "build") + "]"), nil
		default:
			return nil, errors.New("rig ps --nodes --rig rig-b: exit 1: daemon refused")
		}
	})
	m := &Mirror{Store: st, Rig: rig.NewAdapter(runner)}
	read, err := m.Tick(ctx, []Project{{ID: id, Key: "app", Forge: forge.NewFake(forge.KindGitHub), Rigs: []string{"rig-a", "rig-b"}}})
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	pr := read.Projects["app"]
	if len(pr.Seats) != 1 || pr.Seats[0].Rig != "rig-a" {
		t.Fatalf("seats = %+v, want rig-a's seat kept", pr.Seats)
	}
	if len(pr.RigUnread) != 1 || pr.RigUnread[0] != "rig-b" {
		t.Fatalf("unread = %v, want [rig-b]", pr.RigUnread)
	}
	if pr.RigUnknown {
		t.Fatal("project reported unknown while one rig answered: capacity is known, at one")
	}
	if !strings.Contains(pr.RigError, "rig-b") {
		t.Fatalf("rig error = %q, want it to name rig-b", pr.RigError)
	}
	if len(pr.RigWarnings) != 0 {
		t.Fatalf("warnings = %v, want none: rig-b went unread, it is not a ghost", pr.RigWarnings)
	}
	if !read.RigStale || len(read.RigUnread) != 1 || read.RigUnread[0] != "rig-b" {
		t.Fatalf("aggregate stale=%v unread=%v, want rig-b named", read.RigStale, read.RigUnread)
	}
	// The readable roster is a fact worth keeping: the project writes it and the
	// status block records the gap beside it.
	if payload, _, err := st.GetSnapshot(ctx, id, "rig_seats"); err != nil || len(payload) == 0 {
		t.Fatalf("rig_seats = %s, %v; want the readable rigs written", payload, err)
	}
	status, _, err := st.GetSnapshot(ctx, id, "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(string(status), `"rig_unread":["rig-b"]`) {
		t.Fatalf("status = %s, want rig_unread naming rig-b", status)
	}
}

// TestEveryRigUnreadMakesTheProjectUnknown: an unread fleet is not an empty one,
// so the project is undecided and writes no roster at all.
func TestEveryRigUnreadMakesTheProjectUnknown(t *testing.T) {
	ctx := context.Background()
	st, id := twoRigFixture(t)
	runner := runnerFunc(func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "ps" {
			return nil, errors.New("rig ps: exit 1: no daemon")
		}
		return []byte("[]"), nil
	})
	m := &Mirror{Store: st, Rig: rig.NewAdapter(runner)}
	read, err := m.Tick(ctx, []Project{{ID: id, Key: "app", Forge: forge.NewFake(forge.KindGitHub), Rigs: []string{"rig-a", "rig-b"}}})
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	pr := read.Projects["app"]
	if !pr.RigUnknown {
		t.Fatal("project not marked unknown with every rig unread")
	}
	if len(pr.Seats) != 0 {
		t.Fatalf("seats = %+v, want none", pr.Seats)
	}
	if len(pr.RigUnread) != 2 || pr.RigUnread[0] != "rig-a" || pr.RigUnread[1] != "rig-b" {
		t.Fatalf("unread = %v, want [rig-a rig-b] in declared order", pr.RigUnread)
	}
	if payload, at, err := st.GetSnapshot(ctx, id, "rig_seats"); err != nil || len(payload) != 0 || !at.IsZero() {
		t.Fatalf("rig_seats = %s at %v, %v; want no roster written", payload, at, err)
	}
	if status, err := st.MirrorStatus(ctx, id); err != nil || status.LastRigAt != nil {
		t.Fatalf("rig stamp = %v, %v; want none", status.LastRigAt, err)
	}
}

// TestNoDeclaredRigsKeepsTheRosterStale is the review's first note on #447: a
// failed fleet-wide read with no rig declared anywhere had nothing to decompose,
// and the empty set of per-rig errors then read as a clean read — so a project
// that admits every seat planned against zero seats and said "no free builder".
// A decomposition that asked nobody answers nothing.
func TestNoDeclaredRigsKeepsTheRosterStale(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	id, err := st.CreateProject(ctx, store.ProjectInput{
		Key: "app", Name: "app", ForgeKind: "github", ForgeProject: "o/app",
		ConcurrencyCap: 2, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	runner := runnerFunc(func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "ps" {
			return nil, errors.New("rig ps: exit 1: no daemon")
		}
		return []byte("[]"), nil
	})
	m := &Mirror{Store: st, Rig: rig.NewAdapter(runner)}
	read, err := m.Tick(ctx, []Project{{ID: id, Key: "app", Forge: forge.NewFake(forge.KindGitHub)}})
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if !read.RigStale {
		t.Fatal("rig stale = false, want true: nothing was read, so nothing is known")
	}
	pr := read.Projects["app"]
	if !pr.RigUnknown {
		t.Fatalf("project unknown = false, want true: a project that admits every seat is unknown exactly when the roster read failed")
	}
	if reason := func() string { _, r := Busy(read); return r }(); !strings.Contains(reason, "rig read failed") {
		t.Fatalf("busy reason = %q, want the rig read named", reason)
	}
}

// TestRecoveredRosterKeepsTheBreadcrumb is the review's second note on #447: the
// fleet-wide read can fail and every rig still answer. The roster is whole, so
// RigStale stays false — but the failed read is worth remembering, and the
// cadence must not call a whole roster failed when it was rebuilt.
func TestRecoveredRosterKeepsTheBreadcrumb(t *testing.T) {
	ctx := context.Background()
	st, id := twoRigFixture(t)
	runner := runnerFunc(func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) == 0 || args[0] != "ps" {
			return []byte("[]"), nil
		}
		if args[1] == "--nodes" && len(args) > 2 && args[2] == "-A" {
			return nil, errors.New("rig ps --nodes -A: exit 1: node listing timed out")
		}
		rigName := args[len(args)-1]
		return []byte("[" + seatNode(rigName, "build") + "]"), nil
	})
	m := &Mirror{Store: st, Rig: rig.NewAdapter(runner)}
	read, err := m.Tick(ctx, []Project{{ID: id, Key: "app", Forge: forge.NewFake(forge.KindGitHub), Rigs: []string{"rig-a", "rig-b"}}})
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if read.RigStale {
		t.Fatal("rig stale = true, want false: every rig answered on its own")
	}
	if read.RigError == "" {
		t.Fatal("rig error = empty, want the failed fleet-wide read kept as a breadcrumb")
	}
	if len(read.RigUnread) != 0 {
		t.Fatalf("unread = %v, want none", read.RigUnread)
	}
	pr := read.Projects["app"]
	if len(pr.Seats) != 2 || pr.RigUnknown || len(pr.RigWarnings) != 0 {
		t.Fatalf("project seats=%d unknown=%v warnings=%v, want both rigs' seats and no complaint", len(pr.Seats), pr.RigUnknown, pr.RigWarnings)
	}
	busy, reason := Busy(read)
	if !busy {
		t.Fatal("busy = false, want true: a read broke this tick")
	}
	if !strings.Contains(reason, "recovered per rig") {
		t.Fatalf("busy reason = %q, want the recovery named rather than a whole roster called failed", reason)
	}
	if payload, _, err := st.GetSnapshot(ctx, id, "rig_seats"); err != nil || len(payload) == 0 {
		t.Fatalf("rig_seats = %s, %v; want the recovered roster written", payload, err)
	}
}

// TestSnapshotNamesSeatsWaitingOnAHuman (#465): BuildSnapshot is where the
// capacity line's fact comes from. A builder seat that is up but stopped on a
// person has non-empty Attention, is not Running (so it is not a builder), and
// must still be named — that is the difference between "seats are busy" and
// "one seat waits on you".
func TestSnapshotNamesSeatsWaitingOnAHuman(t *testing.T) {
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	project := store.ProjectRow{
		Key: "app", Name: "app", ForgeKind: "github", Enabled: true, ConcurrencyCap: 1,
		RigTiers: []store.RigTier{{Name: "rig-a"}},
	}
	waiting := rig.Seat{
		Session: "dev-builder-claude@rig-a", LogicalID: "dev.builder-claude", Rig: "rig-a",
		Lifecycle: "attention_required",
		Attention: []rig.SeatAttention{{
			Seat: "dev-builder-claude@rig-a", Rig: "rig-a", Kind: rig.AttentionKindAttention, Question: "Approve the merge?",
		}},
	}
	result := ProjectResult{
		Items: []forge.Item{{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Five"}},
		Seats: []rig.Seat{waiting},
	}

	snapshot := BuildSnapshot(project, result, nil, nil, nil, nil)
	if len(snapshot.WaitingOnHuman) != 1 || snapshot.WaitingOnHuman[0] != waiting.Session {
		t.Fatalf("waiting = %v, want the attention seat", snapshot.WaitingOnHuman)
	}
	plan := decide.PlanTick(snapshot, now)
	skip := planSkipFor(plan, 5)
	if skip.Reason != decide.ReasonNoFreeBuilder || !strings.Contains(skip.Detail, "(1 waiting on a human)") {
		t.Fatalf("skip = %+v, want a no_free_builder naming the human", skip)
	}

	// The same seat on a rig this tick could not read proves nothing: no name,
	// no invented count, and the detail says the rig went unread instead.
	unread := result
	unread.RigUnread = []string{"rig-a"}
	plain := decide.PlanTick(BuildSnapshot(project, unread, nil, nil, nil, nil), now)
	skip = planSkipFor(plain, 5)
	if strings.Contains(skip.Detail, "waiting on a human") {
		t.Fatalf("skip = %+v, want no human count from an unread rig", skip)
	}
}

func planSkipFor(plan decide.Plan, item int) decide.Skip {
	for _, skip := range plan.Skips {
		if skip.Number == item {
			return skip
		}
	}
	return decide.Skip{}
}
