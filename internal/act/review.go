package act

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// RaiseResult is the evidence of one re-review decision.
type RaiseResult struct {
	QItemID string
	NoOp    bool
	Events  []int64
}

// RaiseDecision creates one queue row for the reviewer seat asking for a
// re-review. The event ledger makes it once per (item, text); the stale text
// carries the head, so a moved head raises exactly one new decision.
func (a *Actions) RaiseDecision(ctx context.Context, project store.ProjectRow, item int, text, kind, worktreeRoot string) (*RaiseResult, error) {
	if a.Store == nil || a.Rig == nil {
		return nil, fmt.Errorf("raise decision: store and rig are required")
	}
	subject := fmt.Sprintf("#%d", item)
	exists, err := a.Store.HasEvent(ctx, kind, subject, text)
	if err != nil {
		return nil, err
	}
	if exists {
		return &RaiseResult{NoOp: true}, nil
	}

	rigName := ""
	if dispatches, err := a.Store.Dispatches(ctx, project.ID, item); err == nil && len(dispatches) > 0 {
		rigName = rigFromSeat(dispatches[0].Seat)
	}
	if rigName == "" && len(project.Rigs) > 0 {
		rigName = project.Rigs[0]
	}
	reviewer := "dev-review"
	if rigName != "" {
		reviewer = "dev-review@" + rigName
	}
	dir := worktreeRoot
	if dir == "" {
		dir = os.TempDir()
	} else {
		dir = filepath.Join(dir, "briefs")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("raise decision: brief dir: %w", err)
	}
	sum := sha256.Sum256([]byte(text))
	path := filepath.Join(dir, fmt.Sprintf("review-%s-%d-%s.md", slug(project.Key), item, hex.EncodeToString(sum[:4])))
	body := fmt.Sprintf("# %s #%d - re-review\n\nRig: %s\n\n%s\n", project.Key, item, rigName, text)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return nil, fmt.Errorf("raise decision: write brief: %w", err)
	}
	summary := text
	if len(summary) > 120 {
		summary = summary[:120]
	}
	row, err := a.Rig.QueueCreate(ctx, rig.CreateRequest{
		Destination: reviewer, Summary: summary, BodyFile: path, Source: "human@kernel",
	})
	if err != nil {
		return nil, err
	}
	eventID, err := a.event(ctx, "act", kind, subject, text+"; row "+row.ID)
	if err != nil {
		return nil, err
	}
	return &RaiseResult{QItemID: row.ID, Events: []int64{eventID}}, nil
}
