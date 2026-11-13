package web

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/dinhtrung/fleet-console/internal/store"
	"github.com/gofiber/fiber/v3"
)

// Readiness is what GET /api/ready needs beyond the store: the tick interval the
// stale thresholds derive from, a clock, and the rig queue's most recent read.
// It is deliberately not the rig adapter — a probe must not shell out per
// request, so the queue answer arrives through QueueRead and is cached here.
type Readiness struct {
	TickInterval time.Duration
	// MaxWait is the longest the mirror's poll may legitimately sleep between
	// ticks — the cadence's own cap (rig_lifecycle.idle_poll_max when the block
	// is written, tick_interval otherwise). The stale threshold is derived from
	// it: a heartbeat written by the poll loop cannot be fresher than the loop's
	// wait, so 2 x tick_interval alone would call every quiet box stale (#393).
	MaxWait time.Duration
	Now     func() time.Time
	// QueueRead performs one `rig queue list`. Nil answers "not configured" as a
	// failure, which is honest: a control plane with no queue read is not ready.
	QueueRead func(context.Context) error
	// QueueTTL is how long a queue answer is reused. Zero is 30 s.
	QueueTTL time.Duration

	mu       sync.Mutex
	queueAt  time.Time
	queueErr error
	queueIn  bool
}

// queue answers whether the rig queue is readable, at most once per TTL. A
// probe hitting the endpoint every second still shells out once per 30 s — the
// answer, success or failure, is what the cache holds, which is also what makes
// a transient error visible for its window instead of flapping.
func (r *Readiness) queue(ctx context.Context) (time.Time, error) {
	now := r.now()
	r.mu.Lock()
	if r.queueIn && now.Sub(r.queueAt) < r.ttl() {
		at, err := r.queueAt, r.queueErr
		r.mu.Unlock()
		return at, err
	}
	r.mu.Unlock()

	if r.QueueRead == nil {
		return time.Time{}, fmt.Errorf("queue read is not configured")
	}
	err := r.QueueRead(ctx)

	r.mu.Lock()
	defer r.mu.Unlock()
	r.queueAt, r.queueErr, r.queueIn = now, err, true
	return r.queueAt, err
}

func (r *Readiness) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now().UTC()
}

// staleThreshold is twice the tick interval, raised to the poll's own cap plus
// one tick of scheduling grace: a quiet box writes its heartbeat once per
// backoff window, so the window itself is the floor a healthy heartbeat can be
// judged against.
func (r *Readiness) staleThreshold() time.Duration {
	threshold := 2 * r.TickInterval
	if threshold <= 0 {
		threshold = 2 * time.Minute
	}
	if r.MaxWait > 0 {
		if wait := r.MaxWait + r.TickInterval; wait > threshold {
			threshold = wait
		}
	}
	return threshold
}

func (r *Readiness) ttl() time.Duration {
	if r.QueueTTL > 0 {
		return r.QueueTTL
	}
	return 30 * time.Second
}

// readyFailure is one fact that failed, with the number that failed it: the
// fact's name, a sentence for a human, and the age and threshold it was judged
// against (empty when the fact has no age).
type readyFailure struct {
	Fact      string `json:"fact"`
	Detail    string `json:"detail"`
	Age       string `json:"age,omitempty"`
	Threshold string `json:"threshold,omitempty"`
}

// readyHandler answers the operator's question — can this control plane do its
// job right now? — and names every failing fact instead of a bare 503. The
// threshold is twice the tick interval: one quiet tick is normal, two is not.
// Liveness stays /api/health's constant; see the comment there for why.
func readyHandler(st *store.Store, readiness *Readiness) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		failures := []readyFailure{}
		if readiness == nil {
			readiness = &Readiness{}
		}
		now := readiness.now()
		threshold := readiness.staleThreshold()

		// The store first: everything below reads it, and one named database
		// failure is more useful than a cascade of reads failing.
		if err := st.Ping(ctx); err != nil {
			failures = append(failures, readyFailure{Fact: "database", Detail: err.Error()})
		} else if projects, err := st.Projects(ctx); err != nil {
			failures = append(failures, readyFailure{Fact: "database", Detail: err.Error()})
		} else {
			for _, project := range projects {
				status, err := st.MirrorStatus(ctx, project.ID)
				if err != nil {
					failures = append(failures, readyFailure{Fact: "mirror." + project.Key, Detail: err.Error()})
					continue
				}
				failures = append(failures, judgeAges("mirror."+project.Key, now, threshold,
					status.LastForgeAt, status.LastRigAt, status.LastTickAt)...)
			}
		}

		if _, err := readiness.queue(ctx); err != nil {
			failures = append(failures, readyFailure{Fact: "queue", Detail: err.Error()})
		}

		status := fiber.StatusOK
		label := "ok"
		if len(failures) > 0 {
			status, label = fiber.StatusServiceUnavailable, "failed"
		}
		return c.Status(status).JSON(fiber.Map{"status": label, "failures": failures})
	}
}

// judgeAges turns three heartbeats into failures: one per stamp older than the
// threshold, and one for a project that has never completed a tick. The forge
// and rig stamps are separate on purpose — a box whose forge token expired
// still reads its rig on time, and the operator needs to know which is stale.
func judgeAges(fact string, now time.Time, threshold time.Duration, forge, rig, tick *time.Time) []readyFailure {
	var out []readyFailure
	add := func(what string, at *time.Time) {
		if at == nil {
			out = append(out, readyFailure{
				Fact: fact, Detail: what + " has never completed",
				Threshold: threshold.String(),
			})
			return
		}
		age := now.Sub(*at)
		if age <= threshold {
			return
		}
		out = append(out, readyFailure{
			Fact:      fact,
			Detail:    fmt.Sprintf("%s is stale: %s old (at %s)", what, age.Round(time.Second), at.UTC().Format(time.RFC3339)),
			Age:       age.Round(time.Second).String(),
			Threshold: threshold.String(),
		})
	}
	add("last forge read", forge)
	add("last rig read", rig)
	add("last tick", tick)
	return out
}
