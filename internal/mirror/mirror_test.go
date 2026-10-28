package mirror

import (
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/forge"
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
