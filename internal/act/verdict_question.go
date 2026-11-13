package act

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dinhtrung/fleet-console/internal/rig"
)

// VerdictQuestion is a note the merge gate could not turn into a fact: it reads
// like a verdict but names no commit, names both, negates itself, or answers
// about a commit that is no longer the head.
type VerdictQuestion struct {
	NoteID   string
	Head     string // the head under review
	Written  string // the sha the note named, when it named one
	Decision string // "accept", "reject", or "" when the note stated neither
	Reason   string
	Excerpt  string
}

// RaiseVerdictQuestion asks the operator seat to decide about such a note. The
// seat is the one place that can answer for a note the parser cannot read: it
// reads the change request, then posts the verdict as a note — the same note
// the gate reads on the next tick.
//
// The alternatives were both wrong: silence (the merge waits for ever) and a
// guess (a note saying "I cannot accept this" merged as an accept). The seat is
// resolved by operatorDestination, so a project that never opted into a relay
// keeps human@kernel.
//
// One row per (item, note, head): the note id and the head are the key, so a
// tick repeating every minute cannot flood the queue, while a new note or a
// moved head is a new question.
func (a *Actions) RaiseVerdictQuestion(ctx context.Context, projectKey string, item, changeRequest int, question VerdictQuestion, destination, worktreeRoot string) (*RaiseResult, error) {
	if a.Store == nil || a.Rig == nil {
		return nil, fmt.Errorf("raise verdict question: store and rig are required")
	}
	if destination == "" {
		destination = "human@kernel"
	}
	kind := "decision.verdict_question"
	subject := fmt.Sprintf("#%d", item)
	key := fmt.Sprintf("note=%s; head=%s;", question.NoteID, question.Head)
	exists, err := a.Store.HasEvent(ctx, kind, subject, key)
	if err != nil {
		return nil, err
	}
	if exists {
		return &RaiseResult{NoOp: true}, nil
	}

	head := question.Head
	if head == "" {
		head = "unknown"
	}
	summary := fmt.Sprintf("#%d: the merge gate needs a verdict (note %s)", item, question.NoteID)
	// The key rides in the text: HasEvent matches on it, and that is what stops
	// a tick repeating every minute from asking the same question again.
	text := fmt.Sprintf("%s #%d: note %s on change request #%d cannot be read as a verdict (%s); asked %s; %s",
		projectKey, item, question.NoteID, changeRequest, question.Reason, destination, key)

	dir := worktreeRoot
	if dir == "" {
		dir = os.TempDir()
	} else {
		dir = filepath.Join(dir, "briefs")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("raise verdict question: brief dir: %w", err)
	}
	sum := sha256.Sum256([]byte(key))
	path := filepath.Join(dir, fmt.Sprintf("verdict-question-%s-%d-%s.md", slug(projectKey), item, hex.EncodeToString(sum[:4])))

	written := question.Written
	if written == "" {
		written = "none"
	}
	body := fmt.Sprintf(`# Verdict question: %s #%d

Change request #%d carries a note the merge gate could not read as a verdict for head %s.

- Note: %s
- Verdict word in it: %s
- Sha the note names: %s
- Why the gate stopped: %s
- The line it read:

~~~
%s
~~~

Read the change request, then decide. Post the answer as a note on the change
request — the gate captures it on its next tick and acts on it:

1. Post "ACCEPT %s" — the gate merges the request.
2. Post "REJECT %s" — the gate stops the merge and the builder reworks.
3. Send the request back for review (a new head) if the note is about an older
   commit, or close the request by hand if the work is not wanted.

The gate never writes a verdict for you and never merges on a guess.
`,
		projectKey, item, changeRequest, head,
		question.NoteID, orNone(question.Decision), written, question.Reason,
		question.Excerpt, head, head)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return nil, fmt.Errorf("raise verdict question: write brief: %w", err)
	}
	row, err := a.Rig.QueueCreate(ctx, rig.CreateRequest{
		Destination: destination,
		Summary:     summary,
		BodyFile:    path,
		EvidenceRef: path,
		Source:      destination,
	})
	if err != nil {
		// A seat that does not exist must not swallow the question: fall back
		// to the human queue once, and let the skip line name the seat.
		if destination != "human@kernel" {
			if fallback, fallbackErr := a.Rig.QueueCreate(ctx, rig.CreateRequest{
				Destination: "human@kernel",
				Summary:     summary,
				BodyFile:    path,
				EvidenceRef: path,
				Source:      "human@kernel",
			}); fallbackErr == nil {
				row, err = fallback, nil
			}
		}
		if err != nil {
			_ = noteRefusal(ctx, a.Store, item, err)
			return nil, err
		}
	}
	eventID, err := a.event(ctx, "act", kind, subject, text+"; row "+row.ID)
	if err != nil {
		return nil, err
	}
	return &RaiseResult{QItemID: row.ID, Events: []int64{eventID}}, nil
}

// orNone spells an absent value for the brief.
func orNone(value string) string {
	if value == "" {
		return "none"
	}
	return value
}

// questionFromError turns a capture error into the question the operator seat
// answers. Every error the capture can raise is a dead end the gate cannot
// resolve itself: an ambiguous note, a malformed verdict line, or a verdict
// about a commit that is no longer the head.
func questionFromError(err error, head string) (VerdictQuestion, bool) {
	var unresolved *UnresolvedVerdictError
	if errors.As(err, &unresolved) {
		return VerdictQuestion{
			NoteID: unresolved.NoteID, Head: head,
			Reason: unresolved.Reason, Excerpt: unresolved.Excerpt,
		}, true
	}
	var accepted *UnattributedAcceptError
	if errors.As(err, &accepted) {
		return VerdictQuestion{
			NoteID: accepted.NoteID, Head: head, Written: accepted.Written, Decision: "accept",
			Reason: "the note accepts a commit that is not the head under review", Excerpt: err.Error(),
		}, true
	}
	var rejected *UnattributedRejectError
	if errors.As(err, &rejected) {
		return VerdictQuestion{
			NoteID: rejected.NoteID, Head: head, Written: rejected.Written, Decision: "reject",
			Reason: "the note rejects a commit that is not the head under review", Excerpt: err.Error(),
		}, true
	}
	var malformed *UnattributableVerdictError
	if errors.As(err, &malformed) {
		return VerdictQuestion{
			NoteID: malformed.NoteID, Head: head,
			Reason: malformed.Reason, Excerpt: malformed.Line,
		}, true
	}
	return VerdictQuestion{}, false
}
