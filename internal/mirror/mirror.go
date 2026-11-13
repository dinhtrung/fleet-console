// Package mirror reads the forge, the rig and origin, and writes only the app's
// mirror/cache tables. A forge read that fails marks the project stale; it never
// replaces the cached rows with an empty set.
package mirror

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"runtime/debug"
	"sort"
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
	Rigs     []string
	RepoPath string
	Git      git.Runner
}

// ProjectResult is one project's read outcome for this tick.
type ProjectResult struct {
	Items          []forge.Item
	CRs            []forge.ChangeRequest
	Branches       []git.Branch
	Rows           []rig.QueueRow
	Seats          []rig.Seat
	ForgeStale     bool
	ForgeForbidden bool
	ForgeError     string
	BranchError    string
	RigWarnings    []string
}

// TickResult is everything one tick read.
type TickResult struct {
	At       time.Time
	Projects map[string]ProjectResult
	RigStale bool
	RigError string
	// RigDown is the one rig failure with a decision attached (epic #320, D1):
	// no daemon answered. Everything else that sets RigStale keeps the
	// lifecycle's generic refusal — a timeout is not a missing daemon.
	RigDown bool
}

// Mirror runs the read pass.
type Mirror struct {
	Store          *store.Store
	Rig            *rig.Adapter
	Now            func() time.Time
	ProjectTimeout TimeoutSettings
	Logf           func(format string, args ...any)
	// OnWait, when set, records each wait the loop is about to sleep. A test
	// seam: production leaves it nil and the loop just sleeps. It runs before
	// the timer starts, so a recorder that cancels the context stops the loop
	// without the test waiting out a real interval.
	OnWait func(time.Duration)
}

// TimeoutSettings scales one project's read deadline with its item count. The
// forge adapter applies it to the per-item phase once it knows the count; the
// cached count covers the change-request and git reads.
type TimeoutSettings = forge.Timeout

// projectTimeoutFor returns base + per_item*items, capped at the ceiling, from
// the cached item count. The first tick of a new project uses the base; the
// adapter scales its own per-item phase from the count it just listed.
func (m *Mirror) projectTimeoutFor(ctx context.Context, projectID int64) time.Duration {
	items := 0
	if m.Store != nil {
		if count, err := m.Store.ItemCount(ctx, projectID); err == nil {
			items = count
		}
	}
	return m.ProjectTimeout.For(items)
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
	ForgeStale     bool     `json:"forge_stale"`
	ForgeForbidden bool     `json:"forge_forbidden"`
	RigStale       bool     `json:"rig_stale"`
	RigDown        bool     `json:"rig_down"`
	ForgeError     string   `json:"forge_error,omitempty"`
	RigError       string   `json:"rig_error,omitempty"`
	BranchError    string   `json:"branch_error,omitempty"`
	RigWarnings    []string `json:"rig_warnings,omitempty"`
}

// Tick reads every project once. Forge failures keep the previous item rows and
// stamp; the status snapshot records stale plus the error.
func (m *Mirror) Tick(ctx context.Context, projects []Project) (TickResult, error) {
	at := m.now()
	result := TickResult{At: at, Projects: make(map[string]ProjectResult, len(projects))}

	var rows []rig.QueueRow
	var seats []rig.Seat
	seatsRead := false
	if m.Rig != nil {
		var err error
		rows, err = m.Rig.QueueList(ctx)
		if err != nil {
			result.RigStale, result.RigError = true, err.Error()
			result.RigDown = errors.Is(err, rig.ErrDaemonDown)
		} else {
			rows = m.fillBodies(ctx, rows)
		}
		seats, err = m.Rig.Seats(ctx)
		if err != nil {
			result.RigStale = true
			if result.RigError == "" {
				result.RigError = err.Error()
			}
			// The missing daemon is the one failure with a decision attached:
			// the tick carries it beside rig_stale. A timeout or a broken
			// shape stays stale-only.
			if !result.RigDown {
				result.RigDown = errors.Is(err, rig.ErrDaemonDown)
			}
		} else {
			seatsRead = true
		}
	}

	for _, project := range projects {
		pr := ProjectResult{
			Rows:  filterRows(rows, project.Rigs),
			Seats: filterSeats(seats, project.Rigs),
		}
		// Only a read that answered can say a rig has no seats. After a failed
		// read every configured rig would look absent, so the stale flag and
		// RigError carry the truth and no rig is named as a ghost.
		if seatsRead {
			pr.RigWarnings = missingRigs(project.Rigs, seats)
		}

		timeout := m.projectTimeoutFor(ctx, project.ID)
		if project.Forge != nil {
			accepted := m.acceptedSHAs(ctx, project.ID)
			// Items applies project_timeout to its per-item phase from the count it
			// lists; the context below is the hard ceiling for the whole forge read.
			forgeCtx, cancel := context.WithTimeout(ctx, m.ProjectTimeout.Hard())
			items, itemsErr := project.Forge.Items(forgeCtx, m.ProjectTimeout)
			var crs []forge.ChangeRequest
			var crsErr error
			if reader, ok := project.Forge.(forge.CandidateReader); ok {
				crs, crsErr = reader.ChangeRequestsFor(forgeCtx, accepted)
			} else {
				crs, crsErr = project.Forge.ChangeRequests(forgeCtx)
			}
			cancel()
			if itemsErr != nil || crsErr != nil {
				pr.ForgeStale = true
				chosen := itemsErr
				if chosen == nil {
					chosen = crsErr
				}
				// A 403 is a permission failure, not a transient read; prefer its
				// error (which names the operation) when either read hit it.
				switch {
				case errors.Is(itemsErr, forge.ErrForbidden):
					chosen, pr.ForgeForbidden = itemsErr, true
				case errors.Is(crsErr, forge.ErrForbidden):
					chosen, pr.ForgeForbidden = crsErr, true
				}
				pr.ForgeError = chosen.Error()
			}
			// Items stand on their own: a failed change-request read keeps the
			// item rows current and records the failure, instead of blanking the
			// board for the whole project.
			if itemsErr == nil {
				pr.Items = items
				records := make([]store.ItemRecord, 0, len(items))
				for _, item := range items {
					records = append(records, itemRecord(item, at))
				}
				if err := m.Store.ReplaceItems(ctx, project.ID, records); err != nil {
					return result, err
				}
			}
			if itemsErr == nil && crsErr == nil {
				pr.CRs = crs
				if err := m.Store.PutSnapshot(ctx, project.ID, "forge", forgeSnapshot{Items: items, CRs: crs}, at); err != nil {
					return result, err
				}
				if err := m.Store.SetForgeStamp(ctx, project.ID, at); err != nil {
					return result, err
				}
			}
		}

		if project.Git != nil {
			gitCtx, cancel := context.WithTimeout(ctx, timeout)
			branches, err := git.Index(gitCtx, project.Git)
			cancel()
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
			ForgeStale: pr.ForgeStale, ForgeForbidden: pr.ForgeForbidden, ForgeError: pr.ForgeError,
			RigStale: result.RigStale, RigDown: result.RigDown, RigError: result.RigError,
			BranchError: pr.BranchError, RigWarnings: pr.RigWarnings,
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

// acceptedSHAs is the set of head shas with an ACCEPT verdict. A failed read
// falls back to nil, which makes the forge fetch every open change's details:
// slow but correct.
func (m *Mirror) acceptedSHAs(ctx context.Context, projectID int64) map[string]struct{} {
	verdicts, err := m.Store.VerdictsForProject(ctx, projectID)
	if err != nil {
		return nil
	}
	accepted := make(map[string]struct{})
	for _, verdict := range verdicts {
		if strings.EqualFold(verdict.Decision, "accept") {
			accepted[strings.ToLower(verdict.SHA)] = struct{}{}
		}
	}
	return accepted
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

// missingRigs names configured rigs with no live seats. A project with one of
// these has no builders for that rig and must not dispatch to it.
func missingRigs(rigs []string, seats []rig.Seat) []string {
	if len(rigs) == 0 {
		return nil
	}
	present := make(map[string]bool, len(seats))
	for _, seat := range seats {
		present[seat.Rig] = true
	}
	var missing []string
	for _, rigName := range rigs {
		if !present[rigName] {
			missing = append(missing, rigName)
		}
	}
	return missing
}

// filterRows keeps the project's rows: those addressed to one of its rigs, and
// those parked on a person. The second kind is not noise to drop — a decision
// row lives on a seat that is often outside the project's rigs (an @kernel
// operator or advisor), and the operator still owes an answer. Removing it here
// is how the quiet rule stopped seeing the very obligations it must never mute
// (issue #143).
func filterRows(rows []rig.QueueRow, rigs []string) []rig.QueueRow {
	if len(rigs) == 0 {
		return rows
	}
	wanted := make(map[string]bool, len(rigs))
	for _, rigName := range rigs {
		wanted[rigName] = true
	}
	out := make([]rig.QueueRow, 0, len(rows))
	for _, row := range rows {
		if at := strings.LastIndex(row.Destination, "@"); at >= 0 && wanted[row.Destination[at+1:]] {
			out = append(out, row)
			continue
		}
		if rig.WaitsOnHuman(row.State, row.Destination, row.BlockedOn) {
			out = append(out, row)
		}
	}
	return out
}

// filterSeats keeps the project's seats and orders them by the configured rig
// list, so the plan fills rigs in the order the file lists them.
func filterSeats(seats []rig.Seat, rigs []string) []rig.Seat {
	if len(rigs) == 0 {
		return seats
	}
	order := make(map[string]int, len(rigs))
	for index, rigName := range rigs {
		order[rigName] = index
	}
	out := make([]rig.Seat, 0, len(seats))
	for _, seat := range seats {
		if _, ok := order[seat.Rig]; ok {
			out = append(out, seat)
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return order[out[a].Rig] < order[out[b].Rig] })
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
			Rigs: decideRigRefs(project.RigTiers),
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
		// An ended attempt is not a live duplicate: the item may be dispatched
		// again on a later tick (D4 reconciliation ends it first).
		if dispatch.EndedAt == nil {
			snapshot.Dispatched = append(snapshot.Dispatched, dispatch.ItemNumber)
		}
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

// decideRigRefs converts the store's persisted tier list into the plan's own shape. An empty list
// means the project declares no tiers, and the plan keeps the flat behaviour.
func decideRigRefs(tiers []store.RigTier) []decide.RigRef {
	if len(tiers) == 0 {
		return nil
	}
	out := make([]decide.RigRef, 0, len(tiers))
	for _, tier := range tiers {
		spill := tier.Spill
		if spill == "" {
			spill = decide.SpillOnFull
		}
		out = append(out, decide.RigRef{Name: tier.Name, Tier: tier.Tier, Spill: spill})
	}
	return out
}

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
//
// The wait comes from the cadence: the caller's onTick verdict says whether the
// tick ended quiet (the wait doubles, capped at the cadence's Max) or busy (the
// wait returns to Base). A tick that could not read at all counts as busy, so a
// broken source is retried at the normal rate instead of the backed-off one.
// jitter is added to the chosen wait and never exceeds it, so a jittered wait
// cannot jump past the ceiling the operator set.
//
// onTick may be nil, which leaves every tick quiet. The cadence may be zero,
// which makes every wait Base — the pre-cadence behaviour, still the default for
// a box that never writes the rig_lifecycle block.
//
// wake lets another goroutine ask for a tick before the timer would fire. A
// receive runs the tick immediately and resets the cadence to Base, exactly as a
// busy tick does: the wait after a wake is tick_interval even when the tick it
// triggered found nothing. A nil channel disables the arm, so every caller that
// predates the wake keeps its behaviour. Coalescing belongs to the sender — see
// Waker — so this loop only ever sees one pending wake at a time.
func (m *Mirror) Loop(ctx context.Context, sources func(context.Context) ([]Project, error), cadence Cadence, jitter time.Duration, onTick func(TickResult) bool, wake <-chan struct{}) {
	seen := make(map[string]string)
	// quiet and reason describe the tick that just ran: whether it found nothing
	// anywhere, and when it did find something, the fact that ended the stretch.
	var quiet bool
	var reason string
	tick := func() {
		// A panic anywhere in a tick used to end this goroutine — and with it the
		// whole process, leaving the control plane dark until a restart (epic
		// #325, T2). One recover, here and nowhere else in the tree: the journal
		// gets the value and the stack, the tick counts as busy so the poll
		// returns to the base interval, and the loop carries on (design D3+D4).
		defer func() {
			if r := recover(); r != nil {
				m.logf("tick: panicked: %v\n%s", r, debug.Stack())
				quiet, reason = false, "tick: panicked: "+fmt.Sprint(r)
			}
		}()
		quiet, reason = true, ""
		projects, err := sources(ctx)
		if err != nil {
			m.note(seen, "sources", err)
			quiet, reason = false, "sources: "+err.Error()
			return
		}
		result, err := m.Tick(ctx, projects)
		if err != nil {
			m.note(seen, "tick", err)
			quiet, reason = false, "tick: "+err.Error()
			return
		}
		m.noteProjects(seen, result)
		if onTick == nil {
			return
		}
		if !onTick(result) {
			return
		}
		// Busy. Name the fact from the same read, so the reset line says why the
		// poll went back to the normal rate instead of only that it did.
		quiet = false
		_, reason = Busy(result)
		if reason == "" {
			reason = "the caller reported busy"
		}
	}
	// prev starts at the base so the first tick, which also returns the base,
	// logs nothing: a wait that did not change is not news.
	prev := cadence.Base
	next := func() time.Duration {
		// chosen is the cadence's own number, before jitter. The comparison and
		// the log line both use it, because jitter changes every wait: comparing
		// the jittered value would log a line on every quiet tick at the ceiling,
		// which is exactly the noise task 4.3 forbids.
		chosen := cadence.Next(quiet)
		if chosen != prev {
			// One line per change, and only a change: an operator reads this to
			// tell a deliberately slow poll from a hung loop, so a repeated wait
			// would bury the two lines that matter.
			if cadence.Streak() > 0 {
				m.logf("cadence %s (quiet %d)", chosen, cadence.Streak())
			} else {
				m.logf("cadence %s reset: %s", chosen, reason)
			}
			prev = chosen
		}
		wait := chosen
		if jitter > 0 {
			// Capped at the wait itself, so a long tick_interval does not turn a
			// short backed-off wait into a much longer one.
			spread := jitter
			if spread > wait {
				spread = wait
			}
			wait += time.Duration(rand.Int63n(int64(spread)))
		}
		return wait
	}
	tick()
	for {
		wait := next()
		if m.OnWait != nil {
			m.OnWait(wait)
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			tick()
		case <-wake:
			timer.Stop()
			tick()
			// A wake is a busy verdict even when the tick it triggered found
			// nothing: something changed out there, and the next wait must be
			// tick_interval rather than a further doubling. This is the same
			// path a busy tick takes — cadence.Next(false) clears the streak and
			// returns Base — so the cadence's own arithmetic is untouched.
			quiet = false
			reason = "woken by a forge delivery"
		}
	}
}

// logf writes one line when a logger is configured. The loop logs only changes,
// so a nil logger costs nothing on a quiet box.
func (m *Mirror) logf(format string, args ...any) {
	if m.Logf != nil {
		m.Logf(format, args...)
	}
}

// note logs an error once per change. A recovered key forgets its error, so a
// recurrence is logged again.
func (m *Mirror) note(seen map[string]string, key string, err error) {
	if err == nil {
		delete(seen, key)
		return
	}
	message := err.Error()
	if seen[key] == message {
		return
	}
	seen[key] = message
	if m.Logf != nil {
		m.Logf("%s: %v", key, err)
	}
}

// noteProjects reports each project's stale reason without repeating an
// unchanged one.
func (m *Mirror) noteProjects(seen map[string]string, result TickResult) {
	for key, project := range result.Projects {
		var forgeErr, branchErr error
		if project.ForgeError != "" {
			forgeErr = errors.New(project.ForgeError)
		}
		if project.BranchError != "" {
			branchErr = errors.New(project.BranchError)
		}
		m.note(seen, key+" forge", forgeErr)
		m.note(seen, key+" branch", branchErr)
		for _, rigName := range project.RigWarnings {
			m.note(seen, key+" rig "+rigName, errors.New("rig "+rigName+" has no running seats"))
		}
	}
	var rigErr error
	if result.RigError != "" {
		rigErr = errors.New(result.RigError)
	}
	m.note(seen, "rig", rigErr)
}
