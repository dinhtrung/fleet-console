package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/config"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// Both flags are pause: the board's onhold switch and a config block that was
// removed (enabled = 0). Only a row that is enabled and not on hold is live.
func TestPausedProject(t *testing.T) {
	cases := []struct {
		name    string
		onhold  bool
		enabled bool
		want    bool
	}{
		{name: "enabled", enabled: true, want: false},
		{name: "onhold", onhold: true, enabled: true, want: true},
		{name: "disabled", enabled: false, want: true},
		{name: "onhold and disabled", onhold: true, enabled: false, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pausedProject(store.ProjectRow{OnHold: tc.onhold, Enabled: tc.enabled})
			if got != tc.want {
				t.Fatalf("pausedProject(onhold=%v, enabled=%v) = %v, want %v",
					tc.onhold, tc.enabled, got, tc.want)
			}
		})
	}
}

// noCallForgeRunner fails the test if a spec build ever asks the forge anything:
// projectSources constructs adapters, it does not read them.
type noCallForgeRunner struct{ t *testing.T }

func (r noCallForgeRunner) Run(context.Context, ...string) (string, string, error) {
	r.t.Helper()
	r.t.Fatal("projectSources must not call the forge runner to build a spec")
	return "", "", errors.New("unreachable")
}

// The mirror spec carries the pause flag for every project, so the read pass
// can skip a paused project's forge and git while keeping its rig facts.
func TestProjectSourcesMarksAPausedProject(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "fleet.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	create := func(key string, enabled, onhold bool) {
		t.Helper()
		if _, err := db.CreateProject(ctx, store.ProjectInput{
			Key: key, Name: key, ForgeKind: "github", ForgeProject: "org/" + key,
			Rig: "rig-a", ConcurrencyCap: 1, Enabled: enabled, OnHold: onhold,
		}); err != nil {
			t.Fatalf("create %s: %v", key, err)
		}
	}
	create("live", true, false)
	create("held", true, true)
	create("off", false, false)

	specs, err := projectSources(ctx, &config.Effective{}, db, noCallForgeRunner{t: t})
	if err != nil {
		t.Fatalf("projectSources: %v", err)
	}
	if len(specs) != 3 {
		t.Fatalf("got %d specs, want 3", len(specs))
	}
	want := map[string]bool{"live": false, "held": true, "off": true}
	for _, spec := range specs {
		paused, ok := want[spec.Key]
		if !ok {
			t.Fatalf("unexpected spec %q", spec.Key)
		}
		if spec.Paused != paused {
			t.Fatalf("%s: Paused = %v, want %v", spec.Key, spec.Paused, paused)
		}
	}
}
