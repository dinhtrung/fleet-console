package web

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/store"
	"github.com/gofiber/fiber/v3"
)

// seedEvents writes n audit rows through the real store and returns their ids in
// insertion order.
func seedEvents(t *testing.T, st *store.Store, n int) []int64 {
	t.Helper()
	ctx := context.Background()
	ids := make([]int64, 0, n)
	for i := 1; i <= n; i++ {
		id, err := st.AppendEvent(ctx, store.Event{
			Actor: "act", Kind: "dispatch", Subject: "#" + strconv.Itoa(i),
		})
		if err != nil {
			t.Fatalf("append event %d: %v", i, err)
		}
		ids = append(ids, id)
	}
	return ids
}

func eventsApp(t *testing.T, st *store.Store) *fiber.App {
	t.Helper()
	app, err := New(Config{Token: testToken, Store: st, ConfigPath: "/tmp/fake-config.yaml"})
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	return app
}

// A page names the cursor for the next one, and the cursor is the smallest id on
// the page — not a total, and not an offset.
func TestEventsPageCarriesNextBefore(t *testing.T) {
	st := countingStore(t)
	ids := seedEvents(t, st, 3)
	app := eventsApp(t, st)

	resp, body := do(t, app, http.MethodGet, "/api/events?limit=2", testToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	page := decode[store.EventPage](t, body)
	if len(page.Events) != 2 {
		t.Fatalf("events = %d, want 2 (%s)", len(page.Events), body)
	}
	if page.Events[0].ID != ids[2] || page.Events[1].ID != ids[1] {
		t.Fatalf("page ids = [%d %d], want [%d %d] newest first",
			page.Events[0].ID, page.Events[1].ID, ids[2], ids[1])
	}
	if page.NextBefore != ids[1] {
		t.Fatalf("next_before = %d, want %d (the smallest id on the page)", page.NextBefore, ids[1])
	}

	// The cursor walks back to the last, short page.
	resp, body = do(t, app, http.MethodGet,
		"/api/events?limit=2&before="+strconv.FormatInt(page.NextBefore, 10), testToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("page 2 status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	last := decode[store.EventPage](t, body)
	if len(last.Events) != 1 || last.Events[0].ID != ids[0] {
		t.Fatalf("last page = %+v, want the one oldest event", last.Events)
	}
	if last.NextBefore != 0 {
		t.Fatalf("last page next_before = %d, want 0", last.NextBefore)
	}
}

// A name outside the closed filter set is refused by name, and the refusal
// happens before the store is touched: a dropped filter would answer "nothing
// matched" for a question the server did not understand.
func TestEventsUnknownFilterIs400AndTouchesNoStore(t *testing.T) {
	st := countingStore(t)
	seedEvents(t, st, 2)
	app := eventsApp(t, st)

	atomic.StoreInt64(&boardQueries, 0)
	resp, body := do(t, app, http.MethodGet, "/api/events?severity=high", testToken, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "severity") {
		t.Fatalf("body = %q, want it to name severity", body)
	}
	if got := atomic.LoadInt64(&boardQueries); got != 0 {
		t.Fatalf("store queries on a refused filter = %d, want 0", got)
	}
}

func TestEventsMalformedTimestampIs400AndTouchesNoStore(t *testing.T) {
	st := countingStore(t)
	seedEvents(t, st, 2)
	app := eventsApp(t, st)

	for _, path := range []string{"/api/events?since=yesterday", "/api/events?until=2026-13-40T99:00:00Z"} {
		atomic.StoreInt64(&boardQueries, 0)
		resp, body := do(t, app, http.MethodGet, path, testToken, "")
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400 (%s)", path, resp.StatusCode, body)
		}
		name := strings.TrimPrefix(path, "/api/events?")
		if !strings.Contains(string(body), "unsupported filter: "+name[:5]) {
			t.Fatalf("%s body = %q, want it to name %s", path, body, name[:5])
		}
		if got := atomic.LoadInt64(&boardQueries); got != 0 {
			t.Fatalf("%s store queries = %d, want 0", path, got)
		}
	}
}

func TestEventsMalformedCursorIs400AndTouchesNoStore(t *testing.T) {
	st := countingStore(t)
	seedEvents(t, st, 2)
	app := eventsApp(t, st)

	for _, path := range []string{"/api/events?before=abc", "/api/events?before=0", "/api/events?before=-4"} {
		atomic.StoreInt64(&boardQueries, 0)
		resp, body := do(t, app, http.MethodGet, path, testToken, "")
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400 (%s)", path, resp.StatusCode, body)
		}
		if !strings.Contains(string(body), "before") {
			t.Fatalf("%s body = %q, want it to name before", path, body)
		}
		if got := atomic.LoadInt64(&boardQueries); got != 0 {
			t.Fatalf("%s store queries = %d, want 0", path, got)
		}
	}
}

// One row, by id, with the exit code and stdout hash the audit contract records.
func TestEventDetailReturnsTheRow(t *testing.T) {
	st := countingStore(t)
	seedEvents(t, st, 2)
	app := eventsApp(t, st)

	exit := 3
	id, err := st.AppendEvent(context.Background(), store.Event{
		Actor: "act", Kind: "merge", Subject: "#9", Detail: "the gate refused", ExitCode: &exit, StdoutHash: "abc123",
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	resp, body := do(t, app, http.MethodGet, "/api/events/"+strconv.FormatInt(id, 10), testToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	event := decode[store.EventRow](t, body)
	if event.ID != id || event.Actor != "act" || event.Kind != "merge" || event.Subject != "#9" {
		t.Fatalf("event = %+v", event)
	}
	if event.ExitCode == nil || *event.ExitCode != 3 || event.StdoutHash != "abc123" {
		t.Fatalf("exit/stdout = %v/%q, want 3/abc123", event.ExitCode, event.StdoutHash)
	}
}

func TestEventDetailMissingIs404(t *testing.T) {
	st := countingStore(t)
	seedEvents(t, st, 1)
	app := eventsApp(t, st)

	for _, path := range []string{"/api/events/999999", "/api/events/0", "/api/events/nope"} {
		resp, body := do(t, app, http.MethodGet, path, testToken, "")
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404 (%s)", path, resp.StatusCode, body)
		}
	}
}

// The filters reach the query: a project prefix and an actor name each cut the
// page down, and the two compose.
func TestEventsFiltersApplyServerSide(t *testing.T) {
	st := countingStore(t)
	ctx := context.Background()
	for _, e := range []store.Event{
		{Actor: "act", Kind: "dispatch", Subject: "fleet-console"},
		{Actor: "act", Kind: "merge", Subject: "fleet-console#167"},
		{Actor: "human", Kind: "verdict", Subject: "fleet-console"},
		{Actor: "alert", Kind: "telegram.sent", Subject: "qitem-1"},
	} {
		if _, err := st.AppendEvent(ctx, e); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	app := eventsApp(t, st)

	resp, body := do(t, app, http.MethodGet, "/api/events?subject=fleet-console", testToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (%s)", resp.StatusCode, body)
	}
	if got := len(decode[store.EventPage](t, body).Events); got != 3 {
		t.Fatalf("subject=fleet-console events = %d, want 3", got)
	}

	resp, body = do(t, app, http.MethodGet, "/api/events?actor=act&kind=merge", testToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (%s)", resp.StatusCode, body)
	}
	page := decode[store.EventPage](t, body)
	if len(page.Events) != 1 || page.Events[0].Subject != "fleet-console#167" {
		t.Fatalf("actor=act&kind=merge = %+v, want the one merge", page.Events)
	}
}

// A filter that matches nothing is an empty list, not a missing field: the
// screen renders "no events match" from `events`, never from JSON null.
func TestEventsEmptyMatchIsAnEmptyList(t *testing.T) {
	st := countingStore(t)
	seedEvents(t, st, 2)
	app := eventsApp(t, st)

	resp, body := do(t, app, http.MethodGet, "/api/events?actor=nobody", testToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"events":[]`) {
		t.Fatalf("body = %s, want an empty list", body)
	}
	if !strings.Contains(string(body), `"next_before":0`) {
		t.Fatalf("body = %s, want next_before 0 present", body)
	}
}
