package store

import (
	"context"
	"path/filepath"
	"testing"
)

const dispatcherFixture = `{
  "dispatched": {
    "10": {
      "at": 1793214486.933032,
      "attempt": 1,
      "base": "79acffb1643a19fdc6d6d8bdc3f4c10ca2bc13a8",
      "branch": "feature/10-p4-alerts-quota-second-project",
      "brief": "/home/dinhtrung/tasks/task-10.md",
      "qitem": "qitem-20261028190805-ec177bebfc5be46e",
      "seat": "dev-build-1@fleet-console",
      "worktree": "/home/dinhtrung/develop/wt-fc-10"
    }
  },
  "watchdog": {}
}`

func TestImportDispatchesFromOldState(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "fleet.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer st.Close()
	projectID, err := st.CreateProject(ctx, ProjectInput{
		Key: "fleet-console", Name: "fleet-console", ForgeKind: "github",
		ForgeProject: "dinhtrung/fleet-console", Rig: "fleet-console", Enabled: true,
	})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	state, err := ParseDispatcherState([]byte(dispatcherFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	inserted, err := st.ImportDispatches(ctx, projectID, state)
	if err != nil || inserted != 1 {
		t.Fatalf("import = %d, %v", inserted, err)
	}
	rows, err := st.Dispatches(ctx, projectID, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %+v, %v", rows, err)
	}
	row := rows[0]
	if row.QItemID != "qitem-20261028190805-ec177bebfc5be46e" || row.Seat != "dev-build-1@fleet-console" ||
		row.Worktree != "/home/dinhtrung/develop/wt-fc-10" || row.Branch != "feature/10-p4-alerts-quota-second-project" {
		t.Fatalf("row = %+v", row)
	}
	if row.EndedAt != nil {
		t.Fatalf("imported dispatch should stay open: %+v", row)
	}
	if again, err := st.ImportDispatches(ctx, projectID, state); err != nil || again != 0 {
		t.Fatalf("second import = %d, %v, want 0", again, err)
	}
}
