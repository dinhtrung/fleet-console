// Package act executes decide intents through the adapters. It is the only writer,
// serialized per project, and records one event per write. It never deletes a
// branch or a worktree.
package act

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// Rig is the queue subset the act loop drives. *rig.Adapter implements it.
// QueueShow reads one row back, which the repair reconcile needs to learn a
// row's state before resolving it (#340).
type Rig interface {
	QueueCreate(ctx context.Context, req rig.CreateRequest) (*rig.QueueItem, error)
	QueueResolve(ctx context.Context, id, decision string) (*rig.QueueItem, error)
	QueueHandoff(ctx context.Context, id string, req rig.HandoffRequest) (*rig.QueueItem, error)
	QueueUpdate(ctx context.Context, id string, req rig.UpdateRequest) (*rig.QueueItem, error)
	QueueShow(ctx context.Context, id string) (*rig.QueueItem, error)
	Send(ctx context.Context, session, text string) error
}

// Actions is the act pass's dependency set.
//
// ProjectID scopes every read that item numbers alone cannot: item numbers are
// unique only within a project, so a verdict or a dispatch lookup that omits it
// can match another project's work. It is set once per tick from
// TickInput.Project.
type Actions struct {
	Store     *store.Store
	Forge     forge.Forge
	Rig       Rig
	Git       Git
	Now       func() time.Time
	ProjectID int64
	// ProjectKey and WorktreeRoot name the project for the raises some write
	// failures produce (#260) and where their briefs go. They are set once per
	// tick; the resolver construction in cmd leaves them empty, which every
	// raise treats as a best-effort default.
	ProjectKey   string
	WorktreeRoot string
}

func (a *Actions) now() time.Time {
	if a.Now != nil {
		return a.Now().UTC()
	}
	return time.Now().UTC()
}

func (a *Actions) event(ctx context.Context, actor, kind, subject, detail string) (int64, error) {
	return a.Store.AppendEvent(ctx, store.Event{Actor: actor, Kind: kind, Subject: subject, Detail: detail})
}

// DispatchInput is one decide dispatch intent.
type DispatchInput struct {
	ProjectID    int64
	ProjectKey   string
	Item         forge.Item
	Seat         string
	WorktreeRoot string
	Slug         string
}

// DispatchResult is the dispatch evidence.
type DispatchResult struct {
	DispatchID int64
	QItemID    string
	Branch     string
	Worktree   string
	BaseSHA    string
	BriefPath  string
	Reused     bool
	NoOp       bool
	Events     []int64
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slug(value string) string {
	value = strings.ToLower(value)
	value = strings.Trim(nonSlug.ReplaceAllString(value, "-"), "-")
	if len(value) > 40 {
		value = strings.TrimRight(value[:40], "-")
	}
	if value == "" {
		return "task"
	}
	return value
}

const briefTemplate = `# %s #%d: %s

Rig: %s
Worktree: %s
Branch: %s
Base sha: %s

## Brief

%s

## Publish

The merge gate finds the item only through "Closes #<item>" in the pull request's body: a body that only references
the item (Refs, Part of, a title mention) is invisible to it, and that change request is never merged, undrafted or
recorded. If this attempt cannot finish the item, do not open a change request that only references it — hand off
with the candidate sha, the commands and exactly what remains, so the item can be split and each part gets its own
item and its own "Closes #<sub>".

Commit on this branch with explicit paths. Push the branch and open one DRAFT pull request whose body carries
"Closes #%d". Do not merge. Hand off with the candidate sha, the commands you ran and what you could not check.

## The work

%s
`

// Dispatch fetches, resolves the base, adds or reuses the worktree, writes the brief,
// creates the queue row, sets the marker and records the dispatch. A second call for
// the same open dispatch is a no-op naming the existing row.
func (a *Actions) Dispatch(ctx context.Context, in DispatchInput) (*DispatchResult, error) {
	if a.Store == nil || a.Forge == nil || a.Rig == nil || a.Git == nil {
		return nil, fmt.Errorf("dispatch: store, forge, rig and git are required")
	}
	if in.Item.Number <= 0 || in.Seat == "" || in.WorktreeRoot == "" {
		return nil, fmt.Errorf("dispatch: item, seat and worktree root are required")
	}
	existing, err := a.Store.Dispatches(ctx, in.ProjectID, in.Item.Number)
	if err != nil {
		return nil, err
	}
	attempt := len(existing) + 1
	if len(existing) > 0 && existing[0].EndedAt == nil {
		d := existing[0]
		return &DispatchResult{
			DispatchID: d.ID, QItemID: d.QItemID, Branch: d.Branch, Worktree: d.Worktree,
			BaseSHA: d.BaseSHA, BriefPath: d.BriefPath, NoOp: true,
		}, nil
	}

	if err := a.Git.Fetch(ctx); err != nil {
		return nil, err
	}
	base, err := a.Git.BaseSHA(ctx)
	if err != nil {
		return nil, err
	}
	slugValue := in.Slug
	if slugValue == "" {
		slugValue = slug(in.Item.Title)
	}
	branch := fmt.Sprintf("feature/%d-%s", in.Item.Number, slugValue)
	worktree := filepath.Join(in.WorktreeRoot, fmt.Sprintf("wt-%s-%d", in.ProjectKey, in.Item.Number))
	reused, err := a.Git.WorktreeAddOrReuse(ctx, worktree, branch, base)
	if err != nil {
		return nil, err
	}

	briefDir := filepath.Join(in.WorktreeRoot, "briefs")
	if err := os.MkdirAll(briefDir, 0o755); err != nil {
		return nil, fmt.Errorf("dispatch: brief dir: %w", err)
	}
	briefPath := filepath.Join(briefDir, fmt.Sprintf("%s-%d-attempt%d.md", in.ProjectKey, in.Item.Number, attempt))
	rigName := rigFromSeat(in.Seat)
	if rigName == "" {
		rigName = "(unknown)"
	}
	brief := fmt.Sprintf(briefTemplate,
		in.ProjectKey, in.Item.Number, in.Item.Title, rigName, worktree, branch, base,
		in.Item.Body, in.Item.Number, in.Item.Body)
	if err := os.WriteFile(briefPath, []byte(brief), 0o644); err != nil {
		return nil, fmt.Errorf("dispatch: write brief: %w", err)
	}

	// Two-phase dispatch: the app row exists before the rig row, so a crash
	// between them leaves a pending dispatch that boot adoption resolves instead
	// of a second queue row on the next tick.
	dispatchID, err := a.Store.CreateDispatch(ctx, store.NewDispatch{
		ProjectID: in.ProjectID, ItemNumber: in.Item.Number, Attempt: attempt, Seat: in.Seat,
		Branch: branch, Worktree: worktree, BaseSHA: base, QItemID: "",
		BriefPath: briefPath, State: "pending", CreatedAt: a.now(),
	})
	if err != nil {
		return nil, err
	}
	row, err := a.Rig.QueueCreate(ctx, rig.CreateRequest{
		Destination: in.Seat,
		Summary:     fmt.Sprintf("#%d %s", in.Item.Number, in.Item.Title),
		BodyFile:    briefPath,
		Source:      "human@kernel",
	})
	if err != nil {
		return nil, err
	}
	if err := a.Store.StampDispatch(ctx, dispatchID, row.ID); err != nil {
		return nil, err
	}
	if err := a.Forge.SetMarker(ctx, in.Item.Number, true); err != nil {
		return nil, err
	}
	eventID, err := a.event(ctx, "act", "dispatch.create", fmt.Sprintf("#%d", in.Item.Number),
		fmt.Sprintf("seat %s row %s branch %s", in.Seat, row.ID, branch))
	if err != nil {
		return nil, err
	}
	return &DispatchResult{
		DispatchID: dispatchID, QItemID: row.ID, Branch: branch, Worktree: worktree,
		BaseSHA: base, BriefPath: briefPath, Reused: reused, Events: []int64{eventID},
	}, nil
}

// ResolveRow answers a parked row.
func (a *Actions) ResolveRow(ctx context.Context, id, decision string) (*rig.QueueItem, error) {
	item, err := a.Rig.QueueResolve(ctx, id, decision)
	if err != nil {
		return nil, err
	}
	if _, err := a.event(ctx, "act", "row.resolve", id, fmt.Sprintf("%s -> %s", decision, item.State)); err != nil {
		return nil, err
	}
	return item, nil
}

// HandoffRow hands a row to another seat.
func (a *Actions) HandoffRow(ctx context.Context, id string, req rig.HandoffRequest) (*rig.QueueItem, error) {
	item, err := a.Rig.QueueHandoff(ctx, id, req)
	if err != nil {
		return nil, err
	}
	if _, err := a.event(ctx, "act", "row.handoff", id, fmt.Sprintf("to %s -> %s", req.To, item.State)); err != nil {
		return nil, err
	}
	return item, nil
}

// CloseSuperseded closes a row as superseded by another.
func (a *Actions) CloseSuperseded(ctx context.Context, id, target string) (*rig.QueueItem, error) {
	item, err := a.Rig.QueueUpdate(ctx, id, rig.UpdateRequest{
		State: "canceled", ClosureReason: "superseded", ClosureTarget: target,
	})
	if err != nil {
		return nil, err
	}
	if _, err := a.event(ctx, "act", "row.superseded", id, fmt.Sprintf("superseded by %s", target)); err != nil {
		return nil, err
	}
	return item, nil
}

// Renudge sends one nudge to the seat holding an unclaimed row.
func (a *Actions) Renudge(ctx context.Context, id, seat, text string) error {
	if err := a.Rig.Send(ctx, seat, text); err != nil {
		return err
	}
	_, err := a.event(ctx, "act", "row.renudge", id, "nudged "+seat)
	return err
}

// rigFromSeat reads the rig from a canonical seat name (pod-member@rig).
func rigFromSeat(seat string) string {
	at := strings.LastIndex(seat, "@")
	if at < 0 || at == len(seat)-1 {
		return ""
	}
	return seat[at+1:]
}
