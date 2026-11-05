package mirror

import (
	"fmt"
	"sort"

	"github.com/dinhtrung/fleet-console/internal/forge"
)

// TerminalRowStates are the queue-row states that mean "this row is finished".
// One definition, because the cadence's busy verdict and the reconcile and
// adoption passes must agree about when a row has stopped being work.
var TerminalRowStates = map[string]struct{}{
	"terminal": {}, "closed": {}, "done": {}, "cancelled": {}, "canceled": {},
	"denied": {}, "failed": {}, "handed-off": {}, "handed_off": {}, "handedoff": {},
}

// RowTerminal reports whether a queue row has stopped being work.
func RowTerminal(state string) bool {
	_, terminal := TerminalRowStates[state]
	return terminal
}

// Busy answers design D7's verdict for one read and names the fact that made it
// busy. Quiet means the whole fleet looked empty and every read succeeded, which
// is the only condition that earns a longer wait.
//
// Deliberately broader than "has an intent" (D7): an item blocked on another
// item, an item on hold, and an open change request awaiting a verdict all mean
// "keep watching at the normal rate" even though none of them dispatches.
//
// A failed or stale read counts as busy. A blind read is not idleness: backing
// off there would lengthen the interval exactly when the app cannot see, which
// is the opposite of what an operator wants. That covers the rig read as well as
// the forge read, since both make the tick's view untrustworthy.
//
// Projects are walked in key order so the named reason is deterministic; a map
// walk would make the log line change run to run for no reason.
func Busy(read TickResult) (bool, string) {
	keys := make([]string, 0, len(read.Projects))
	for key := range read.Projects {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		project := read.Projects[key]
		if reason := projectBusyReason(key, project); reason != "" {
			return true, reason
		}
	}
	if read.RigStale || read.RigError != "" {
		reason := "rig read stale"
		if read.RigError != "" {
			reason = "rig read failed: " + read.RigError
		}
		return true, reason
	}
	return false, ""
}

// projectBusyReason returns why one project is busy, or "" when it is quiet. The
// read failures come first: when the forge read is stale or failed, the item and
// change-request lists are the last known ones and cannot be trusted to be empty.
func projectBusyReason(key string, project ProjectResult) string {
	switch {
	case project.ForgeForbidden:
		return fmt.Sprintf("%s: forge read forbidden", key)
	case project.ForgeError != "":
		return fmt.Sprintf("%s: forge read failed: %s", key, project.ForgeError)
	case project.ForgeStale:
		return fmt.Sprintf("%s: forge read stale", key)
	case project.BranchError != "":
		return fmt.Sprintf("%s: branch read failed: %s", key, project.BranchError)
	}
	for _, item := range project.Items {
		if item.State == forge.StateOpen {
			return fmt.Sprintf("%s: #%d open", key, item.Number)
		}
	}
	for _, cr := range project.CRs {
		if cr.State == forge.StateOpen {
			return fmt.Sprintf("%s: change request #%d open", key, cr.Number)
		}
	}
	for _, row := range project.Rows {
		if !RowTerminal(row.State) {
			return fmt.Sprintf("%s: queue row %s %s", key, row.ID, row.State)
		}
	}
	return ""
}
