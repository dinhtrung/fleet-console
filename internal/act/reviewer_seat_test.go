package act

import (
	"context"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// TestRaiseDecisionUsesTheConfiguredReviewer (#384): the review row goes to the
// project's reviewer seat, and with none configured the destination is exactly
// what it was before the field existed.
func TestRaiseDecisionUsesTheConfiguredReviewer(t *testing.T) {
	ctx := context.Background()
	cases := []struct{ name, reviewer, want string }{
		{"default", "", "dev-review@rig-a"},
		{"configured", "dev-reviewer", "dev-reviewer@rig-a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newStore(t)
			queue := &queueStub{created: &rig.CreateRequest{}}
			actions := &Actions{
				Store: st, Rig: queue, ProjectID: 1, ProjectKey: "app",
				WorktreeRoot: t.TempDir(), Reviewer: tc.reviewer,
			}
			project := store.ProjectRow{ID: 1, Key: "app", ForgeKind: "github", ForgeProject: "o/r", Rigs: []string{"rig-a"}}
			if _, err := actions.RaiseDecision(ctx, project, 5, "re-review it", "decision.stale", 20, "aaaa111", actions.WorktreeRoot); err != nil {
				t.Fatalf("raise: %v", err)
			}
			if queue.created.Destination != tc.want {
				t.Fatalf("destination = %q, want %q", queue.created.Destination, tc.want)
			}
		})
	}
}
