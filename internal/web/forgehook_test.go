package web

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/dinhtrung/fleet-console/internal/mirror"
)

const hookSecret = "s3cr3t-shared-with-the-forge"

// sign computes the header the forge sends.
func sign(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// hookApp builds the receiver with a captured log, and the waker the test watches.
func hookApp(t *testing.T, secret string) (*fiber.App, *mirror.Waker, *[]string) {
	t.Helper()
	waker := mirror.NewWaker()
	var lines []string
	hook := ForgeHook{
		Secret: secret,
		Waker:  waker,
		Logf: func(format string, args ...any) {
			lines = append(lines, fmt.Sprintf(format, args...))
		},
	}
	app := fiber.New()
	// Registered on the app itself, matching app.go: the route must not sit behind
	// bearerAuth, or every real delivery would 401 before the HMAC ran.
	app.Post("/api/hooks/forge", hook.Handle)
	return app, waker, &lines
}

func post(t *testing.T, app *fiber.App, body []byte, headers map[string]string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/hooks/forge", bytes.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := app.Test(req, fiber.TestConfig{Timeout: 5000})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	return res
}

// deliveryBody is the smallest issues delivery the log line needs to name.
var deliveryBody = []byte(`{"action":"opened","issue":{"number":170,"title":"webhook"},"repository":{"full_name":"dinhtrung/fleet-console"}}`)

// A valid delivery wakes the loop and answers 202 woke:true, and the log names it.
func TestForgeHookValidDeliveryWakes(t *testing.T) {
	app, waker, lines := hookApp(t, hookSecret)
	res := post(t, app, deliveryBody, map[string]string{
		SignatureHeader: sign(deliveryBody, hookSecret),
	})
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", res.StatusCode)
	}
	if got := waker.Channel(); len(got) != 1 {
		t.Fatalf("pending wakes = %d, want 1", len(got))
	}
	if len(*lines) != 1 {
		t.Fatalf("log lines = %v, want one", *lines)
	}
	line := (*lines)[0]
	for _, want := range []string{"repo=dinhtrung/fleet-console", "action=opened", "issue=#170", "woke=true"} {
		if !strings.Contains(line, want) {
			t.Fatalf("log line %q does not name %q", line, want)
		}
	}
}

// Two deliveries before the loop consumes the first: both 202, one wake, and the
// second says so rather than pretending a second tick was queued.
func TestForgeHookCoalescesPendingWake(t *testing.T) {
	app, waker, _ := hookApp(t, hookSecret)
	sig := sign(deliveryBody, hookSecret)

	first := post(t, app, deliveryBody, map[string]string{SignatureHeader: sig})
	second := post(t, app, deliveryBody, map[string]string{SignatureHeader: sig})
	for name, res := range map[string]*http.Response{"first": first, "second": second} {
		if res.StatusCode != http.StatusAccepted {
			t.Fatalf("%s status = %d, want 202", name, res.StatusCode)
		}
	}
	if got := len(waker.Channel()); got != 1 {
		t.Fatalf("pending wakes = %d, want exactly 1 after two deliveries", got)
	}
}

// Missing, malformed and wrong signatures all answer 401 and send no wake.
func TestForgeHookRefusesBadSignatures(t *testing.T) {
	cases := []struct {
		name   string
		header string
	}{
		{"no signature", ""},
		{"malformed prefix", "sha1=abcd"},
		{"not hex", "sha256=zzzz"},
		{"wrong length", "sha256=abcd"},
		{"wrong secret", sign(deliveryBody, "a-different-secret")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app, waker, lines := hookApp(t, hookSecret)
			headers := map[string]string{}
			if tc.header != "" {
				headers[SignatureHeader] = tc.header
			}
			res := post(t, app, deliveryBody, headers)
			if res.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", res.StatusCode)
			}
			if got := len(waker.Channel()); got != 0 {
				t.Fatalf("pending wakes = %d, want 0", got)
			}
			if len(*lines) != 0 {
				t.Fatalf("log lines = %v, want none: a refused delivery must not be logged", *lines)
			}
		})
	}
}

// The raw-body proof: the HMAC is over the bytes as received, not over a re-encode of the
// decoded payload. A handler that decoded first and hashed the re-marshalled value would reject a
// legitimate delivery whose bytes differ from its canonical form — so the discriminating case is a
// delivery signed over its own non-canonical bytes.
func TestForgeHookHashesTheRawBody(t *testing.T) {
	app, waker, lines := hookApp(t, hookSecret)

	// Same JSON object as deliveryBody, different bytes: key order differs.
	reordered := []byte(`{"repository":{"full_name":"dinhtrung/fleet-console"},"issue":{"title":"webhook","number":170},"action":"opened"}`)
	if bytes.Equal(reordered, deliveryBody) {
		t.Fatal("the reordered body is byte-identical; this test proves nothing")
	}
	// The canonical form a decode-then-marshal handler would hash. It must differ from the bytes
	// sent, or this test cannot tell the two handlers apart.
	var decoded any
	if err := json.Unmarshal(reordered, &decoded); err != nil {
		t.Fatalf("unmarshal reordered: %v", err)
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	if bytes.Equal(canonical, reordered) {
		t.Fatalf("re-marshalling reproduced the sent bytes (%s); key order is not being tested", canonical)
	}

	// 1. Signed over the bytes actually sent. A handler that hashes what it received accepts; a
	//    handler that hashes the canonical form would compute a different digest and refuse. This
	//    is the assertion that distinguishes them.
	raw := post(t, app, reordered, map[string]string{
		SignatureHeader: sign(reordered, hookSecret),
	})
	if raw.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: the delivery is signed over the bytes that were sent", raw.StatusCode)
	}
	if got := len(waker.Channel()); got != 1 {
		t.Fatalf("pending wakes = %d, want 1", got)
	}

	// 2. Signed over the canonical form instead, while sending the reordered bytes: the digest
	//    covers bytes the server never received, so it must be refused.
	app2, waker2, lines2 := hookApp(t, hookSecret)
	mismatch := post(t, app2, reordered, map[string]string{
		SignatureHeader: sign(canonical, hookSecret),
	})
	if mismatch.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: the signature covers bytes that were not sent", mismatch.StatusCode)
	}
	if got := len(waker2.Channel()); got != 0 {
		t.Fatalf("pending wakes = %d, want 0", got)
	}
	if len(*lines2) != 0 {
		t.Fatalf("log lines = %v, want none", *lines2)
	}
	if len(*lines) != 1 {
		t.Fatalf("accepted delivery log lines = %d, want 1", len(*lines))
	}
}

// With no secret configured the route answers 404 and reads no body.
func TestForgeHookOffWhenNotConfigured(t *testing.T) {
	app, waker, lines := hookApp(t, "")
	res := post(t, app, deliveryBody, map[string]string{
		SignatureHeader: sign(deliveryBody, "anything"),
	})
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.StatusCode)
	}
	if got := len(waker.Channel()); got != 0 {
		t.Fatalf("pending wakes = %d, want 0", got)
	}
	if len(*lines) != 0 {
		t.Fatalf("log lines = %v, want none", *lines)
	}
}

// An oversized body answers 413 without an HMAC comparison. The declared size is
// enough: the handler refuses before reading, so a large claim costs nothing.
func TestForgeHookRefusesOversizedBody(t *testing.T) {
	app, waker, _ := hookApp(t, hookSecret)
	big := bytes.Repeat([]byte("x"), maxBody+1)
	res := post(t, app, big, map[string]string{
		SignatureHeader: sign(big, hookSecret),
	})
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", res.StatusCode)
	}
	if got := len(waker.Channel()); got != 0 {
		t.Fatalf("pending wakes = %d, want 0", got)
	}
}

// A verified delivery with no loop running still answers 202 and reports that
// nothing consumed the wake, so a caller can tell "queued" from "nobody listening".
func TestForgeHookWithoutWakerStillAccepts(t *testing.T) {
	hook := ForgeHook{Secret: hookSecret, Logf: func(string, ...any) {}}
	app := fiber.New()
	app.Post("/api/hooks/forge", hook.Handle)

	res := post(t, app, deliveryBody, map[string]string{
		SignatureHeader: sign(deliveryBody, hookSecret),
	})
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", res.StatusCode)
	}
	var out HookResponse
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("body: %v", err)
	}
	if out.Woke {
		t.Fatal("woke = true with no waker, want false")
	}
}

// The app installs idempotency() on every request. A forge cannot set a board
// header, so the receiver must be exempt or every real delivery dies as a 400
// before the HMAC runs. This drives the real middleware, not a stub.
func TestForgeHookExemptFromIdempotency(t *testing.T) {
	waker := mirror.NewWaker()
	hook := ForgeHook{Secret: hookSecret, Waker: waker, Logf: func(string, ...any) {}}
	app := fiber.New()
	app.Use(idempotency())
	app.Post("/api/hooks/forge", hook.Handle)

	res := post(t, app, deliveryBody, map[string]string{
		SignatureHeader: sign(deliveryBody, hookSecret),
	})
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: the receiver must not require an Idempotency-Key", res.StatusCode)
	}
	if got := len(waker.Channel()); got != 1 {
		t.Fatalf("pending wakes = %d, want 1", got)
	}
}
