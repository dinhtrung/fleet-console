package act

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// AdoptionRig is the queue subset the boot/tick adoption pass reads and writes.
type AdoptionRig interface {
	QueueList(ctx context.Context) ([]rig.QueueRow, error)
	QueueShow(ctx context.Context, id string) (*rig.QueueItem, error)
	QueueCreate(ctx context.Context, req rig.CreateRequest) (*rig.QueueItem, error)
}

// AdoptionResult is one project's pending-dispatch reconciliation.
type AdoptionResult struct {
	Adopted   []int64  // pending dispatch ids stamped with a queue row
	Abandoned []int64  // pending dispatch ids closed without a row
	Ambiguous []int64  // pending dispatch ids with more than one candidate
	Decisions []string // queue rows created for an ambiguity
}

// AdoptPending resolves two-phase dispatches that never got their queue row
// stamped: exactly one matching row is adopted, none is abandoned, and more
// than one is abandoned with one decision asking a human to pick.
func AdoptPending(ctx context.Context, st *store.Store, rg AdoptionRig, project store.ProjectRow, worktreeRoot string) (AdoptionResult, error) {
	result := AdoptionResult{}
	if st == nil || rg == nil {
		return result, fmt.Errorf("adopt: store and rig are required")
	}
	pending, err := st.PendingDispatches(ctx, project.ID)
	if err != nil || len(pending) == 0 {
		return result, err
	}
	rows, err := rg.QueueList(ctx)
	if err != nil {
		return result, err
	}
	allowed := make(map[string]bool)
	for _, rigName := range project.Rigs {
		allowed[rigName] = true
	}
	if project.Rig != "" {
		allowed[project.Rig] = true
	}
	bodies := make(map[string]string)
	bodyOf := func(id string) string {
		if body, ok := bodies[id]; ok {
			return body
		}
		item, err := rg.QueueShow(ctx, id)
		if err != nil || item == nil {
			bodies[id] = ""
			return ""
		}
		bodies[id] = item.Body
		return item.Body
	}

	for _, dispatch := range pending {
		summary := briefSummary(dispatch)
		inProject := func(row rig.QueueRow) bool {
			if len(allowed) == 0 {
				return true
			}
			at := strings.LastIndex(row.Destination, "@")
			return at >= 0 && allowed[row.Destination[at+1:]]
		}
		var matches []rig.QueueRow
		// The exact brief summary is one cheap list comparison per row.
		for _, row := range rows {
			if inProject(row) && summary != "" && row.Summary == summary {
				matches = append(matches, row)
			}
		}
		// Branch matching needs the row body, so only try it when no summary
		// matched and only on rows that can still matter.
		if len(matches) == 0 && dispatch.Branch != "" {
			needle := fmt.Sprintf("#%d", dispatch.ItemNumber)
			for _, row := range rows {
				if !inProject(row) || !strings.Contains(row.Summary, needle) {
					continue
				}
				state := strings.ToLower(row.PickupState)
				if state == "" {
					state = strings.ToLower(row.State)
				}
				if _, terminal := reconcileTerminal[state]; terminal {
					continue
				}
				if strings.Contains(bodyOf(row.ID), "Branch: "+dispatch.Branch) {
					matches = append(matches, row)
				}
			}
		}
		switch len(matches) {
		case 1:
			if err := st.StampDispatch(ctx, dispatch.ID, matches[0].ID); err != nil {
				return result, err
			}
			result.Adopted = append(result.Adopted, dispatch.ID)
		case 0:
			if err := st.AbandonDispatch(ctx, dispatch.ID, "no queue row matched"); err != nil {
				return result, err
			}
			result.Abandoned = append(result.Abandoned, dispatch.ID)
		default:
			if err := st.AbandonDispatch(ctx, dispatch.ID, "ambiguous queue rows"); err != nil {
				return result, err
			}
			result.Abandoned = append(result.Abandoned, dispatch.ID)
			result.Ambiguous = append(result.Ambiguous, dispatch.ID)
			decision, err := raiseAdoptionDecision(ctx, rg, project, dispatch, matches, worktreeRoot)
			if err != nil {
				return result, err
			}
			result.Decisions = append(result.Decisions, decision.ID)
		}
	}
	return result, nil
}

// briefSummary derives the exact queue summary ("#5 Title") from the brief file
// the pending dispatch already wrote. An unreadable brief falls back to branch
// matching.
func briefSummary(dispatch store.DispatchRow) string {
	if dispatch.BriefPath == "" {
		return ""
	}
	raw, err := os.ReadFile(dispatch.BriefPath)
	if err != nil {
		return ""
	}
	line := strings.SplitN(string(raw), "\n", 2)[0]
	separator := strings.Index(line, ": ")
	if separator < 0 {
		return ""
	}
	title := strings.TrimSpace(line[separator+2:])
	if title == "" {
		return ""
	}
	return fmt.Sprintf("#%d %s", dispatch.ItemNumber, title)
}

func raiseAdoptionDecision(ctx context.Context, rg AdoptionRig, project store.ProjectRow, dispatch store.DispatchRow, matches []rig.QueueRow, worktreeRoot string) (*rig.QueueItem, error) {
	dir := worktreeRoot
	if dir == "" {
		dir = os.TempDir()
	} else {
		dir = filepath.Join(dir, "briefs")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("adopt: brief dir: %w", err)
	}
	var candidates strings.Builder
	for _, row := range matches {
		fmt.Fprintf(&candidates, "- %s (%s): %s\n", row.ID, row.State, row.Summary)
	}
	path := filepath.Join(dir, fmt.Sprintf("ambiguous-%s-%d.md", project.Key, dispatch.ItemNumber))
	body := fmt.Sprintf("# Ambiguous dispatch for #%d\n\nAttempt %d, branch %s, has %d matching queue rows:\n\n%s\nPick one row, close the others, and confirm the dispatch.\n",
		dispatch.ItemNumber, dispatch.Attempt, dispatch.Branch, len(matches), candidates.String())
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return nil, fmt.Errorf("adopt: write decision: %w", err)
	}
	row, err := rg.QueueCreate(ctx, rig.CreateRequest{
		Destination: "human@kernel",
		Summary:     fmt.Sprintf("ambiguous dispatch: #%d has %d queue rows", dispatch.ItemNumber, len(matches)),
		BodyFile:    path,
		Source:      "human@kernel",
	})
	if err != nil {
		return nil, err
	}
	return row, nil
}
