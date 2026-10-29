package act

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// ReconcileResult is one tick's dispatch reconciliation.
type ReconcileResult struct {
	Changed []int // item numbers whose dispatch ended this tick
	Reasons map[int]string
	Events  []int64
}

var reconcileTerminal = map[string]struct{}{
	"terminal": {}, "closed": {}, "done": {}, "cancelled": {}, "canceled": {},
	"denied": {}, "failed": {}, "handed-off": {}, "handed_off": {}, "handedoff": {},
}

type reconcileMarker struct {
	Missing map[string]string `json:"missing"` // qitem id -> first tick it was absent
}

// Reconcile applies D4: the rig wins. An open dispatch is ended when its item's
// CR merged (merged), when its queue row is terminal (superseded), or when the
// row has been absent for a full tick (abandoned). One event per transition.
func Reconcile(ctx context.Context, st *store.Store, project store.ProjectRow, rows []rig.QueueRow, crs []forge.ChangeRequest, now time.Time) (ReconcileResult, error) {
	result := ReconcileResult{Reasons: map[int]string{}}
	if st == nil {
		return result, fmt.Errorf("reconcile: store is required")
	}
	dispatches, err := st.DispatchesForProject(ctx, project.ID)
	if err != nil {
		return result, err
	}
	byQID := make(map[string]rig.QueueRow, len(rows))
	for _, row := range rows {
		byQID[row.ID] = row
	}
	merged := make(map[int]bool)
	for _, cr := range crs {
		if cr.Merged {
			merged[cr.ClosesItem] = true
		}
	}

	marker := reconcileMarker{Missing: map[string]string{}}
	if raw, _, err := st.GetSnapshot(ctx, project.ID, "reconcile"); err == nil && len(raw) > 0 {
		_ = json.Unmarshal(raw, &marker)
		if marker.Missing == nil {
			marker.Missing = map[string]string{}
		}
	}

	changed := func(item int, dispatch store.DispatchRow, reason, detail string) error {
		if err := st.EndDispatch(ctx, dispatch.ID, reason); err != nil {
			return err
		}
		eventID, err := st.AppendEvent(ctx, store.Event{
			Actor: "act", Kind: "dispatch." + reason, Subject: fmt.Sprintf("#%d", item), Detail: detail,
		})
		if err != nil {
			return err
		}
		result.Changed = append(result.Changed, item)
		result.Reasons[item] = reason
		result.Events = append(result.Events, eventID)
		return nil
	}

	for _, dispatch := range dispatches {
		if dispatch.EndedAt != nil || dispatch.State == "pending" {
			// Pending two-phase dispatches are the adoption pass's job.
			continue
		}
		item := dispatch.ItemNumber
		if merged[item] {
			if err := changed(item, dispatch, "merged",
				fmt.Sprintf("change request merged; attempt %d row %s", dispatch.Attempt, dispatch.QItemID)); err != nil {
				return result, err
			}
			delete(marker.Missing, dispatch.QItemID)
			continue
		}
		row, present := byQID[dispatch.QItemID]
		if present {
			delete(marker.Missing, dispatch.QItemID)
			state := strings.ToLower(row.PickupState)
			if state == "" {
				state = strings.ToLower(row.State)
			}
			if _, terminal := reconcileTerminal[state]; terminal {
				if err := changed(item, dispatch, "superseded",
					fmt.Sprintf("row %s is %s; attempt %d", dispatch.QItemID, state, dispatch.Attempt)); err != nil {
					return result, err
				}
			}
			continue
		}
		// The row is absent. Mark it on the first tick; abandon on the next full
		// tick. A dispatch created by this tick's act pass is not in this read.
		if first, seen := marker.Missing[dispatch.QItemID]; seen {
			if err := changed(item, dispatch, "abandoned",
				fmt.Sprintf("no row %s for item #%d since %s; attempt %d", dispatch.QItemID, item, first, dispatch.Attempt)); err != nil {
				return result, err
			}
			delete(marker.Missing, dispatch.QItemID)
			continue
		}
		marker.Missing[dispatch.QItemID] = now.UTC().Format(time.RFC3339)
	}

	if err := st.PutSnapshot(ctx, project.ID, "reconcile", marker, now); err != nil {
		return result, err
	}
	return result, nil
}
