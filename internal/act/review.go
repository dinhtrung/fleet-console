package act

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dinhtrung/fleet-console/internal/forge"
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

// RaiseMalformedVerdict answers the plan's decision.malformed_verdict ask
// (epic #345, design D2): the reviewer posted a note that parses as a verdict
// but names no sha, and only it can write the one-line repair. The text rides
// the intent with the literal already substituted — note id, line as written,
// head — so the brief and the change-request comment carry the same words.
// The ledger row is the dedupe: the text names the note and the head, so it
// is once per (note, head) and a moved head asks again. The same text is
// posted on the change request as one comment, so the artifact carries the
// instruction and the seat's row points at it.
func (a *Actions) RaiseMalformedVerdict(ctx context.Context, project store.ProjectRow, item int, text string, changeRequest int, worktreeRoot string) (*RaiseResult, error) {
	if a.Store == nil || a.Rig == nil {
		return nil, fmt.Errorf("raise malformed verdict: store and rig are required")
	}
	kind := "decision.malformed_verdict"
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
		return nil, fmt.Errorf("raise malformed verdict: brief dir: %w", err)
	}
	sum := sha256.Sum256([]byte(text))
	path := filepath.Join(dir, fmt.Sprintf("malformed-verdict-%s-%d-%s.md", slug(project.Key), item, hex.EncodeToString(sum[:4])))
	facts := ""
	if changeRequest > 0 {
		facts = fmt.Sprintf("\nChange request: #%d\n", changeRequest)
	}
	body := fmt.Sprintf("# %s #%d - your verdict note named no commit\n\nRig: %s\n\n%s\n%s\nThe comment above is also posted on the change request, so the thread carries the instruction.\n",
		project.Key, item, rigName, text, facts)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return nil, fmt.Errorf("raise malformed verdict: write brief: %w", err)
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
	if changeRequest > 0 {
		if err := a.Forge.Comment(ctx, changeRequest, text); err != nil {
			return nil, fmt.Errorf("raise malformed verdict: comment on change request #%d: %w", changeRequest, err)
		}
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

// noItemQuestion is the one question a change request that closes no item
// raises (#368). The two cases ask different things because only one of them
// has a body edit that could work: an open item can be closed by the body, a
// closed one cannot — the gate needs an open item, and adoption additionally
// requires the branch to be feature/<item>- (merge.go).
func noItemQuestion(projectKey string, cr forge.ChangeRequest, named int, namedOpen bool) string {
	head := cr.HeadSHA
	if head == "" {
		head = "unknown"
	}
	if namedOpen {
		return fmt.Sprintf("%s change request #%d closes no item: it names #%d, which is open. The gate merges only on \"Closes #%d\" in the body. If this change request is #%d's work, add \"Closes #%d\" to the body (head %s); if the work is not #%d's, open a follow-up item and re-home the branch to feature/<new item>- so the gate can adopt it.",
			projectKey, cr.Number, named, named, named, named, head, named)
	}
	return fmt.Sprintf("%s change request #%d closes no item: it names #%d, which is not open. Adding \"Closes #%d\" cannot help — the gate merges only on an open item (head %s). Merge it by hand once it is reviewed, or open a follow-up item and re-home the branch to feature/<new item>- so the gate can adopt it.",
		projectKey, cr.Number, named, named, head)
}

// RaiseNoItemQuestion asks the operator about a change request whose body
// closes no item but whose title or branch names one (#368). The change
// request cannot merge without an open item, so silence is a dead end, not an
// answer. The row carries the change request, the head sha and the named item
// with its state, and the ledger dedupes per (change request, head) — a moved
// head is a new question, and a resolved row does not silence it. It never
// associates the change request with the item: the title and the branch are
// mentions (#314), and this row only asks a human.
func (a *Actions) RaiseNoItemQuestion(ctx context.Context, project store.ProjectRow, cr forge.ChangeRequest, named int, namedOpen bool, worktreeRoot, destination string) (*RaiseResult, error) {
	if a.Store == nil || a.Rig == nil {
		return nil, fmt.Errorf("raise no-item question: store and rig are required")
	}
	subject := fmt.Sprintf("cr-%d", cr.Number)
	sha := strings.ToLower(cr.HeadSHA)
	key := fmt.Sprintf("cr=%d; head=%s;", cr.Number, sha)
	exists, err := a.Store.HasEvent(ctx, "decision.no_item", subject, key)
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
		return nil, fmt.Errorf("raise no-item question: brief dir: %w", err)
	}
	question := noItemQuestion(project.Key, cr, named, namedOpen)
	sum := sha256.Sum256([]byte(question))
	path := filepath.Join(dir, fmt.Sprintf("operator-%s-cr-%d-%s.md", slug(project.Key), cr.Number, hex.EncodeToString(sum[:4])))
	head := sha
	if head == "" {
		head = "unknown"
	}
	state := "not open (closed, or not in the open list this read saw)"
	if namedOpen {
		state = "open"
	}
	body := fmt.Sprintf("# %s cr-%d - change request closes no item\n\n%s\n\nChange request: #%d\nHead: %s\nNamed item: #%d (%s)\n\nA title or branch mention never associates a change request with an item (#314); this row asks a human and does not set the association. Do not expect the merge gate to take this change request: it has no lawful path.\n",
		project.Key, cr.Number, question, cr.Number, head, named, state)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return nil, fmt.Errorf("raise no-item question: write brief: %w", err)
	}
	word := "not open"
	if namedOpen {
		word = "open"
	}
	summary := fmt.Sprintf("%s change request #%d closes no item; names #%d (%s)", project.Key, cr.Number, named, word)
	row, err := a.Rig.QueueCreate(ctx, rig.CreateRequest{
		Destination: destination, Summary: summary, BodyFile: path, Source: "human@kernel", EvidenceRef: path,
	})
	if err != nil {
		_ = noteRefusalSubject(ctx, a.Store, subject, err)
		return nil, err
	}
	detail := fmt.Sprintf("cr=%d; head=%s; named=%d; state=%s; row %s", cr.Number, sha, named, strings.ReplaceAll(word, " ", "-"), row.ID)
	eventID, err := a.event(ctx, "act", "decision.no_item", subject, detail)
	if err != nil {
		return nil, err
	}
	return &RaiseResult{QItemID: row.ID, Events: []int64{eventID}}, nil
}
