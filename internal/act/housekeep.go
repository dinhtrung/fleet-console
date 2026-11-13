package act

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/housekeeping"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// The audit vocabulary. One event per removal and one per run, so the record
// answers "what did the sweep delete, and what did it leave behind" without
// replaying the summary's arithmetic from the log.
const (
	EventHousekeepingRemove  = "housekeeping.remove"
	EventHousekeepingSummary = "housekeeping.summary"
	// EventHousekeepingRetention is one prune run's record (#395): the window,
	// the counts, and whether it changed anything.
	EventHousekeepingRetention = "housekeeping.retention"
)

// HousekeepInput is one project's reclaim pass. The tick supplies it after the
// dispatch phase, so a worktree an item was just dispatched into already has a
// live dispatch row when the plan reads it (design D5).
type HousekeepInput struct {
	Store   *store.Store
	Project store.ProjectRow
	// ForgeOK reports whether the forge read for this project succeeded this tick.
	// A failed read proposes nothing: "the read failed" is not "nothing is left".
	ForgeOK      func() bool
	Items        []forge.Item
	CRs          []forge.ChangeRequest
	Rows         []rig.QueueRow
	LiveItems    []int
	WorktreeRoot string
	Git          housekeeping.Git
	// Apply false prints the plan and changes nothing: the dry run.
	Apply bool
	Now   func() time.Time
}

// HousekeepReport is what one pass did, for the log line and the tests.
type HousekeepReport struct {
	Project    string
	ForgeStale bool
	Applied    bool
	// Removed and Kept are the plan's own decisions after Execute ran: a git
	// refusal moves a decision from removed to kept, so these counts are the
	// truth rather than the plan's intent.
	Removed []string
	Kept    map[housekeeping.Reason]int
	Failed  int
	// SummaryEventID and RemoveEventIDs make the audit trail checkable in a test
	// without re-querying by kind.
	SummaryEventID int64
	RemoveEventIDs []int64
}

// Housekeep runs one project's reclaim pass and writes its audit events.
//
// The plan is the read-only half; Execute is the half that removes and is called
// only when Apply is true. A forge read that failed proposes nothing at all —
// not "nothing is left", which would delete every worktree on the board.
func Housekeep(ctx context.Context, in HousekeepInput) (HousekeepReport, error) {
	report := HousekeepReport{Project: in.Project.Key, Applied: in.Apply}
	now := in.Now
	if now == nil {
		now = time.Now().UTC
	}
	plan, err := housekeeping.Plan(ctx, housekeeping.Input{
		Project:      in.Project.Key,
		WorktreeRoot: in.WorktreeRoot,
		ForgeOK:      in.ForgeOK != nil && in.ForgeOK(),
		Items:        in.Items,
		CRs:          in.CRs,
		QueueRows:    in.Rows,
		LiveItems:    in.LiveItems,
		Git:          in.Git,
	})
	if err != nil {
		return report, err
	}
	report.ForgeStale = plan.ForgeStale
	decisions := plan.Decisions
	if in.Apply {
		if decisions, err = housekeeping.Execute(ctx, decisions, in.Git); err != nil {
			return report, err
		}
	}
	report.Kept = make(map[housekeeping.Reason]int, 8)
	for _, decision := range decisions {
		if decision.Remove {
			report.Removed = append(report.Removed, decision.Candidate.Path)
			if in.Apply {
				id, err := appendHousekeepEvent(ctx, in.Store, decision, now())
				if err != nil {
					return report, err
				}
				report.RemoveEventIDs = append(report.RemoveEventIDs, id)
			}
			continue
		}
		report.Kept[decision.Reason]++
		if decision.Reason == housekeeping.ReasonGitRefused {
			report.Failed++
			if in.Apply {
				if _, err := appendHousekeepEvent(ctx, in.Store, decision, now()); err != nil {
					return report, err
				}
			}
		}
	}
	if !in.Apply {
		return report, nil
	}
	// One summary per run, written even when it removed nothing: "the sweep ran and
	// found nothing" is a fact the operator needs, and its absence would be
	// indistinguishable from "the sweep never ran".
	subject := fmt.Sprintf("project %s", in.Project.Key)
	detail := summaryDetail(report)
	id, err := in.Store.AppendEvent(ctx, store.Event{
		Actor: "housekeeping", Kind: EventHousekeepingSummary, Subject: subject, Detail: detail,
	})
	if err != nil {
		return report, err
	}
	report.SummaryEventID = id
	return report, nil
}

// appendHousekeepEvent records one removal decision. A failed removal carries the
// error in its detail and its exit code, so the row says "refused" without the
// reader parsing prose.
func appendHousekeepEvent(ctx context.Context, st *store.Store, decision housekeeping.Decision, now time.Time) (int64, error) {
	candidate := decision.Candidate
	detail := "removed " + candidate.Path
	var exitCode *int
	if decision.Reason == housekeeping.ReasonGitRefused {
		detail = fmt.Sprintf("refused %s: %s", candidate.Path, decision.Detail)
		failed := 1
		exitCode = &failed
	} else if candidate.Branch != "" {
		detail = fmt.Sprintf("removed %s (branch %s)", candidate.Path, candidate.Branch)
	}
	subject := fmt.Sprintf("item %d", candidate.Item)
	if candidate.Item == 0 {
		subject = candidate.Path
	}
	return st.AppendEvent(ctx, store.Event{
		Actor: "housekeeping", Kind: EventHousekeepingRemove, Subject: subject,
		Detail: detail, ExitCode: exitCode,
	})
}

// summaryDetail renders the counts the design names: removed, each keep reason,
// and the failed removals. It is one line so a log reader can grep it.
func summaryDetail(report HousekeepReport) string {
	parts := []string{fmt.Sprintf("removed=%d", len(report.Removed))}
	for _, reason := range []housekeeping.Reason{
		housekeeping.ReasonOpenCR,
		housekeeping.ReasonLiveDispatch,
		housekeeping.ReasonLiveRow,
		housekeeping.ReasonDirty,
		housekeeping.ReasonGitRefused,
		housekeeping.ReasonSymlink,
		housekeeping.ReasonOutsideRoot,
	} {
		if count := report.Kept[reason]; count > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", reason, count))
		}
	}
	if report.Failed > 0 {
		parts = append(parts, fmt.Sprintf("failed=%d", report.Failed))
	}
	if report.ForgeStale {
		parts = append(parts, "forge-stale=1")
	}
	return strings.Join(parts, " ")
}

// Scheduler decides whether a tick should run housekeeping. It holds the
// last-run stamp in memory: the interval is a cadence, not a durable promise, so
// a restart legitimately resets it and the first run waits one full interval
// (design D6) rather than firing at boot.
type Scheduler struct {
	mu       sync.Mutex
	interval time.Duration
	last     time.Time
	started  time.Time
}

// NewScheduler builds a scheduler for interval. A zero interval means off: Due
// always answers false.
func NewScheduler(interval time.Duration, now time.Time) *Scheduler {
	return &Scheduler{interval: interval, started: now}
}

// Due reports whether the interval has elapsed since the last run (or since the
// process started, for the first run) and records the run's time when it returns
// true. A zero interval is never due. Recording inside Due is what makes two
// callers in one tick see one run.
func (s *Scheduler) Due(now time.Time) bool {
	if s == nil || s.interval <= 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	reference := s.last
	if reference.IsZero() {
		reference = s.started
	}
	if now.Sub(reference) < s.interval {
		return false
	}
	s.last = now
	return true
}

// RetentionInput is one ledger prune, global to the box: the event table has no
// project column, so the pass runs once per housekeeping cycle rather than per
// project (#395).
type RetentionInput struct {
	Store *store.Store
	// KeepDays is the window. Zero or less means retention is off: the pass
	// does nothing and writes nothing, because "disabled" is not a run.
	KeepDays int
	// Apply false counts what it would delete and writes nothing but the record
	// of the run — the dry run every housekeeping step shares.
	Apply bool
	Now   func() time.Time
}

// RetentionReport is what one prune did, or would do.
type RetentionReport struct {
	Window   time.Time
	Scanned  int
	Deleted  int
	KeptLive int
	Applied  bool
	EventID  int64
}

// RetainEvents prunes the ledger by a window and records the run: one
// housekeeping.retention event carrying scanned, deleted and kept_live, so the
// audit answers what left and what live work kept. The guards (a live dispatch,
// an unresolved queue row) live in the store's delete, next to the rows they
// protect.
func RetainEvents(ctx context.Context, in RetentionInput) (RetentionReport, error) {
	if in.Store == nil || in.KeepDays <= 0 {
		return RetentionReport{}, nil
	}
	clock := in.Now
	if clock == nil {
		clock = time.Now
	}
	cutoff := clock().UTC().AddDate(0, 0, -in.KeepDays)
	counts, err := in.Store.PruneEvents(ctx, cutoff, 500, in.Apply)
	if err != nil {
		return RetentionReport{}, err
	}
	report := RetentionReport{
		Window: cutoff, Scanned: counts.Scanned, Deleted: counts.Deleted,
		KeptLive: counts.KeptLive, Applied: in.Apply,
	}
	dry := "false"
	if !in.Apply {
		dry = "true"
	}
	detail := fmt.Sprintf("window=%dd cutoff=%s scanned=%d deleted=%d kept_live=%d dry_run=%s",
		in.KeepDays, cutoff.Format(time.RFC3339), report.Scanned, report.Deleted, report.KeptLive, dry)
	id, err := in.Store.AppendEvent(ctx, store.Event{
		Actor: "housekeeping", Kind: EventHousekeepingRetention, Subject: "ledger", Detail: detail,
	})
	if err != nil {
		return report, err
	}
	report.EventID = id
	return report, nil
}
