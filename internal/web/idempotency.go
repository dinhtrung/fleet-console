package web

import (
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
)

const (
	idempotencyKeyHeader = "Idempotency-Key"
	idempotencyTTL       = 24 * time.Hour
	idempotencyMax       = 10000
)

type idemEntry struct {
	status      int
	contentType string
	body        []byte
	at          time.Time
}

// idemLedger remembers the response of each completed mutating request. It is
// process-local: a restart forgets keys, which the durable act log covers once
// the act loop exists. Bounded by TTL and size so a long-running server cannot
// leak keys.
type idemLedger struct {
	mu      sync.Mutex
	entries map[string]idemEntry
}

func newIdemLedger() *idemLedger {
	return &idemLedger{entries: make(map[string]idemEntry)}
}

// idempotency enforces one Idempotency-Key header on every mutating request and
// replays the first successful response instead of running the handler twice.
// It serializes mutating requests: writes are the act loop's already-serialized
// path, and this keeps the first-writer-wins check honest.
func idempotency() fiber.Handler {
	ledger := newIdemLedger()
	return ledger.handler()
}

// handler is the middleware over one ledger. Split from idempotency so a test
// can drive its own ledger and read the keys it stored (#474).
func (l *idemLedger) handler() fiber.Handler {
	return func(c fiber.Ctx) error {
		// Session endpoints are idempotent by nature and are reached by a plain
		// HTML form, which cannot set a header. The forge receiver is the same case
		// from the other direction: a forge signs its delivery with an HMAC and
		// cannot know a board header, and its own signature already makes a replay
		// harmless — a repeated delivery sends a wake that the next tick coalesces.
		switch c.Path() {
		case "/login", "/logout", "/api/session", "/api/hooks/forge":
			return c.Next()
		}
		switch c.Method() {
		case fiber.MethodGet, fiber.MethodHead, fiber.MethodOptions:
			return c.Next()
		}
		// Clone: c.Get hands back a string that aliases fasthttp's reused header
		// buffer (this app does not set fiber's Immutable option), and this key
		// lives in the ledger for 24 hours. Without the copy the stored key's
		// bytes are rewritten by a later request, so two entries end up with the
		// same text and a lookup can replay an unrelated response — the response
		// of a different request, with the handler never run (#474).
		key := strings.Clone(strings.TrimSpace(c.Get(idempotencyKeyHeader)))
		if key == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "missing Idempotency-Key header"})
		}

		l.mu.Lock()
		defer l.mu.Unlock()

		if entry, ok := l.entries[key]; ok {
			if time.Since(entry.at) <= idempotencyTTL {
				if entry.contentType != "" {
					c.Set(fiber.HeaderContentType, entry.contentType)
				}
				return c.Status(entry.status).Send(entry.body)
			}
			delete(l.entries, key)
		}

		if err := c.Next(); err != nil {
			return err
		}
		status := c.Response().StatusCode()
		if status < 200 || status >= 300 {
			return nil
		}
		l.put(key, idemEntry{
			status:      status,
			contentType: string(c.Response().Header.ContentType()),
			body:        append([]byte(nil), c.Response().Body()...),
			at:          time.Now(),
		})
		return nil
	}
}

// put assumes ledger.mu is held.
func (l *idemLedger) put(key string, entry idemEntry) {
	if len(l.entries) >= idempotencyMax {
		now := time.Now()
		for k, v := range l.entries {
			if now.Sub(v.at) > idempotencyTTL {
				delete(l.entries, k)
			}
		}
		if len(l.entries) >= idempotencyMax {
			var oldestKey string
			var oldest time.Time
			for k, v := range l.entries {
				if oldestKey == "" || v.at.Before(oldest) {
					oldestKey, oldest = k, v.at
				}
			}
			delete(l.entries, oldestKey)
		}
	}
	l.entries[key] = entry
}
