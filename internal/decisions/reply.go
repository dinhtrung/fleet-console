package decisions

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dinhtrung/fleet-console/internal/forge"
)

// The reply side of the decision conversation. T1 posted the question as one comment on the item;
// this reads the operator's answer from the same thread and resolves the row through the same call
// the board's answer uses (design D5), so the two surfaces can never disagree.

// Reply is one comment that answered a decision.
type Reply struct {
	// DecisionID is the qitem the comment answered.
	DecisionID string
	// Answer is the text after the prefix, trimmed and capped at MaxAnswer.
	Answer string
	// CommentID is the forge's id for the comment. It is the idempotency key: a comment read twice
	// resolves nothing the second time, and a correction is posted at most once per id (design D7).
	CommentID string
	// Author is the login that wrote the comment, as the forge reported it.
	Author string
}

// Note is one decision the app has already asked about: a row whose `decision.noted` event exists.
type Note struct {
	Decision Decision
	// AskedAt is when the question note was posted. A comment older than this cannot answer it: the
	// item's earlier conversation is not this decision's.
	AskedAt time.Time
}

// ReplyReader reads answers off the items the app asked on. A nil reader reads nothing.
type ReplyReader struct {
	Forge ForgeNotes
	Store Store
	// Resolver is the same call the board's answer uses. A nil Resolver reads replies and resolves
	// nothing, which is what a dry run wants.
	Resolver Resolver
	Logf     func(format string, args ...any)
	Now      func() time.Time
}

func (r *ReplyReader) logf(format string, args ...any) {
	if r.Logf != nil {
		r.Logf(format, args...)
	}
}

// ForgeNotes is the read and the two writes the reply path needs: the item's conversation, the
// closing note on it, and the row's resolution.
type ForgeNotes interface {
	Notes(ctx context.Context, number int) ([]forge.Comment, error)
	Comment(ctx context.Context, number int, body string) error
}

// Resolver resolves a row, exactly as the board's POST /api/decisions/:id/answer does. It is the
// same interface act.Actions.ResolveRow uses, so whichever surface answers first wins and the other
// sees a terminal row.
type Resolver interface {
	ResolveRow(ctx context.Context, id, decision string) error
}

// ReplyOutcome is what one decision's reply read did.
type ReplyOutcome struct {
	DecisionID string
	Item       int
	Resolved   bool   // the row was resolved this call
	Corrected  bool   // a correction note was posted this call
	Skipped    bool   // nothing to do
	Reason     string // why it was skipped or what failed
	CommentID  string // the comment that answered or earned the correction
}

// AllowPrefix is the exact line prefix a comment must begin with to answer a decision. T1's ReplyLine
// renders the operator's line as "fleet: accept", so the prefix is the marker, not the whole line.
const AllowPrefix = "fleet:"

// ReplyAnswer extracts the answer from a comment's first non-empty line. It returns false when the
// line does not begin with AllowPrefix, which means the comment is ordinary conversation and must
// resolve nothing (design D7, first case).
func ReplyAnswer(body string) (string, bool) {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// The first non-empty line decides: a prefix further down a comment is a reply to something
		// else, or a quote of the instructions.
		if !strings.HasPrefix(line, AllowPrefix) {
			return "", false
		}
		answer := strings.TrimSpace(strings.TrimPrefix(line, AllowPrefix))
		// "fleet: accept qitem-abc" — the id names which decision when more than one is open.
		return answer, true
	}
	return "", false
}

// allowedAuthor reports whether a comment's author may answer. An empty allowlist means the project's
// repository owner, which the caller resolves; an empty author is never allowed, because a forge that
// did not report one cannot be trusted to have reported the right one.
func allowedAuthor(author string, allowlist []string) bool {
	author = strings.TrimSpace(author)
	if author == "" {
		return false
	}
	for _, allowed := range allowlist {
		if strings.EqualFold(strings.TrimSpace(allowed), author) {
			return true
		}
	}
	return false
}

// CapAnswer trims an answer and caps it at MaxAnswer characters. A decision is one line, not a
// discussion; the cap keeps the closing note and the event readable.
func CapAnswer(answer string) string {
	answer = strings.TrimSpace(answer)
	runes := []rune(answer)
	if len(runes) <= MaxAnswer {
		return answer
	}
	return strings.TrimSpace(string(runes[:MaxAnswer]))
}

// PickReply chooses the comment that answers a decision, if one does. candidates are the open
// decisions on this item; the comment resolves one only when its author is allowed, it was created
// after the question note, and its first non-empty line begins with the prefix.
//
// Ambiguity is real: two open decisions on one item and a reply naming neither cannot be applied.
// This returns the matching notes and lets the caller decide, because "which one" needs the note's
// own text and the caller has it.
func PickReply(comment forge.Comment, notes []Note, allowlist []string) (Reply, bool) {
	answer, ok := ReplyAnswer(comment.Body)
	if !ok {
		return Reply{}, false
	}
	if !allowedAuthor(comment.Author, allowlist) {
		return Reply{}, false
	}
	createdAt, err := time.Parse(time.RFC3339, strings.TrimSpace(comment.CreatedAt))
	if err != nil {
		// An unparseable timestamp cannot prove "after the question", so it cannot answer one.
		return Reply{}, false
	}
	answer = CapAnswer(answer)

	// An answer naming a decision id answers that one, wherever it sits in the list. A name that
	// matches nothing open must resolve nothing rather than fall through to the unnamed path: the
	// operator tried to answer a specific decision and got its id wrong, so guessing another one
	// would answer the wrong question.
	if named := namedTokens(answer, notes); len(named) > 0 {
		for _, note := range notes {
			if note.Decision.ID != named[0] {
				continue
			}
			if !createdAt.After(note.AskedAt) {
				return Reply{}, false
			}
			return Reply{
				DecisionID: note.Decision.ID, Answer: answer, CommentID: comment.ID, Author: comment.Author,
			}, true
		}
		return Reply{}, false
	}
	// A token that looks like a decision id but matches none open: treat as named-and-unknown.
	if looksLikeDecisionID(answer) {
		return Reply{}, false
	}

	// An unnamed answer resolves only when exactly one decision on the item could take it. With two
	// open, "fleet: accept" does not say which, so the caller posts one correction instead.
	eligible := make([]Note, 0, len(notes))
	for _, note := range notes {
		if createdAt.After(note.AskedAt) {
			eligible = append(eligible, note)
		}
	}
	if len(eligible) != 1 {
		return Reply{}, false
	}
	note := eligible[0]
	return Reply{
		DecisionID: note.Decision.ID, Answer: answer, CommentID: comment.ID, Author: comment.Author,
	}, true
}

// namedTokens returns the open decision ids the answer names, whole-token. "accept qitem-abc" names
// it; "accept qitem-abcdef" does not, because the token is compared whole rather than as a prefix.
func namedTokens(answer string, notes []Note) []string {
	named := make([]string, 0, 1)
	for _, note := range notes {
		id := strings.TrimSpace(note.Decision.ID)
		if id == "" {
			continue
		}
		for _, token := range strings.Fields(answer) {
			if token == id {
				named = append(named, id)
				break
			}
		}
	}
	return named
}

// looksLikeDecisionID reports whether an answer carries a token shaped like a queue item id. The
// operator naming a decision the app cannot find is a mistake to surface, not an invitation to
// answer a different one.
func looksLikeDecisionID(answer string) bool {
	for _, token := range strings.Fields(answer) {
		if strings.HasPrefix(token, "qitem-") {
			return true
		}
	}
	return false
}

// Ambiguous reports the open decision ids a prefixed but unnamed answer could have answered. A caller
// posts one correction listing them when this returns more than one id (design D7, third case).
func Ambiguous(comment forge.Comment, notes []Note, allowlist []string) []string {
	if _, ok := ReplyAnswer(comment.Body); !ok {
		return nil
	}
	if !allowedAuthor(comment.Author, allowlist) {
		return nil
	}
	createdAt, err := time.Parse(time.RFC3339, strings.TrimSpace(comment.CreatedAt))
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(notes))
	for _, note := range notes {
		if createdAt.After(note.AskedAt) {
			ids = append(ids, note.Decision.ID)
		}
	}
	if len(ids) < 2 {
		return nil
	}
	sort.Strings(ids)
	return ids
}

// ClosingNote renders the comment posted after a reply resolved a decision. It names the answer and
// the login that gave it, and links the comment it came from, so the item's thread shows its own
// history without a reader consulting the board.
func ClosingNote(reply Reply, commentURL string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "answered: %s — %s\n", reply.Answer, reply.Author)
	if strings.TrimSpace(commentURL) != "" {
		fmt.Fprintf(&b, "\nfrom %s", commentURL)
	}
	return b.String()
}

// Read resolves decisions from the replies on their items. notes are the decisions this app has
// already asked about; allowlist is the logins whose comments may answer (empty means the caller
// could not resolve an owner, and then nothing is answered).
//
// Three cases per design D7: a comment that is not a reply is silence; a reply that cannot be
// applied earns one correction per comment id; a valid reply resolves the row through the same call
// the board uses. A row already terminal is never resolved again — that is what makes "the board
// answered first" safe.
//
// A correction is keyed by comment id in the event log, so a persistent typo cannot become a loop.
func (r *ReplyReader) Read(ctx context.Context, notes []Note, allowlist []string) []ReplyOutcome {
	out := make([]ReplyOutcome, 0, len(notes))
	if r == nil || r.Forge == nil || r.Store == nil {
		return out
	}
	// One forge read per item, not per decision: two open decisions on one item share a thread.
	byItem := make(map[int][]Note, len(notes))
	for _, note := range notes {
		if note.Decision.Item > 0 {
			byItem[note.Decision.Item] = append(byItem[note.Decision.Item], note)
		}
	}
	items := make([]int, 0, len(byItem))
	for item := range byItem {
		items = append(items, item)
	}
	sort.Ints(items)

	for _, item := range items {
		comments, err := r.Forge.Notes(ctx, item)
		if err != nil {
			out = append(out, ReplyOutcome{Item: item, Skipped: true, Reason: fmt.Sprintf("read notes: %v", err)})
			continue
		}
		open := byItem[item]
		for _, comment := range comments {
			if _, ok := ReplyAnswer(comment.Body); !ok {
				continue
			}
			reply, ok := PickReply(comment, open, allowlist)
			if !ok {
				// A prefixed comment that resolves nothing: ambiguous, wrong author, or older than
				// the question. Only the ambiguous case earns a correction; the others are silence.
				if ids := Ambiguous(comment, open, allowlist); len(ids) > 1 {
					out = append(out, r.correct(ctx, item, comment, ids))
				}
				continue
			}
			out = append(out, r.resolve(ctx, item, reply))
		}
	}
	return out
}

// resolve applies one valid reply: resolve the row, post the closing note, record decision.answered.
func (r *ReplyReader) resolve(ctx context.Context, item int, reply Reply) ReplyOutcome {
	out := ReplyOutcome{DecisionID: reply.DecisionID, Item: item, CommentID: reply.CommentID}
	if r.Resolver == nil {
		out.Skipped, out.Reason = true, "no resolver configured"
		return out
	}
	// The comment id is the idempotency key for a resolution too: a comment read twice resolves once.
	done, err := r.Store.HasEvent(ctx, "decision.answered", subjectOf(item), reply.CommentID)
	if err != nil {
		out.Reason = fmt.Sprintf("ledger read: %v", err)
		return out
	}
	if done {
		out.Skipped, out.Reason = true, "already answered"
		return out
	}
	if err := r.Resolver.ResolveRow(ctx, reply.DecisionID, reply.Answer); err != nil {
		// A terminal row is not an error to retry: the board answered first (design D5). The event
		// records the late comment so the operator can see why their reply changed nothing.
		detail := fmt.Sprintf("%s comment %s: %v", reply.DecisionID, reply.CommentID, err)
		// Once per comment id (2.3): a reply whose resolve keeps failing must not append a second
		// event on the next tick. The event is the ledger, so it is the thing to consult.
		seen, seenErr := r.Store.HasEvent(ctx, "decision.reply_ignored", subjectOf(item), reply.CommentID)
		if seenErr != nil {
			r.logf("decision.reply_ignored %s: ledger read: %v", reply.DecisionID, seenErr)
		} else if !seen {
			if _, appErr := r.Store.AppendEvent(ctx, Event{
				Actor: "act", Kind: "decision.reply_ignored", Subject: subjectOf(item), Detail: detail,
			}); appErr != nil {
				r.logf("decision.reply_ignored %s: %v", reply.DecisionID, appErr)
			}
		}
		out.Skipped, out.Reason = true, err.Error()
		return out
	}
	out.Resolved = true
	// The closing note is best-effort after the row is resolved: the resolution already happened and
	// is the load-bearing write. A failed note is one log line, and the event still records the answer.
	if err := r.Forge.Comment(ctx, item, ClosingNote(reply, "")); err != nil {
		r.logf("decision.closing_note %s: %v", reply.DecisionID, err)
	}
	if _, err := r.Store.AppendEvent(ctx, Event{
		Actor: "act", Kind: "decision.answered", Subject: subjectOf(item),
		Detail: fmt.Sprintf("%s answered %q by %s comment %s", reply.DecisionID, reply.Answer, reply.Author, reply.CommentID),
	}); err != nil {
		out.Reason = fmt.Sprintf("audit: %v", err)
		return out
	}
	return out
}

// correct posts one correction listing the open ids, once per comment id.
func (r *ReplyReader) correct(ctx context.Context, item int, comment forge.Comment, ids []string) ReplyOutcome {
	out := ReplyOutcome{Item: item, CommentID: comment.ID, Corrected: true}
	seen, err := r.Store.HasEvent(ctx, "decision.reply_ignored", subjectOf(item), comment.ID)
	if err != nil {
		out.Corrected, out.Reason = false, fmt.Sprintf("ledger read: %v", err)
		return out
	}
	if seen {
		out.Corrected, out.Skipped, out.Reason = false, true, "correction already posted"
		return out
	}
	body := "That reply does not name which decision it answers. Open decisions on this item:\n\n"
	for _, id := range ids {
		body += fmt.Sprintf("- %s\n", id)
	}
	body += "\nReply again with the id: `fleet: accept " + ids[0] + "`\n"
	if err := r.Forge.Comment(ctx, item, body); err != nil {
		out.Corrected, out.Reason = false, err.Error()
		return out
	}
	if _, err := r.Store.AppendEvent(ctx, Event{
		Actor: "act", Kind: "decision.reply_ignored", Subject: subjectOf(item),
		Detail: fmt.Sprintf("%s comment %s: ambiguous, named %s", comment.ID, comment.ID, strings.Join(ids, ", ")),
	}); err != nil {
		out.Reason = fmt.Sprintf("audit: %v", err)
	}
	return out
}
