// This file holds the progress notifier: one message per state, never edited.
//
// A progress report is a photograph — unlike a decision message, which is
// edited as its state changes. So the notifier hashes the rendered body, sends
// a new message only when that hash is new, and records what it did:
// `progress.sent` with `<hash>:<messageID>`, or `progress.failed` with the
// transport error. A failed send is recorded and reported; it is never returned
// to the caller, because a dead Telegram must not fail a tick.
package alert

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/dinhtrung/fleet-console/internal/store"
)

const (
	// EventProgressSent records one delivered report. Its detail is
	// `<hash>:<messageID>` and the hash is the dedupe key.
	EventProgressSent = "progress.sent"
	// EventProgressFailed records one send that failed after the retry.
	EventProgressFailed = "progress.failed"

	// defaultRetryDelay is the one pause between the attempt and its retry.
	defaultRetryDelay = 2 * time.Second
	// defaultSubject is the dedupe ledger key when the caller names none.
	defaultSubject = "progress"
	// takenAt prefixes the timestamp line when a report labels it.
	takenAt = "taken at"
)

// ErrNotConfigured is what Notify answers when the store or the sender is
// absent: a configuration, not a failure. The caller tests it with errors.Is
// and logs one boot line instead of sending anything.
var ErrNotConfigured = errors.New("alert: progress notifications are not configured")

// ProgressResult is what happened to one report.
type ProgressResult struct {
	Hash      string // the state hash the dedupe used
	MessageID string // the message the send produced
	Sent      bool   // a new message was created
	Duplicate bool   // this state was already reported
	Failed    bool   // the transport failed after the retry
	Reason    string // why it failed, empty otherwise
}

// Notifier sends the progress report once per state.
//
// Store is the audit ledger the dedupe reads; Sender is Telegram. Subject names
// the ledger entries (the project key, so two projects never dedupe against
// each other) and defaults to "progress". Logf receives the one failure line
// and defaults to stderr with no prefix; Sleep is the retry pause and defaults
// to time.Sleep, so a test never waits two seconds.
type Notifier struct {
	Store      *store.Store
	Sender     Sender
	Subject    string
	Logf       func(format string, args ...any)
	Sleep      func(time.Duration)
	RetryDelay time.Duration
}

// Configured reports whether the notifier can send at all. The boot path uses
// it to print its one line and to make every trigger a no-op.
func (n *Notifier) Configured() bool {
	return n != nil && n.Store != nil && n.Sender != nil
}

// Notify sends body when its state is new and records the outcome.
//
// A transport failure is recorded (`progress.failed`), printed once, and
// reported in the result with a nil error: a dead Telegram is not a dead fleet.
// Store failures are returned, because a ledger that cannot be written is a
// defect the caller should see.
func (n *Notifier) Notify(ctx context.Context, body string) (ProgressResult, error) {
	if !n.Configured() {
		return ProgressResult{}, ErrNotConfigured
	}
	subject := n.subject()
	hash := HashProgress(body)

	previous, err := n.Store.EventByKindAndSubject(ctx, EventProgressSent, subject)
	if err != nil {
		return ProgressResult{Hash: hash}, err
	}
	if previous != nil {
		if recorded, messageID, ok := splitProgressDetail(previous.Detail); ok && recorded == hash {
			return ProgressResult{Hash: hash, MessageID: messageID, Duplicate: true}, nil
		}
	}

	messageID, sendErr := n.send(ctx, body)
	if sendErr != nil {
		// The ledger write must survive a cancelled tick: the audit trail is the
		// whole point of the failure path, so it runs on a context that outlives
		// the caller's deadline.
		_, writeErr := n.Store.AppendEvent(context.WithoutCancel(ctx), store.Event{
			Actor: "alert", Kind: EventProgressFailed, Subject: subject, Detail: sendErr.Error(),
		})
		n.logf("alert: progress send: %v", sendErr)
		result := ProgressResult{Hash: hash, Failed: true, Reason: sendErr.Error()}
		if writeErr != nil {
			return result, fmt.Errorf("alert: record progress failure: %w", writeErr)
		}
		return result, nil
	}

	if _, err := n.Store.AppendEvent(ctx, store.Event{
		Actor: "alert", Kind: EventProgressSent, Subject: subject, Detail: hash + ":" + messageID,
	}); err != nil {
		return ProgressResult{Hash: hash, MessageID: messageID, Sent: true}, err
	}
	return ProgressResult{Hash: hash, MessageID: messageID, Sent: true}, nil
}

// send makes one attempt and, if it failed, exactly one more after the pause.
// A cancelled context is not retried: the caller has already given up.
func (n *Notifier) send(ctx context.Context, body string) (string, error) {
	messageID, err := n.Sender.Send(ctx, body)
	if err == nil {
		return messageID, nil
	}
	first := err
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", fmt.Errorf("%w (context: %v)", first, ctxErr)
	}
	n.sleep(n.retryDelay())
	messageID, err = n.Sender.Send(ctx, body)
	if err != nil {
		return "", fmt.Errorf("after one retry: %w", err)
	}
	return messageID, nil
}

// HashProgress is the dedupe key: the sha256 of the body with its trailing
// timestamp line removed, so a report that changed only the clock is the same
// state and is not sent twice.
func HashProgress(body string) string {
	sum := sha256.Sum256([]byte(ProgressBody(body)))
	return hex.EncodeToString(sum[:])
}

// ProgressBody is body without the trailing timestamp line. The report itself is
// unchanged; only the hash ignores the clock, which is the one field that moves
// without the state moving.
func ProgressBody(body string) string {
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) == 0 {
		return ""
	}
	if _, ok := parseProgressTime(lines[len(lines)-1]); ok {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// parseProgressTime reads the timestamp line a report carries, if it carries
// one: a bare RFC 3339 stamp or the same stamp behind "taken at ". The label is
// matched case-insensitively and removed without touching the value, because
// RFC 3339's `T` and `Z` are case-sensitive to time.Parse.
func parseProgressTime(line string) (time.Time, bool) {
	value := strings.TrimSpace(line)
	if len(value) >= len(takenAt) && strings.EqualFold(value[:len(takenAt)], takenAt) {
		value = strings.TrimSpace(value[len(takenAt):])
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05Z"} {
		if at, err := time.Parse(layout, value); err == nil {
			return at, true
		}
	}
	return time.Time{}, false
}

// splitProgressDetail reads `<hash>:<messageID>` back out of the ledger.
func splitProgressDetail(detail string) (hash, messageID string, ok bool) {
	index := strings.Index(detail, ":")
	if index <= 0 || index == len(detail)-1 {
		return "", "", false
	}
	return detail[:index], detail[index+1:], true
}

func (n *Notifier) subject() string {
	if value := strings.TrimSpace(n.Subject); value != "" {
		return value
	}
	return defaultSubject
}

func (n *Notifier) retryDelay() time.Duration {
	if n.RetryDelay > 0 {
		return n.RetryDelay
	}
	return defaultRetryDelay
}

func (n *Notifier) sleep(delay time.Duration) {
	if n.Sleep != nil {
		n.Sleep(delay)
		return
	}
	time.Sleep(delay)
}

func (n *Notifier) logf(format string, args ...any) {
	logger := n.Logf
	if logger == nil {
		logger = log.New(os.Stderr, "", 0).Printf
	}
	logger(format, args...)
}
