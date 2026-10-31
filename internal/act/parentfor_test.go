package act

import (
	"context"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// TestParentForResolvesFromTheStore pins the snapshot guard's precondition: the
// tick can only reach `children == 0` when the merged item is absent from the
// snapshot, and the parent then has to come from the mirror cache.
//
// It is a unit test on `parentFor` rather than a tick test because no tick-level
// fixture can exercise this. The guard fires on `children == 0`, and children are
// counted by walking `in.Items` for items whose parent is the epic; a parent read
// from the snapshot implies the merged item is in the snapshot, and it is itself
// a child, so `children >= 1` always. The fallback is the guard's precondition,
// not its companion — so it is asserted here, on its own.
func TestParentForResolvesFromTheStore(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	project := store.ProjectRow{ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Enabled: true}

	// The mirror writes every item each tick, so the cache holds the parent even
	// when a later forge read returns a truncated snapshot.
	parent := 64
	if err := st.ReplaceItems(ctx, pid, []store.ItemRecord{
		{Number: 62, Kind: "task", State: forge.StateClosed, Title: "child", Parent: &parent, URL: "u", UpdatedAt: time.Now().UTC()},
	}); err != nil {
		t.Fatalf("items: %v", err)
	}

	actions := &Actions{Store: st}
	got := actions.parentFor(ctx, TickInput{Items: nil, Project: project}, 62)
	if got == nil {
		t.Fatal("parentFor returned nil with the item cached in the store")
	}
	if *got != parent {
		t.Fatalf("parentFor = %d, want %d", *got, parent)
	}

	// The snapshot wins when it carries the item: the store is the fallback, not
	// the first source.
	fromSnapshot := actions.parentFor(ctx, TickInput{
		Items:   []forge.Item{{Number: 62, Parent: intPtr(70)}},
		Project: project,
	}, 62)
	if fromSnapshot == nil || *fromSnapshot != 70 {
		t.Fatalf("parentFor = %v, want the snapshot's 70", fromSnapshot)
	}
}

// TestParentForIsNilWhenNothingKnows is the half that makes the first test mean
// something: a test that can only observe "found" cannot tell a working lookup
// from one that returns whatever it is given.
func TestParentForIsNilWhenNothingKnows(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	project := store.ProjectRow{ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Enabled: true}

	actions := &Actions{Store: st}
	if got := actions.parentFor(ctx, TickInput{Items: nil, Project: project}, 62); got != nil {
		t.Fatalf("parentFor = %v, want nil for an item nothing has seen", got)
	}

	// And a nil store is nil, not a panic: the tick may run without one in a test.
	if got := (&Actions{}).parentFor(ctx, TickInput{Project: project}, 62); got != nil {
		t.Fatalf("parentFor with no store = %v, want nil", got)
	}
}
