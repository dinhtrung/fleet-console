package act

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// The attention vocabulary (#427). One obligation per (seat, question): raised
// the first tick it is seen, resolved on the first successful read that no
// longer reports it. The ledger is the durable record; the notice is the
// operator's one alert per change.
const (
	EventAttentionRaised   = "attention.raised"
	EventAttentionResolved = "attention.resolved"
)

// AttentionNotice is one obligation appearing or ending, for the caller's notice
// channel. A resolved notice carries the time the question was first seen, so
// the alert can say how long it stood.
type AttentionNotice struct {
	Seat     string
	Rig      string
	Kind     string
	Question string
	Since    time.Time
	Resolved bool
}

// attentionSeen is the process memory for one standing fingerprint. It is like
// the notifier's quiet state: a restart forgets the age, and nothing decides on
// it (design D2).
type attentionSeen struct {
	notice rig.SeatAttention
	since  time.Time
}

// AttentionCoordinator turns the rig's seat facts into ledger obligations. It is
// safe for one caller per tick; Observe serializes itself so two projects
// sharing a rig cannot double-raise a question.
type AttentionCoordinator struct {
	Store *store.Store
	Now   func() time.Time
	// Notify sees every raise and every resolve, once each. Nil is a no-op.
	Notify func(AttentionNotice)

	mu   sync.Mutex
	seen map[string]attentionSeen
}

// Observe consumes one read's seats for the rigs it covered. coveredRigs is
// what the read actually looked at: a project bound only to rig-b must not
// resolve rig-a's standing obligation, because its read never covered rig-a
// (#427 review). readOK false means the read failed or was stale: it proves
// nothing, so nothing is raised and nothing is resolved — absence must be
// observed, not inferred (design D2).
func (c *AttentionCoordinator) Observe(ctx context.Context, seats []rig.Seat, coveredRigs []string, readOK bool) error {
	if c == nil || c.Store == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !readOK {
		return nil
	}
	if c.seen == nil {
		c.seen = map[string]attentionSeen{}
	}
	now := c.now()

	covered := make(map[string]bool, len(coveredRigs))
	for _, name := range coveredRigs {
		if name != "" {
			covered[name] = true
		}
	}

	current := make(map[string]rig.SeatAttention)
	for _, seat := range seats {
		for _, entry := range seat.Attention {
			current[attentionFingerprint(entry)] = entry
		}
	}

	// A question this tick reports that no earlier tick did is a new
	// obligation. The first-seen time is this tick.
	for fingerprint, entry := range current {
		if _, standing := c.seen[fingerprint]; standing {
			continue
		}
		c.seen[fingerprint] = attentionSeen{notice: entry, since: now}
		if err := c.write(ctx, EventAttentionRaised, entry, fmt.Sprintf("question=%s;", entry.Question)); err != nil {
			return err
		}
		c.notify(AttentionNotice{
			Seat: entry.Seat, Rig: entry.Rig, Kind: entry.Kind, Question: entry.Question, Since: now,
		})
	}

	// A standing obligation a successful read no longer reports has been
	// answered. Nothing here reads a clock: a timer cannot tell "answered"
	// from "never asked again".
	for fingerprint, standing := range c.seen {
		if _, still := current[fingerprint]; still {
			continue
		}
		// Only a read that covered the obligation's rig can see it answered.
		if !covered[standing.notice.Rig] {
			continue
		}
		delete(c.seen, fingerprint)
		detail := fmt.Sprintf("question=%s; stood=%s;",
			standing.notice.Question, now.Sub(standing.since).Round(time.Second))
		if err := c.write(ctx, EventAttentionResolved, standing.notice, detail); err != nil {
			return err
		}
		// D2: the resolution is one event and no notice — the raise was the alert;
		// its clearing needs no second one.
	}
	return nil
}

// attentionFingerprint is `seat + hash(question)` (design D2). The kind is not
// part of it: one seat waiting on one question is one obligation — hashing only
// the question means a re-worded question is a new obligation, and the same
// question reported by a different kind is the same wait.
func attentionFingerprint(entry rig.SeatAttention) string {
	sum := sha256.Sum256([]byte(entry.Question))
	return entry.Seat + "\x00" + fmt.Sprintf("%x", sum[:8])
}

func (c *AttentionCoordinator) write(ctx context.Context, kind string, entry rig.SeatAttention, detail string) error {
	detail = fmt.Sprintf("kind=%s; rig=%s; %s", entry.Kind, entry.Rig, detail)
	_, err := c.Store.AppendEvent(ctx, store.Event{
		Actor: "act", Kind: kind, Subject: entry.Seat, Detail: detail,
	})
	return err
}

func (c *AttentionCoordinator) notify(notice AttentionNotice) {
	if c.Notify != nil {
		c.Notify(notice)
	}
}

func (c *AttentionCoordinator) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

// AttentionLine renders one raise as the journal line main prints. A resolution
// has no notice (design D2), so there is no resolved form here.
func AttentionLine(notice AttentionNotice) string {
	question := notice.Question
	if strings.TrimSpace(question) == "" {
		question = "the rig gave no question"
	}
	return fmt.Sprintf("%s on %s is waiting on a human (%s): %s", notice.Seat, notice.Rig, notice.Kind, question)
}
