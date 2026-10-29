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
			5*time.Millisecond, time.Millisecond,
			func(TickResult) {
				if atomic.AddInt32(&ticks, 1) >= 2 {
					cancel()
				}
			})
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
	snapshot := BuildSnapshot(project, ProjectResult{}, dispatches, nil)
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
