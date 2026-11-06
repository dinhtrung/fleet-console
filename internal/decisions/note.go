// Package decisions asks a raised decision as one comment on its item and reads
// the operator's reply from the same thread. The item's conversation is the reply
// channel because the app already reads it every tick (design D1), the forge
// already provides an author and a timestamp the app can trust (D1), and a
// decision can exist with no change request at all (D2).
//
// This file renders and posts the question. Reading a reply is T2.
package decisions

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ReplyPrefix is the exact line prefix a comment must begin with to answer a
// decision. It is a prefix rather than "any accept/reject word" because a thread
// about a rejected change request contains both words constantly (design D3).
const ReplyPrefix = "fleet:"

// Marker is the HTML comment that carries the decision's id, so the note can be
// found again in a thread without parsing prose. Invisible when rendered.
const Marker = "<!-- fleet-console:decision %s -->"

// MaxAnswer caps an answer's length. A decision is one line, not a discussion.
const MaxAnswer = 200

// Decision is one obligation waiting on a person, as the board already models it.
// It is deliberately the fields the note and the reply both need and nothing
// more: no store row, no new table.
type Decision struct {
	ID          string // the qitem id
	Summary     string // what the row is about
	Question    string // what is being asked, as the row words it
	Options     []string
	Item        int    // the issue or change request the decision is about; 0 when unknown
	Destination string // the seat holding the question
	CreatedAt   time.Time
	Deadline    time.Time // zero when the row carries none
}

// NoteContext is what the renderer needs beyond the decision: where the item and
// the board's copy live, and how many decisions are open on that item.
type NoteContext struct {
	// ItemURL links the item's thread. Empty means "do not link it", which is
	// what a decision with no known item number gets.
	ItemURL string
	// BoardURL links the board's own copy of the decision.
	BoardURL string
	// OpenOnItem counts the decisions open on this item, including this one. When
	// more than one is open the reply line must name the id, or the operator's
	// answer would be ambiguous (design D3).
	OpenOnItem int
}

// Render returns the note body and the exact line the operator should reply with.
// Pure: no clock, no forge, no store, so the golden test pins the bytes.
func Render(d Decision, ctx NoteContext) (body, replyLine string) {
	replyLine = ReplyLine(d, ctx)

	var b strings.Builder
	question := strings.TrimSpace(d.Question)
	if question == "" {
		question = strings.TrimSpace(d.Summary)
	}
	if question == "" {
		question = "a decision is needed on this item"
	}

	fmt.Fprintf(&b, "**%s**\n\n", question)
	if len(d.Options) > 0 {
		b.WriteString("Options:\n")
		for _, option := range d.Options {
			option = strings.TrimSpace(option)
			if option == "" {
				continue
			}
			fmt.Fprintf(&b, "- %s\n", option)
		}
		b.WriteString("\n")
	}
	if !d.Deadline.IsZero() {
		// Only when the row carries one: "no deadline" in every note is noise the
		// operator learns to skip, which is how a real deadline gets missed.
		fmt.Fprintf(&b, "Deadline: %s\n\n", d.Deadline.UTC().Format(time.RFC3339))
	}
	b.WriteString("Reply with this line:\n\n")
	fmt.Fprintf(&b, "```\n%s\n```\n", replyLine)
	if ctx.ItemURL != "" {
		fmt.Fprintf(&b, "\nItem: %s\n", ctx.ItemURL)
	}
	if ctx.BoardURL != "" {
		fmt.Fprintf(&b, "Board: %s\n", ctx.BoardURL)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, Marker, d.ID)
	b.WriteString("\n")
	return b.String(), replyLine
}

// ReplyLine is the exact text an allowed author types to answer this decision.
//
// One open decision on the item needs only the prefix: `fleet: accept`. Two or
// more needs the id as well, because "accept" would not say which question it
// answers. The id goes after the answer so a human reading the thread sees the
// answer first.
func ReplyLine(d Decision, ctx NoteContext) string {
	if ctx.OpenOnItem > 1 {
		return fmt.Sprintf("%s accept %s", ReplyPrefix, d.ID)
	}
	return ReplyPrefix + " accept"
}

// ItemNumber resolves which item a decision is about: the number the app raised it
// for, else the first `#<n>` in the row's summary. It returns 0 rather than a guess
// when neither exists, and the caller logs and posts nothing (task 1.3).
//
// The summary fallback exists because a row created by hand carries the item in its
// text and nowhere else; rig's queue rows have no item field.
func ItemNumber(raised int, summary string) int {
	if raised > 0 {
		return raised
	}
	return ParseItemNumber(summary)
}

// ParseItemNumber returns the first `#<n>` in text, or 0. A `#` inside a URL
// fragment or a word like "issue#12" still counts: the number is what matters, and
// requiring a leading space would miss "Closes #157" and "#157:" alike.
func ParseItemNumber(text string) int {
	for i := 0; i < len(text); i++ {
		if text[i] != '#' {
			continue
		}
		j := i + 1
		number := 0
		digits := 0
		for j < len(text) && text[j] >= '0' && text[j] <= '9' {
			if digits == 0 && text[j] == '0' {
				// "#007" is not an item number; skip the whole run.
				for j < len(text) && text[j] >= '0' && text[j] <= '9' {
					j++
				}
				number = 0
				break
			}
			number = number*10 + int(text[j]-'0')
			digits++
			j++
			if digits > 9 {
				break
			}
		}
		if number > 0 {
			return number
		}
	}
	return 0
}

// Noter posts the question notes. Forge is the narrow write surface the app already
// has; Store is where the idempotency index lives; Logf reports the once-per-change
// failure. A nil Noter posts nothing.
type Noter struct {
	Forge Forge
	Store Store
	Logf  func(format string, args ...any)
	Now   func() time.Time
}

// Forge is the one write the note needs.
type Forge interface {
	Comment(ctx context.Context, number int, body string) error
}

// Store is the event ledger the idempotency index reads and writes.
type Store interface {
	HasEvent(ctx context.Context, kind, subject, contains string) (bool, error)
	AppendEvent(ctx context.Context, e Event) (int64, error)
}

// Event is one audit row. It mirrors store.Event's shape without importing it, so
// this package stays free of the database.
type Event struct {
	Actor  string
	Kind   string
	Subject string
	Detail string
}

// Result is what one decision's note attempt did.
type Result struct {
	Decision string
	Item     int
	Posted   bool   // a comment was written this call
	Skipped  bool   // already noted, or no item number to note on
	Reason   string // why it was skipped or what failed
}

// Note posts one decision's question on its item, once. A decision that already has
// a `decision.noted` event is skipped without a forge write, which is what makes two
// ticks produce one comment (task 1.2).
//
// A failed post writes one `decision.note_failed` per decision per change and is
// retried on the next tick: the event is the ledger, and the retry is the caller
// calling Note again. The escalation is not this function's business and is never
// blocked by a failed note.
func (n *Noter) Note(ctx context.Context, d Decision, nc NoteContext) (Result, error) {
	result := Result{Decision: d.ID, Item: d.Item}
	if n == nil || n.Forge == nil {
		result.Skipped, result.Reason = true, "no forge configured"
		return result, nil
	}
	if d.Item <= 0 {
		// Never guess a number: a note on the wrong item is worse than no note.
		// One log line so the operator knows why nothing appeared on the item.
		result.Skipped, result.Reason = true, "no item number"
		n.logf("decision.note_skipped %s: no item number in the row summary", d.ID)
		return result, nil
	}
	noted, err := n.noted(ctx, d.Item, d.ID)
	if err != nil {
		return result, err
	}
	if noted {
		result.Skipped, result.Reason = true, "already noted"
		return result, nil
	}
	body, _ := Render(d, nc)
	if err := n.Forge.Comment(ctx, d.Item, body); err != nil {
		result.Reason = err.Error()
		// One failure event per decision per change: the mirror.note shape. The
		// event log is the ledger, so an identical failure is not appended again —
		// a permanently broken write must not fill the journal every tick. The
		// retry still happens: the caller calls Note again next tick.
		detail := fmt.Sprintf("%s: %v", d.ID, err)
		already, hasErr := n.Store.HasEvent(ctx, "decision.note_failed", subjectOf(d.Item), detail)
		if hasErr != nil {
			n.logf("decision.note_failed %s: ledger read: %v", d.ID, hasErr)
		} else if already {
			return result, err
		}
		if _, appErr := n.Store.AppendEvent(ctx, Event{
			Actor: "act", Kind: "decision.note_failed", Subject: subjectOf(d.Item), Detail: detail,
		}); appErr != nil {
			n.logf("decision.note_failed %s: %v", d.ID, appErr)
		}
		return result, err
	}
	result.Posted = true
	if _, err := n.Store.AppendEvent(ctx, Event{
		Actor: "act", Kind: "decision.noted", Subject: subjectOf(d.Item),
		Detail: fmt.Sprintf("%s noted on #%d", d.ID, d.Item),
	}); err != nil {
		return result, err
	}
	return result, nil
}

// noted reads the idempotency index: the event log already says this decision was
// asked on this item.
func (n *Noter) noted(ctx context.Context, item int, decisionID string) (bool, error) {
	return n.Store.HasEvent(ctx, "decision.noted", subjectOf(item), decisionID)
}

// subjectOf is the event subject for an item: the `#<n>` form the rest of the audit
// log uses.
func subjectOf(item int) string { return fmt.Sprintf("#%d", item) }

func (n *Noter) logf(format string, args ...any) {
	if n.Logf != nil {
		n.Logf(format, args...)
	}
}
