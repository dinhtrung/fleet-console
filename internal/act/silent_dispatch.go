package act

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// EventSilentDispatch is the audit kind one silent dispatch dedupes on — the
// kind decide's intent already carries (#261). The ledger is the dedupe, the
// same once-per-fact shape every raise uses, so the raise adds no second
// mechanism.
const EventSilentDispatch = "dispatch.silent"

// SilentDispatchRaise is the evidence of one raised silent-dispatch question:
// the row id, the wording the phone hook sends, and the journal line. NoOp
// marks the deduped case, where a question for this (item, attempt) already
// exists.
type SilentDispatchRaise struct {
	RaiseResult
	Question string
	Journal  string
}

// SilentDispatchInput names one live dispatch that has produced no change
// request. The caller reads the facts off the store's dispatch row at tick
// time, so a dispatch that ended between the plan and the raise raises
// nothing; the title is filled from the store when the caller has none.
type SilentDispatchInput struct {
	Item       int
	Title      string
	Seat       string
	Branch     string
	Worktree   string
	Attempt    int
	Age        time.Duration
	Repo       string // the forge repo slug, for the one distinguishing command
	ProjectKey string
}

// RaiseSilentDispatch asks the operator about a live dispatch that produced
// no change request (#261). Once per (item, attempt), deduped on the event
// ledger the way RaisePermissionDecision dedupes — one mechanism, not two.
// The fleet does not kill the seat and does not re-dispatch: a second builder
// on the same branch would close one item from two heads, and the merge gate
// refuses both. The brief names both hypotheses — the builder never pushed,
// or the forge refused the write — and the one command that tells them apart.
func (a *Actions) RaiseSilentDispatch(ctx context.Context, in SilentDispatchInput) (*SilentDispatchRaise, error) {
	if a.Store == nil || a.Rig == nil {
		return nil, fmt.Errorf("raise silent dispatch: store and rig are required")
	}
	subject := fmt.Sprintf("#%d", in.Item)
	// The trailing space keeps attempt 1 from matching attempt 10: both the
	// decide event and this raise spell the detail `... attempt=<n> age=...`,
	// so one space after the value is the boundary.
	key := fmt.Sprintf("attempt=%d ", in.Attempt)
	exists, err := a.Store.HasEvent(ctx, EventSilentDispatch, subject, key)
	if err != nil {
		return nil, err
	}
	if exists {
		return &SilentDispatchRaise{RaiseResult: RaiseResult{NoOp: true}}, nil
	}

	projectKey := in.ProjectKey
	if projectKey == "" {
		projectKey = "(project)"
	}
	title := in.Title
	if title == "" && a.ProjectID != 0 {
		if row, rowErr := a.Store.Item(ctx, a.ProjectID, in.Item); rowErr == nil && row != nil {
			title = row.Title
		}
	}
	if title == "" {
		title = "(no title recorded)"
	}
	minutes := int(in.Age.Round(time.Minute).Minutes())
	age := in.Age.Round(time.Minute)
	question := fmt.Sprintf("%s #%d (%s): dispatch produced no change request in %d minutes (seat %s, attempt %d)",
		projectKey, in.Item, title, minutes, in.Seat, in.Attempt)

	dir := a.WorktreeRoot
	if dir == "" {
		dir = os.TempDir()
	} else {
		dir = filepath.Join(dir, "briefs")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("raise silent dispatch: brief dir: %w", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("silent-%s-%d-attempt%d.md", slug(projectKey), in.Item, in.Attempt))
	body := fmt.Sprintf(`# Silent dispatch: %s #%d %s

The dispatch below is live and has produced no change request in %d minutes (#261).
The fleet does not kill the seat and does not re-dispatch: a second builder on the
same branch would close one item from two branches, and the merge gate refuses both.

- Seat: %s
- Branch: %s
- Worktree: %s
- Attempt: %d
- Brief: %s

## The two hypotheses

1. The builder never pushed or never opened the pull request.
2. The forge refused a write (see the permission questions, #258/#260).

## The one command that tells them apart

    gh pr list -R %s --head %s

Empty output is hypothesis 1: nothing left the seat. A listed pull request means
the work exists but the fleet's writes may have been refused — check the forge
token, or the preflight that proves it can write.

Seat transcript directory: %s (one file per occupant account)
`, projectKey, in.Item, title, minutes, in.Seat, in.Branch, in.Worktree, in.Attempt, path, in.Repo, in.Branch, transcriptDir(in.Seat))
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return nil, fmt.Errorf("raise silent dispatch: write brief: %w", err)
	}
	row, err := a.Rig.QueueCreate(ctx, rig.CreateRequest{
		Destination: "human@kernel",
		Summary:     fmt.Sprintf("#%d: dispatch produced no change request in %d minutes", in.Item, minutes),
		BodyFile:    path,
		Source:      "human@kernel",
	})
	if err != nil {
		return nil, err
	}
	eventID, err := a.event(ctx, "act", EventSilentDispatch, subject,
		fmt.Sprintf("seat=%s branch=%s attempt=%d age=%s; row %s", in.Seat, in.Branch, in.Attempt, age, row.ID))
	if err != nil {
		return nil, err
	}
	return &SilentDispatchRaise{
		RaiseResult: RaiseResult{QItemID: row.ID, Events: []int64{eventID}},
		Question:    question,
		Journal: fmt.Sprintf("#%d silent dispatch: seat=%s branch=%s attempt=%d age=%s; row %s",
			in.Item, in.Seat, in.Branch, in.Attempt, age, row.ID),
	}, nil
}

// transcriptDir names where the seat's transcript lives. OpenRig keys
// transcripts by rig and occupant account, not by seat name, so a dispatch
// row can name only the directory — one file per account that ever sat there.
func transcriptDir(seat string) string {
	rigName := rigFromSeat(seat)
	if rigName == "" {
		return "(unknown rig — run rig whoami in the seat)"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "(home directory unknown)"
	}
	return filepath.Join(home, ".openrig", "transcripts", rigName)
}

// dispatchAge is how long the dispatch has been live. The store spells
// timestamps as strings; the layouts are the ones the mirror parses.
func dispatchAge(dispatch store.DispatchRow, now func() time.Time) time.Duration {
	created := parseStoreTime(dispatch.CreatedAt)
	if created.IsZero() {
		return 0
	}
	return now().Sub(created)
}

// parseStoreTime reads the store's timestamp strings. The mirror keeps the
// same list, so a spelling one side writes the other can read.
func parseStoreTime(raw string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05Z"} {
		if at, err := time.Parse(layout, raw); err == nil {
			return at
		}
	}
	return time.Time{}
}
