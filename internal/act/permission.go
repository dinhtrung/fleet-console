package act

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
)

// EventPermissionDenied is the audit kind one refused forge write dedupes on:
// the ledger, not a table of its own (#260).
const EventPermissionDenied = "forge.permission_denied"

// PermissionRefusal is the shape of one forge write the credential may not
// perform, named the way the forge named it (#258).
type PermissionRefusal struct {
	Item       int
	Title      string
	Operation  string // comment | ready | merge | close
	Forge      string // github / gitlab
	Detail     string // the forge's own sentence
	CR         int    // the change request number, when the write had one
	SHA        string // the head the write judged, when there was one
	Attempt    int    // the dispatch attempt the write rode; 0 when there is none
	ProjectKey string
}

// PermissionRaise is the evidence of one raised permission question: the row
// id and the wording the notifier sends. NoOp marks the deduped case, where a
// question for this failure already exists.
type PermissionRaise struct {
	RaiseResult
	Question string
}

// PermissionRefused is what a refused forge write raises to its caller (#260).
// It carries the journal line the tick prints — `#<n> permission: <operation>:
// <detail>`, not an anonymous adapter error — and, when the row is new, the
// raise the phone hook needs. Refusals are not retries: a permission failure
// never ends a dispatch, so the next tick retries by itself once the token is
// fixed, and the plan never sees the refusal as progress.
type PermissionRefused struct {
	Item      int
	Operation string
	Journal   string
	Question  string
	// Raise is non-nil when RaisePermissionDecision ran, dedupe included. A
	// nil Raise means the raise itself failed; the journal line says so and
	// the next tick retries it.
	Raise *PermissionRaise
}

func (e *PermissionRefused) Error() string { return e.Journal }

// PermissionInput names one refused write for the operator question. Title is
// optional: the raise fills it from the store when the caller has no snapshot
// at hand.
type PermissionInput struct {
	Item       int
	Title      string
	Operation  string
	Refusal    *forge.PermissionError
	Attempt    int // the dispatch attempt the write rode; 0 when there is none
	CR         int
	SHA        string
	ProjectKey string
}

// RaisePermissionDecision asks the operator to fix the forge token (#260): one
// queue row to the human, once per (item, attempt, operation, detail). The
// dedupe is the event ledger — the same once-per-fact shape every raise uses,
// so a refusal that repeats every tick stays one question, and a different
// operation or a new attempt raises one new question. The brief carries the
// forge's own sentence, the change request and head when the write had one,
// the fix for either token kind, and the sentence that says the fleet retries
// by itself on the next tick after the token is fixed.
func (a *Actions) RaisePermissionDecision(ctx context.Context, in PermissionInput) (*PermissionRaise, error) {
	if a.Store == nil || a.Rig == nil {
		return nil, fmt.Errorf("raise permission decision: store and rig are required")
	}
	if in.Refusal == nil {
		return nil, fmt.Errorf("raise permission decision: no refusal given")
	}
	operation := in.Operation
	if operation == "" {
		operation = in.Refusal.Operation
	}
	if operation == "" {
		operation = "write"
	}
	key := fmt.Sprintf("attempt=%d operation=%s detail=%s", in.Attempt, operation, firstLine(in.Refusal.Detail))
	subject := fmt.Sprintf("#%d", in.Item)
	exists, err := a.Store.HasEvent(ctx, EventPermissionDenied, subject, key)
	if err != nil {
		return nil, err
	}
	if exists {
		return &PermissionRaise{RaiseResult: RaiseResult{NoOp: true}}, nil
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
	detail := in.Refusal.Detail
	if detail == "" {
		detail = in.Refusal.Error()
	}
	question := fmt.Sprintf("%s #%d (%s): the forge refused %s: %s", projectKey, in.Item, title, operation, detail)

	dir := a.WorktreeRoot
	if dir == "" {
		dir = os.TempDir()
	} else {
		dir = filepath.Join(dir, "briefs")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("raise permission decision: brief dir: %w", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("permission-%s-%d-%s.md", slug(key), in.Item, operation))
	// The fix names the one permission the refused operation needs (#329):
	// "grant Issues: write and Pull requests: write" on a refused merge sent the
	// operator to grant, wait, and hit the same 403 on mergePullRequest, which
	// GitHub files under Contents. An operation the table does not know keeps
	// the generic sentence rather than a guess.
	grant := forge.FixPermission(operation)
	if grant == "" {
		grant = "the permissions the operation needs"
	}
	body := fmt.Sprintf("# Permission refused: %s #%d %s\n\nThe forge refused a write the fleet's token may not perform (#258). Retrying cannot succeed until the token can do it.\n\n- Operation: %s\n- Forge: %s\n%s- The forge's own sentence: %s\n\n## Fix\n\n- Classic PAT: gh auth refresh -s repo\n- Fine-grained token: grant it %q on this repository\n- GitLab: the token needs \"api\" with write access\n\nThe fleet retries by itself on the next tick after the token is fixed; nothing else needs doing.\n",
		projectKey, in.Item, title, operation, in.Refusal.Forge, changeRequestLine(in.CR, in.SHA), detail, grant)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return nil, fmt.Errorf("raise permission decision: write brief: %w", err)
	}
	row, err := a.Rig.QueueCreate(ctx, rig.CreateRequest{
		Destination: "human@kernel",
		Summary:     fmt.Sprintf("#%d: forge refused %s (permission denied)", in.Item, operation),
		BodyFile:    path,
		EvidenceRef: path,
		Source:      "human@kernel",
	})
	if err != nil {
		_ = noteRefusal(ctx, a.Store, in.Item, err)
		return nil, err
	}
	eventID, err := a.event(ctx, "act", EventPermissionDenied, subject, key+"; row "+row.ID)
	if err != nil {
		return nil, err
	}
	return &PermissionRaise{
		RaiseResult: RaiseResult{QItemID: row.ID, Events: []int64{eventID}},
		Question:    question,
	}, nil
}

// changeRequestLine is the brief's change-request line, empty when the refused
// write was not about one.
func changeRequestLine(cr int, sha string) string {
	if cr == 0 && sha == "" {
		return ""
	}
	return fmt.Sprintf("- Change request: #%d at %s\n", cr, sha)
}

// RefusePermission checks one failed forge write; when the credential caused
// the refusal it raises the operator question and returns the refusal for the
// caller to journal. A nil return means the error is not a permission refusal
// and the caller handles it as before.
//
// attempt is the dispatch attempt the write rode (0 when there is none); it is
// part of the dedupe key, so the same refusal at a new attempt raises one new
// question. The title is best-effort: the raise reads the item from the store
// when the caller has none.
func (a *Actions) RefusePermission(ctx context.Context, item int, title, operation string, err error, attempt, cr int, sha string) *PermissionRefused {
	if err == nil || !forge.IsPermission(err) {
		return nil
	}
	var refusal *forge.PermissionError
	if !errors.As(err, &refusal) {
		refusal = &forge.PermissionError{Operation: operation, Detail: err.Error()}
	}
	op := operation
	if op == "" {
		op = refusal.Operation
	}
	raise, raiseErr := a.RaisePermissionDecision(ctx, PermissionInput{
		Item: item, Title: title, Operation: op, Refusal: refusal,
		Attempt: attempt, CR: cr, SHA: sha, ProjectKey: a.ProjectKey,
	})
	refused := &PermissionRefused{Item: item, Operation: op}
	detail := firstLine(refusal.Detail)
	if raiseErr != nil {
		// The refusal is still journaled under its own name; the raise failed
		// and the next tick retries it — the ledger holds nothing yet.
		refused.Journal = fmt.Sprintf("#%d permission: %s: %s (row not raised: %v)", item, op, detail, raiseErr)
		return refused
	}
	refused.Raise = raise
	refused.Question = raise.Question
	refused.Journal = fmt.Sprintf("#%d permission: %s: %s", item, op, detail)
	return refused
}

// notePermission records one permission refusal: the journal line plus, when
// the question is new, the phone hook every decision rides. It reports whether
// the caller should stop handling the write error itself.
func notePermission(in TickInput, report *TickReport, refused *PermissionRefused) bool {
	if refused == nil {
		return false
	}
	report.Skipped = append(report.Skipped, refused.Journal)
	if refused.Raise != nil && !refused.Raise.NoOp && in.OnDecision != nil {
		in.OnDecision(DecisionRaised{QItemID: refused.Raise.QItemID, Item: refused.Item, Text: refused.Question})
	}
	return true
}
