// Package mirror reads the forge, the rig and origin, and writes only the app's
// mirror/cache tables. A forge read that fails marks the project stale; it never
// replaces the cached rows with an empty set.
package mirror

import (
	"context"
	"encoding/json"
	"math/rand"
	"strings"
	"time"

	"github.com/dinhtrung/fleet-console/internal/decide"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/git"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// Project is one project's read sources.
type Project struct {
	ID       int64
	Key      string
	Forge    forge.Forge
	Rig      string
	RepoPath string
	Git      git.Runner
}

// ProjectResult is one project's read outcome for this tick.
type ProjectResult struct {
	Items       []forge.Item
	CRs         []forge.ChangeRequest
	Branches    []git.Branch
	Rows        []rig.QueueRow
	Seats       []rig.Seat
	ForgeStale  bool
	ForgeError  string
	BranchError string
}

// TickResult is everything one tick read.
type TickResult struct {
	At       time.Time
	Projects map[string]ProjectResult
	RigStale bool
	RigError string
}

// Mirror runs the read pass.
type Mirror struct {
	Store *store.Store
	Rig   *rig.Adapter
	Now   func() time.Time
}

func (m *Mirror) now() time.Time {
	if m.Now != nil {
		return m.Now().UTC()
	}
	return time.Now().UTC()
}

type forgeSnapshot struct {
	Items []forge.Item          `json:"items"`
	CRs   []forge.ChangeRequest `json:"change_requests"`
}

type statusBlock struct {
	ForgeStale  bool   `json:"forge_stale"`
	RigStale    bool   `json:"rig_stale"`
	ForgeError  string `json:"forge_error,omitempty"`
	RigError    string `json:"rig_error,omitempty"`
	BranchError string `json:"branch_error,omitempty"`
}

// Tick reads every project once. Forge failures keep the previous item rows and
// stamp; the status snapshot records stale plus the error.
func (m *Mirror) Tick(ctx context.Context, projects []Project) (TickResult, error) {
	at := m.now()
	result := TickResult{At: at, Projects: make(map[string]ProjectResult, len(projects))}

	var rows []rig.QueueRow
	var seats []rig.Seat
	if m.Rig != nil {
		var err error
		rows, err = m.Rig.QueueList(ctx)
		if err != nil {
			result.RigStale, result.RigError = true, err.Error()
		} else {
			rows = m.fillBodies(ctx, rows)
		}
		seats, err = m.Rig.Seats(ctx)
		if err != nil {
			result.RigStale = true
			if result.RigError == "" {
				result.RigError = err.Error()
			}
		}
	}

	for _, project := range projects {
		pr := ProjectResult{
			Rows:  filterRows(rows, project.Rig),
			Seats: filterSeats(seats, project.Rig),
		}

		if project.Forge != nil {
			items, itemsErr := project.Forge.Items(ctx)
			crs, crsErr := project.Forge.ChangeRequests(ctx)
			if itemsErr != nil || crsErr != nil {
				pr.ForgeStale = true
				if itemsErr != nil {
					pr.ForgeError = itemsErr.Error()
				} else {
					pr.ForgeError = crsErr.Error()
				}
			} else {
				pr.Items, pr.CRs = items, crs
				records := make([]store.ItemRecord, 0, len(items))
				for _, item := range items {
					records = append(records, itemRecord(item, at))
				}
				if err := m.Store.ReplaceItems(ctx, project.ID, records); err != nil {
					return result, err
				}
				if err := m.Store.PutSnapshot(ctx, project.ID, "forge", forgeSnapshot{Items: items, CRs: crs}, at); err != nil {
					return result, err
				}
				if err := m.Store.SetForgeStamp(ctx, project.ID, at); err != nil {
					return result, err
				}
			}
		}

		if project.Git != nil {
			branches, err := git.Index(ctx, project.Git)
			if err != nil {
				pr.BranchError = err.Error()
			} else {
				pr.Branches = branches
				if err := m.Store.PutSnapshot(ctx, project.ID, "branches", branches, at); err != nil {
					return result, err
				}
			}
		}

		status := statusBlock{
			ForgeStale: pr.ForgeStale, ForgeError: pr.ForgeError,
			RigStale: result.RigStale, RigError: result.RigError,
			BranchError: pr.BranchError,
		}
		if err := m.Store.PutSnapshot(ctx, project.ID, "status", status, at); err != nil {
			return result, err
		}
		if !result.RigStale {
			if err := m.Store.PutSnapshot(ctx, project.ID, "rig_queue", pr.Rows, at); err != nil {
				return result, err
			}
			if err := m.Store.PutSnapshot(ctx, project.ID, "rig_seats", pr.Seats, at); err != nil {
				return result, err
			}
			if err := m.Store.SetRigStamp(ctx, project.ID, at); err != nil {
				return result, err
			}
		}
		result.Projects[project.Key] = pr
	}
	return result, nil
}

func (m *Mirror) fillBodies(ctx context.Context, rows []rig.QueueRow) []rig.QueueRow {
	for index := range rows {
		switch strings.ToLower(rows[index].State) {
		case "pending", "in-progress", "blocked":
			item, err := m.Rig.QueueShow(ctx, rows[index].ID)
			if err != nil {
				continue
			}
			rows[index].Body = item.Body
			if rows[index].CreatedAt == "" {
				rows[index].CreatedAt = item.CreatedAt
			}
		}
	}
	return rows
}

func filterRows(rows []rig.QueueRow, rigName string) []rig.QueueRow {
	if rigName == "" {
		return rows
	}
	suffix := "@" + rigName
	out := make([]rig.QueueRow, 0, len(rows))
	for _, row := range rows {
		if strings.HasSuffix(row.Destination, suffix) {
			out = append(out, row)
		}
	}
	return out
}

func filterSeats(seats []rig.Seat, rigName string) []rig.Seat {
	if rigName == "" {
		return seats
	}
	out := make([]rig.Seat, 0, len(seats))
	for _, seat := range seats {
		if seat.Rig == rigName {
			out = append(out, seat)
		}
	}
	return out
}

func itemRecord(item forge.Item, at time.Time) store.ItemRecord {
	blocked := make([]int, 0, len(item.BlockedBy))
	for _, link := range item.BlockedBy {
		blocked = append(blocked, link.Number)
	}
	raw, err := json.Marshal(blocked)
	if err != nil {
		raw = []byte("[]")
	}
	return store.ItemRecord{
		Number: item.Number, Kind: string(item.Kind), State: item.State, Title: item.Title,
		Labels: strings.Join(item.Labels, ","), Parent: item.Parent,
		BlockedByJSON: string(raw), URL: item.URL, UpdatedAt: at,
	}
}

// BuildSnapshot turns one project's reads into the decide input.
func BuildSnapshot(project store.ProjectRow, result ProjectResult, dispatches []store.DispatchRow, verdicts []store.VerdictForItem) decideSnapshot {
	snapshot := decideSnapshot{
		Project: projectFields{
			Key: project.Key, Forge: decide.Forge(project.ForgeKind), Enabled: project.Enabled,
			OnHold: project.OnHold, ConcurrencyCap: project.ConcurrencyCap,
		},
		ForgeOK: !result.ForgeStale,
	}
	if result.ForgeStale {
		snapshot.ForgeError = result.ForgeError
	}
	for _, item := range result.Items {
		converted := itemFields{
			Number: item.Number, Kind: decide.ItemKind(item.Kind), State: item.State, Title: item.Title,
			Labels: item.Labels, Parent: item.Parent, URL: item.URL,
		}
		for _, link := range item.BlockedBy {
			converted.BlockedBy = append(converted.BlockedBy, linkFields{Number: link.Number, State: link.State})
		}
		for _, link := range item.Related {
			converted.Related = append(converted.Related, linkFields{Number: link.Number, State: link.State})
		}
		snapshot.Items = append(snapshot.Items, converted)
	}
	for _, row := range result.Rows {
		snapshot.Rows = append(snapshot.Rows, rowFields{
			QItemID: row.ID, State: row.State, Pickup: row.PickupState,
			Destination: row.Destination, Body: row.Body, Summary: row.Summary,
			CreatedAt: parseTime(row.CreatedAt), LastNudgeAt: parseTime(row.LastNudgeAt),
		})
	}
	for _, branch := range result.Branches {
		converted := branchFields{Name: branch.Name, Item: branch.Item}
		for _, cr := range result.CRs {
			if cr.ClosesItem != branch.Item {
				continue
			}
			if cr.Merged {
				converted.HasMergedCR = true
			} else if strings.EqualFold(cr.State, "open") {
				converted.HasOpenCR = true
			}
		}
		snapshot.Branches = append(snapshot.Branches, converted)
	}
	for _, seat := range result.Seats {
		if seat.Lifecycle != "running" || !strings.Contains(seat.LogicalID, "build") {
			continue
		}
		snapshot.Builders = append(snapshot.Builders, seat.Session)
	}
	for _, dispatch := range dispatches {
		snapshot.Dispatched = append(snapshot.Dispatched, dispatch.ItemNumber)
		converted := dispatchFields{
			Item: dispatch.ItemNumber, Seat: dispatch.Seat, Branch: dispatch.Branch,
			QItemID: dispatch.QItemID, Attempt: dispatch.Attempt,
			CreatedAt: parseTime(dispatch.CreatedAt), EndReason: dispatch.EndReason,
		}
		if dispatch.EndedAt != nil {
			converted.EndedAt = parseTime(*dispatch.EndedAt)
		}
		snapshot.Dispatches = append(snapshot.Dispatches, converted)
	}
	for _, cr := range result.CRs {
		snapshot.CRs = append(snapshot.CRs, crFields{
			Number: cr.Number, Item: cr.ClosesItem, State: cr.State,
			Merged: cr.Merged, HeadSHA: cr.HeadSHA, DocsOnly: cr.DocsOnly,
		})
	}
	for _, verdict := range verdicts {
		snapshot.Verdicts = append(snapshot.Verdicts, verdictFields{Item: verdict.Item, SHA: verdict.SHA, Decision: verdict.Decision})
	}
	return snapshot
}

func parseTime(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05Z"} {
		if at, err := time.Parse(layout, raw); err == nil {
			return at
		}
	}
	return time.Time{}
}

// The aliases below keep mirror independent of the decide package's exported names
// while still producing its exact types.
type decideSnapshot = decide.Snapshot
type projectFields = decide.Project
type itemFields = decide.Item
type linkFields = decide.Link
type rowFields = decide.Row
type branchFields = decide.Branch
type dispatchFields = decide.DispatchState
type crFields = decide.CRState
type verdictFields = decide.VerdictState

// Loop ticks immediately and then on a jittered interval until ctx is done. A
// source error or a failed tick is retried on the next interval; it never stops the
// loop.
func (m *Mirror) Loop(ctx context.Context, sources func(context.Context) ([]Project, error), interval, jitter time.Duration, onTick func(TickResult)) {
	tick := func() {
		projects, err := sources(ctx)
		if err != nil {
			return
		}
		result, err := m.Tick(ctx, projects)
		if err != nil || onTick == nil {
			return
		}
		onTick(result)
	}
	tick()
	for {
		wait := interval
		if jitter > 0 {
			wait += time.Duration(rand.Int63n(int64(jitter)))
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			tick()
		}
	}
}
