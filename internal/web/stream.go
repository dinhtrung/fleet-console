package web

import (
	"bufio"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/gofiber/fiber/v3"
)

// Hub fans one state change out to every open SSE client.
type Hub struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

// NewHub returns an empty hub.
func NewHub() *Hub { return &Hub{subs: make(map[chan []byte]struct{})} }

// Subscribe registers a client. The caller must Unsubscribe.
func (h *Hub) Subscribe() chan []byte {
	ch := make(chan []byte, 8)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

// Unsubscribe removes a client.
func (h *Hub) Unsubscribe(ch chan []byte) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

// Publish encodes one SSE event. A slow client is skipped, never blocked on.
func (h *Hub) Publish(kind string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		data = []byte(`{}`)
	}
	msg := []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", kind, data))
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- msg:
		default:
		}
	}
}

// Stream serves GET /api/stream.
func (h *Hub) Stream(c fiber.Ctx) error {
	c.Set(fiber.HeaderContentType, "text/event-stream")
	c.Set(fiber.HeaderCacheControl, "no-cache")
	c.Set(fiber.HeaderConnection, "keep-alive")
	ch := h.Subscribe()
	done := c.RequestCtx().Done()
	return c.SendStreamWriter(func(w *bufio.Writer) {
		defer h.Unsubscribe(ch)
		fmt.Fprint(w, ": connected\n\n")
		w.Flush()
		for {
			select {
			case msg := <-ch:
				if _, err := w.Write(msg); err != nil {
					return
				}
				w.Flush()
			case <-done:
				return
			}
		}
	})
}
