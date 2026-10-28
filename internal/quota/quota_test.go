package quota

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

func stores(t *testing.T) (*store.Store, int64) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "fleet.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	id, err := st.CreateProject(context.Background(), store.ProjectInput{
		Key: "app", Name: "app", ForgeKind: "github", ForgeProject: "o/r", Rig: "rig", Enabled: true,
	})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	return st, id
}

func signal(seat string, at time.Time, class string) rig.ProviderSignal {
	return rig.ProviderSignal{SeatSession: seat, SourceClass: class, AsOf: at.UTC().Format(time.RFC3339)}
}

func TestTwoStalledSeatsRaiseOneDecisionAndPause(t *testing.T) {
	ctx := context.Background()
	st, projectID := stores(t)
	now := time.Now().UTC()
	signals := []rig.ProviderSignal{
		signal("build-1@rig", now.Add(-time.Minute), "unknown"),
		signal("build-2@rig", now.Add(-2*time.Minute), "unknown"),
	}
	detector := &Detector{Store: st, Now: func() time.Time { return now }}
	finding, raised, err := detector.Check(ctx, projectID, "rig", signals)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !raised || finding == nil || len(finding.Seats) != 2 {
		t.Fatalf("finding = %+v raised=%v", finding, raised)
	}
	projects, _ := st.Projects(ctx)
	if !projects[0].OnHold {
		t.Fatal("project was not paused")
	}
	events, _ := st.RecentEvents(ctx, 10)
	kinds := map[string]int{}
	for _, event := range events {
		kinds[event.Kind]++
	}
	if kinds["quota.decision"] != 1 || kinds["quota.pause"] != 1 {
		t.Fatalf("events = %+v", kinds)
	}

	if _, raised, err := detector.Check(ctx, projectID, "rig", signals); err != nil || raised {
		t.Fatalf("second check raised=%v err=%v, want no new decision", raised, err)
	}
	events, _ = st.RecentEvents(ctx, 10)
	count := 0
	for _, event := range events {
		if event.Kind == "quota.decision" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("quota decisions = %d, want 1", count)
	}
}

func TestOneStallOrOldSignalDoesNotRaise(t *testing.T) {
	ctx := context.Background()
	st, projectID := stores(t)
	now := time.Now().UTC()
	detector := &Detector{Store: st, Now: func() time.Time { return now }}
	cases := [][]rig.ProviderSignal{
		{signal("build-1@rig", now.Add(-time.Minute), "unknown")},
		{
			signal("build-1@rig", now.Add(-time.Minute), "unknown"),
			signal("build-2@rig", now.Add(-10*time.Minute), "unknown"),
		},
		{
			signal("build-1@rig", now.Add(-time.Minute), "provider_statusline"),
			signal("build-2@rig", now.Add(-time.Minute), "unknown"),
		},
	}
	for index, signals := range cases {
		if _, raised, err := detector.Check(ctx, projectID, "rig", signals); err != nil || raised {
			t.Fatalf("case %d raised=%v err=%v, want no decision", index, raised, err)
		}
	}
}
