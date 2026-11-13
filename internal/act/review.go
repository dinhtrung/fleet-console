package act

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
// carries the head, so a moved head raises exactly one new decision. The brief
// carries the facts to execute (#265): the change request number and the head
// sha verbatim, the literal command that posts the verdict note with the sha
// substituted, and the row-answer fallback. The facts ride the intent (#265);
// nothing here recomputes them.
func (a *Actions) RaiseDecision(ctx context.Context, project store.ProjectRow, item int, text, kind string, changeRequest int, headSHA, worktreeRoot string) (*RaiseResult, error) {
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
	sha := strings.ToLower(headSHA)
	sum := sha256.Sum256([]byte(text))
	path := filepath.Join(dir, fmt.Sprintf("review-%s-%d-%s.md", slug(project.Key), item, hex.EncodeToString(sum[:4])))
	facts := ""
	if changeRequest > 0 || sha != "" {
		facts = fmt.Sprintf("\nChange request: #%d\nHead: %s", changeRequest, sha)
	}
	command := ""
	if changeRequest > 0 && sha != "" {
		command = fmt.Sprintf("\nPost the verdict note with:\n\n    %s\n\nTo reject instead, the same command with `REJECT %s`.\nIf the command cannot run, answer on the row with `ACCEPT %s`.\n",
			verdictCommand(project, changeRequest, sha), sha, sha)
	}
	body := fmt.Sprintf("# %s #%d - re-review\n\nRig: %s\n\n%s\n%s%s",
		project.Key, item, rigName, text, facts, command)
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

// verdictCommand is the one command that posts a verdict note on the project's
// forge, with the sha already substituted — executable by a weaker model
// instead of a formatting rule it can fail (#265, design D5).
func verdictCommand(project store.ProjectRow, changeRequest int, sha string) string {
	if strings.Contains(strings.ToLower(project.ForgeKind), "gitlab") {
		return fmt.Sprintf("glab api -X POST 'projects/%s/merge_requests/%d/notes' -F 'body=ACCEPT %s'",
			project.ForgeProject, changeRequest, sha)
	}
	return fmt.Sprintf("gh pr comment %d -R %s --body 'ACCEPT %s'",
		changeRequest, project.ForgeProject, sha)
}

// RaiseOperatorQuestion creates one queue row for the operator: a decision
// only a human can make, such as a change request with no verdict (#265,
// design D3). The destination is resolved by the caller (verdict-relay,
// design D4): the project's verdict_relay.seat when its relay mode is
// operator, human@kernel otherwise — operatorDestination is that one
// resolution. The shape is raiseAdoptionDecision's — a brief under briefs/,
// then the operator row — and the event ledger dedupes per (item, kind,
// head): the key carries a terminator, so head=aaaa111 never matches a longer
// sha's event, and a moved head is a new fact that asks again.
func (a *Actions) RaiseOperatorQuestion(ctx context.Context, project store.ProjectRow, item int, question, kind, headSHA, worktreeRoot, destination string) (*RaiseResult, error) {
	if a.Store == nil || a.Rig == nil {
		return nil, fmt.Errorf("raise operator question: store and rig are required")
	}
	subject := fmt.Sprintf("#%d", item)
	sha := strings.ToLower(headSHA)
	key := fmt.Sprintf("head=%s;", sha)
	exists, err := a.Store.HasEvent(ctx, kind, subject, key)
	if err != nil {
		return nil, err
	}
	if exists {
		return &RaiseResult{NoOp: true}, nil
	}

	dir := worktreeRoot
	if dir == "" {
		dir = os.TempDir()
	} else {
		dir = filepath.Join(dir, "briefs")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("raise operator question: brief dir: %w", err)
	}
	sum := sha256.Sum256([]byte(question + sha))
	path := filepath.Join(dir, fmt.Sprintf("operator-%s-%d-%s.md", slug(project.Key), item, hex.EncodeToString(sum[:4])))
	body := fmt.Sprintf("# %s #%d - operator question\n\n%s\n\nHead under review: %s\nThe two answers that already work: Approve/Reject on the item drawer (POST /api/items/%d/verdict), or reply fleet: accept / fleet: reject on the item. Either writes the note on the change request, which stays the only verdict record — a row answer is never read as one.\n",
		project.Key, item, question, sha, item)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return nil, fmt.Errorf("raise operator question: write brief: %w", err)
	}
	summary := question
	if len(summary) > 120 {
		summary = summary[:120]
	}
	row, err := a.Rig.QueueCreate(ctx, rig.CreateRequest{
		Destination: destination, Summary: summary, BodyFile: path, Source: "human@kernel", EvidenceRef: path,
	})
	if err != nil {
		_ = noteRefusal(ctx, a.Store, item, err)
		return nil, err
	}
	detail := fmt.Sprintf("%s; head=%s; row %s", firstLine(question), sha, row.ID)
	eventID, err := a.event(ctx, "act", kind, subject, detail)
	if err != nil {
		return nil, err
	}
	return &RaiseResult{QItemID: row.ID, Events: []int64{eventID}}, nil
}
