package web

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/mirror"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// rosterRunnerFunc is one scripted `rig` CLI for a mirror tick.
type rosterRunnerFunc func(ctx context.Context, args ...string) ([]byte, error)

func (f rosterRunnerFunc) Run(ctx context.Context, args ...string) ([]byte, error) {
	return f(ctx, args...)
}

// The boundary these tests pin is mirror -> status snapshot -> waiting panel.
// The panel's own tests seed `rig_stale` directly, so they prove the render but
// not that a failed roster read ever reaches it; the mirror's tests prove the
// flag but not that the surface reads it. #449 fixed the mirror half for a
// project that declares no rigs, and a probe showed the panel rendering
// `known=true, seats=0` — "nobody is waiting", over a failed fleet read — before
// that fix. These two tests hold the seam itself:
//
//  1. failed fleet-wide roster read, no declared rigs -> unknown, never clear;
//  2. fully recovered decomposition -> known, because the panel reads RigStale
//     and not RigError, which is deliberately kept as a breadcrumb.
//
// The second is the one that regresses silently: a later "helpful" change that
// also treated RigError as a staleness claim would turn a fleet every rig
// answered back into unknown, and nothing else in the tree would fail.

// TestFailedRosterReachesThePanelAsUnknown is the whole chain: a mirror tick whose
// fleet-wide `rig ps` fails while `queue list` answers, for a project that
// declares no rigs. Nothing can be read, so nothing is known — and the panel must
// say that rather than listing an empty fleet as clear.
func TestFailedRosterReachesThePanelAsUnknown(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fleet.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	pid, err := st.CreateProject(ctx, store.ProjectInput{
		Key: "app", Name: "app", ForgeKind: "github", ForgeProject: "o/r",
		ConcurrencyCap: 2, Enabled: true,
	})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	// The queue answers; the roster read fails. No rig is declared, so the
	// per-rig decomposition has no list to ask.
	runner := rosterRunnerFunc(func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "ps" {
			return nil, errors.New("rig ps: exit 1: no daemon")
		}
		return []byte("[]"), nil
	})
	fake := forge.NewFake(forge.KindGitHub)
	fake.ItemsValue = []forge.Item{{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Five"}}
	now := time.Date(2026, 11, 14, 2, 0, 0, 0, time.UTC)
	m := &mirror.Mirror{Store: st, Rig: rig.NewAdapter(runner), Now: func() time.Time { return now }}
	read, err := m.Tick(ctx, []mirror.Project{{ID: pid, Key: "app", Forge: fake}})
	if err != nil {
		t.Fatalf("tick: %v", err)
	}

	// The mirror half: the roster is not whole, and no roster is written for a
	// project whose capacity is unknown — an empty snapshot would be read as a
	// fact about the fleet.
	if !read.RigStale {
		t.Fatalf("rig stale = false, want true: the roster read failed")
	}
	if !read.Projects["app"].RigUnknown {
		t.Fatalf("rig unknown = false, want true: a project that admits every seat is unknown exactly when the read failed")
	}
	if payload, _, err := st.GetSnapshot(ctx, pid, "rig_seats"); err != nil {
		t.Fatalf("snapshot: %v", err)
	} else if len(payload) != 0 {
		t.Fatalf("rig_seats = %s, want no snapshot for an unknown roster", payload)
	}

	// The panel half: unknown, carrying the read's own words, and never a list.
	panel := waitingPanel(ctx, st, pid, "", now)
	if panel.Known {
		t.Fatalf("panel = %+v, want unknown: an unread rig must not look healthy", panel)
	}
	if panel.Reason == "" {
		t.Fatalf("panel = %+v, want the read's reason stated", panel)
	}
	if len(panel.Seats) != 0 {
		t.Fatalf("seats = %+v, want none presented as fact", panel.Seats)
	}
	if panel.UIURL != "" {
		t.Fatalf("ui url = %q, want no link when nothing is known", panel.UIURL)
	}
}

// TestRecoveredRosterKeepsThePanelKnown is the negative half, and the reason the
// panel branches on RigStale alone. The fleet-wide read fails, every declared rig
// then answers on its own: the roster is known, so the panel is known and its
// empty seat list is a real "nobody is waiting" — even though RigError still
// carries the first failure as a breadcrumb.
func TestRecoveredRosterKeepsThePanelKnown(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fleet.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	pid, err := st.CreateProject(ctx, store.ProjectInput{
		Key: "app", Name: "app", ForgeKind: "github", ForgeProject: "o/r",
		Rigs: []string{"rig-a", "rig-b"}, ConcurrencyCap: 2, Enabled: true,
	})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	// `ps --nodes -A` fails; the per-rig reads answer with one running seat each.
	runner := rosterRunnerFunc(func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) == 0 || args[0] != "ps" {
			return []byte("[]"), nil
		}
		if len(args) > 2 && args[1] == "--nodes" && args[2] == "-A" {
			return nil, errors.New("rig ps --nodes -A: exit 1: node listing timed out")
		}
		rigName := args[len(args)-1]
		return []byte(`[{"nodeId":"n-` + rigName + `","rigName":"` + rigName +
			`","canonicalSessionName":"dev-build@` + rigName + `","logicalId":"dev.build",` +
			`"lifecycleState":"running","sessionStatus":"running"}]`), nil
	})
	fake := forge.NewFake(forge.KindGitHub)
	fake.ItemsValue = []forge.Item{{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Five"}}
	now := time.Date(2026, 11, 14, 2, 0, 0, 0, time.UTC)
	m := &mirror.Mirror{Store: st, Rig: rig.NewAdapter(runner), Now: func() time.Time { return now }}
	read, err := m.Tick(ctx, []mirror.Project{{
		ID: pid, Key: "app", Forge: fake, Rigs: []string{"rig-a", "rig-b"},
	}})
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if read.RigStale {
		t.Fatalf("rig stale = true, want false: every declared rig answered")
	}
	if read.RigError == "" {
		t.Fatalf("rig error = empty, want the failed fleet-wide read kept as a breadcrumb")
	}

	panel := waitingPanel(ctx, st, pid, "", now)
	if !panel.Known {
		t.Fatalf("panel = %+v, want known: a recovered roster is known, and RigError is a breadcrumb, not a staleness claim", panel)
	}
	if len(panel.Seats) != 0 {
		t.Fatalf("seats = %+v, want none: every seat answered and none is waiting", panel.Seats)
	}
	if panel.Reason != "" {
		t.Fatalf("reason = %q, want none for a known roster", panel.Reason)
	}
}
