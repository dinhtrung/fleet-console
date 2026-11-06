package web

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/dinhtrung/fleet-console/internal/mirror"
)

// ForgeHook receives forge deliveries. The route is registered on the app itself,
// not on the bearer group: a forge cannot present the board's token, so the HMAC is
// the whole credential and a token check would reject every real delivery.
//
// Everything here happens before any store access. The handler reads no row,
// dispatches nothing and changes no row — a verified delivery only asks the loop to
// tick sooner than its timer would (design D2/D4).
type ForgeHook struct {
	// Secret is the shared secret the forge signs with. Empty means the receiver is
	// not configured: the route answers 404 and never reads the body (design D3).
	Secret string
	// Waker is the loop's wake channel. A nil Waker is safe: the delivery is still
	// verified and still answers 202, reporting woke:false because nothing consumed
	// the request.
	Waker *mirror.Waker
	// Logf is where the delivery line goes. Nil writes to the standard logger; a
	// test installs a capture so it can assert the line names the event.
	Logf func(format string, args ...any)
}

// SignatureHeader is the header GitHub signs deliveries with.
const SignatureHeader = "X-Hub-Signature-256"

// maxBody bounds the delivery. GitHub's payloads for an issues event are a few KB;
// 1 MiB leaves generous room while refusing to buffer a body chosen by the sender.
const maxBody = 1 << 20

// HookResponse is what a delivery answers.
type HookResponse struct {
	// Woke is false when a wake was already pending and this delivery was coalesced
	// into it. A caller retrying on woke:false would be retrying something that is
	// already about to happen.
	Woke bool `json:"woke"`
}

// Handle is the POST /api/hooks/forge handler.
func (h ForgeHook) Handle(c fiber.Ctx) error {
	if h.Secret == "" {
		// Off until configured. The body is never read, so an upgraded box cannot be
		// pushed into buffering a delivery it will not use.
		return fiber.NewError(http.StatusNotFound, "forge webhook is not configured")
	}

	// Order matters. The size check comes first so an oversized claim costs nothing;
	// then the header, so a request with no signature is refused before its body is
	// buffered; only then is the body read, because the HMAC needs the exact bytes.
	if size := c.Get("Content-Length"); size != "" {
		if n, err := strconv.ParseInt(size, 10, 64); err == nil && n > maxBody {
			return fiber.NewError(http.StatusRequestEntityTooLarge,
				fmt.Sprintf("request body exceeds %d bytes", maxBody))
		}
	}
	signature := c.Get(SignatureHeader)
	if signature == "" {
		// The body is never read and never logged: a delivery's payload is the forge's
		// data, not ours to write down.
		return fiber.NewError(http.StatusUnauthorized, "missing "+SignatureHeader)
	}
	body, err := readBounded(c)
	if err != nil {
		return fiber.NewError(http.StatusRequestEntityTooLarge,
			fmt.Sprintf("request body exceeds %d bytes", maxBody))
	}
	if !validSignature(body, signature, h.Secret) {
		return fiber.NewError(http.StatusUnauthorized, "signature does not match")
	}

	woke := h.Waker.Wake()

	// One line names the delivery. The payload is not logged: the body is the
	// forge's data, and a log that quotes it leaks issues into a file with wider
	// access than the forge itself.
	event := eventOf(body)
	h.logDelivery(event, woke)

	return c.Status(http.StatusAccepted).JSON(HookResponse{Woke: woke})
}

// readBounded reads at most maxBody bytes. A larger body is reported as an error
// without reading it into memory, so the 413 path allocates nothing proportional to
// the sender's claim.
func readBounded(c fiber.Ctx) ([]byte, error) {
	if size := c.Get("Content-Length"); size != "" {
		if n, err := strconv.ParseInt(size, 10, 64); err == nil && n > maxBody {
			return nil, fmt.Errorf("declared size %d", n)
		}
	}
	return io.ReadAll(io.LimitReader(bytes.NewReader(c.Body()), maxBody+1))
}

// validSignature compares the delivery's HMAC against the one the secret implies.
// It is constant time and accepts only the documented shape: a sha256 digest in
// hex, prefixed by "sha256=". A malformed header is refused as firmly as a wrong
// digest, because a header we cannot parse is a header we cannot verify.
func validSignature(body []byte, header, secret string) bool {
	digest, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	claimed, err := hex.DecodeString(digest)
	if err != nil || len(claimed) != sha256.Size {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), claimed)
}

// DeliveryEvent is the slice of a forge delivery the log line names. Only fields
// the forge documents on an issues delivery are read; anything else is ignored
// rather than validated, because a forge may add fields at any time.
type DeliveryEvent struct {
	Action string `json:"action"`
	Issue  struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
	} `json:"issue"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

// eventOf extracts what the log line names. A body that does not parse yields the
// zero value: the wake is already sent by then, and a delivery we could not name is
// still a delivery the forge thought was worth sending.
func eventOf(body []byte) DeliveryEvent {
	var event DeliveryEvent
	if err := json.Unmarshal(body, &event); err != nil {
		return DeliveryEvent{}
	}
	return event
}

// logDelivery writes the one line the spec asks for. A delivery that found a wake
// already pending is named as coalesced, so an operator reading the log can tell a
// burst of deliveries from a burst of ticks.
func (h ForgeHook) logDelivery(event DeliveryEvent, woke bool) {
	line := "forge webhook: delivery accepted"
	if event.Repository.FullName != "" {
		line += " repo=" + event.Repository.FullName
	}
	if event.Action != "" {
		line += " action=" + event.Action
	}
	if event.Issue.Number != 0 {
		line += " issue=#" + strconv.Itoa(event.Issue.Number)
	}
	if woke {
		line += " woke=true"
	} else {
		line += " woke=false coalesced"
	}
	if h.Logf != nil {
		h.Logf("%s", line)
		return
	}
	log.Print(line)
}
