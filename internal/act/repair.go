package act

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dinhtrung/fleet-console/internal/rig"
)

// RaiseResult is already declared in review.go; RaiseMergeRepair returns it too.

// RaiseMergeRepair raises the one decision the merge gate cannot make: an
// accepted change request that cannot merge because its head conflicts with the
// default branch (#251). The row goes to the human, naming the item, the change
// request, the head and the forge's own mergeability word, with the two options —
// rework the branch, or defer the item.
//
// It is once per (item, head sha): the text carries the head, and HasEvent is the
// ledger. A second tick at the same head is a no-op; a rebased head is a new key
// and may raise again, which is also the natural clear — once the rebased head
// merges, the item closes and nothing waits.
func (a *Actions) RaiseMergeRepair(ctx context.Context, projectKey string, failure *NotMergeableError, worktreeRoot string) (*RaiseResult, error) {
	if a.Store == nil || a.Rig == nil {
		return nil, fmt.Errorf("raise merge repair: store and rig are required")
	}
	if failure == nil {
		return nil, fmt.Errorf("raise merge repair: no refusal given")
	}
	reason := failure.Reason
	if reason == "" {
		reason = "unknown reason"
	}
	summary := fmt.Sprintf("#%d: accepted change request #%d cannot merge (%s)", failure.Item, failure.Number, reason)
	// The subject and the head are the idempotency key, exactly as the re-review
	// raise uses text-that-names-the-head: same item, same head, one row ever.
	kind := "decision.merge_repair"
	subject := fmt.Sprintf("#%d", failure.Item)
	exists, err := a.Store.HasEvent(ctx, kind, subject, failure.SHA)
	if err != nil {
		return nil, err
	}
	if exists {
		return &RaiseResult{NoOp: true}, nil
	}
	text := fmt.Sprintf("%s #%d: change request #%d carries ACCEPT at %s but cannot merge (%s). Rework the branch onto the current default head, or defer the item?",
		projectKey, failure.Item, failure.Number, failure.SHA, reason)

	dir := worktreeRoot
	if dir == "" {
		dir = os.TempDir()
	} else {
		dir = filepath.Join(dir, "briefs")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("raise merge repair: brief dir: %w", err)
	}
	sum := sha256.Sum256([]byte(text))
	path := filepath.Join(dir, fmt.Sprintf("merge-repair-%s-%d-%s.md", slug(projectKey), failure.Item, hex.EncodeToString(sum[:4])))
	body := fmt.Sprintf("# Merge repair: %s #%d\n\nChange request #%d carries ACCEPT at %s but the forge reports it cannot merge (%s).\n\n- Branch: %s\n\nTwo options:\n\n1. Rework: rebase %s onto the default branch and resolve the conflicts; the next tick merges it when the head is accepted and green.\n2. Defer: park the item (onhold) and close this row; the branch stays as it is.\n\nReply with the row's answer prefix and your choice.\n",
		projectKey, failure.Item, failure.Number, failure.SHA, reason, failure.Branch, failure.Branch)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return nil, fmt.Errorf("raise merge repair: write brief: %w", err)
	}
	row, err := a.Rig.QueueCreate(ctx, rig.CreateRequest{
		Destination: "human@kernel",
		Summary:     summary,
		BodyFile:    path,
		EvidenceRef: path,
		Source:      "human@kernel",
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
