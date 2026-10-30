package alert

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/store"
)

// fakeSender records what the notifier did and answers errors on demand.
type fakeSender struct {
	bodies  []string
	edits   []string
	failFor int // how many Send calls fail before one succeeds; 0 = none
	attempt int
}

func (f *fakeSender) Send(_ context.Context, text string) (string, error) {
	f.attempt++
	f.bodies = append(f.bodies, text)
	if f.failFor > 0 {
		f.failFor--
		return "", errors.New("telegram sendMessage: Bad Gateway")
	}
	return fmt.Sprintf("%d", 100+len(f.bodies)), nil
}

func (f *fakeSender) Edit(_ context.Context, messageID, text string) error {
	f.edits = append(f.edits, messageID+": "+text)
	return nil
}

func notifierStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "fleet.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func newNotifier(t *testing.T, sender Sender) (*Notifier, *store.Store, *[]string) {
	t.Helper()
	st := notifierStore(t)
	var logged []string
	notifier := &Notifier{
		Store: st, Sender: sender, Subject: "app",
		Sleep: func(time.Duration) {},
		Logf:  func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) },
	}
	return notifier, st, &logged
}

// report is the rendered shape a report has: a body and a trailing timestamp.
func report(state string, at time.Time) string {
	return "PROJECT\n  key: app\n" + state + "\n\n" + at.UTC().Format(time.RFC3339)
}

func events(t *testing.T, st *store.Store, kind string) []store.EventRow {
	t.Helper()
	rows, err := st.RecentEvents(context.Background(), 50)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	var out []store.EventRow
	for _, row := range rows {
		if row.Kind == kind {
			out = append(out, row)
		}
	}
	return out
}

func TestNotifySendsOncePerState(t *testing.T) {
	sender := &fakeSender{}
	notifier, st, _ := newNotifier(t, sender)
	base := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

	first, err := notifier.Notify(context.Background(), report("▶ in progress (2)", base))
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if !first.Sent || first.Duplicate || first.MessageID != "101" || first.Hash == "" {
		t.Fatalf("first = %+v, want one send with a message id", first)
	}

	// Same state, one tick later: the timestamp moved, the state did not.
	second, err := notifier.Notify(context.Background(), report("▶ in progress (2)", base.Add(15*time.Second)))
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !second.Duplicate || second.Sent || second.Hash != first.Hash {
		t.Fatalf("second = %+v, want a duplicate with the same hash", second)
	}
	if len(sender.bodies) != 1 {
		t.Fatalf("sends = %d, want exactly one for one state", len(sender.bodies))
	}
	if len(sender.edits) != 0 {
		t.Fatalf("edits = %d, want none: a progress report is never edited", len(sender.edits))
	}

	// A changed bucket is a new state.
	third, err := notifier.Notify(context.Background(), report("▶ in progress (1)", base.Add(30*time.Second)))
	if err != nil {
		t.Fatalf("third: %v", err)
	}
	if !third.Sent || third.Hash == first.Hash {
		t.Fatalf("third = %+v, want a new send with a new hash", third)
	}
	if len(sender.bodies) != 2 {
		t.Fatalf("sends = %d, want two after the state changed", len(sender.bodies))
	}
	if len(sender.edits) != 0 {
		t.Fatalf("edits = %d, want none", len(sender.edits))
	}

	stored := events(t, st, EventProgressSent)
	if len(stored) != 2 {
		t.Fatalf("progress.sent rows = %d, want 2", len(stored))
	}
	for index, want := range []ProgressResult{first, third} {
		if stored[len(stored)-1-index].Detail != want.Hash+":"+want.MessageID {
			t.Fatalf("stored detail = %q, want %q", stored[len(stored)-1-index].Detail, want.Hash+":"+want.MessageID)
		}
		if stored[len(stored)-1-index].Subject != "app" {
			t.Fatalf("stored subject = %q, want app", stored[len(stored)-1-index].Subject)
		}
	}
}

// TestNotifyRetriesOnceThenRecordsFailure: one retry after the pause, then
// progress.failed, one line on the log, and no error for the caller.
func TestNotifyRetriesOnceThenRecordsFailure(t *testing.T) {
	sender := &fakeSender{failFor: 2}
	notifier, st, logged := newNotifier(t, sender)
	var pauses []time.Duration
	notifier.Sleep = func(delay time.Duration) { pauses = append(pauses, delay) }

	result, err := notifier.Notify(context.Background(), report("state", time.Now()))
	if err != nil {
		t.Fatalf("Notify returned %v, want nil: a dead Telegram is not a dead tick", err)
	}
	if !result.Failed || result.Sent {
		t.Fatalf("result = %+v, want Failed and not Sent", result)
	}
	if sender.attempt != 2 {
		t.Fatalf("attempts = %d, want the attempt and exactly one retry", sender.attempt)
	}
	if len(pauses) != 1 || pauses[0] != defaultRetryDelay {
		t.Fatalf("pauses = %v, want one %s", pauses, defaultRetryDelay)
	}
	if len(events(t, st, EventProgressFailed)) != 1 {
		t.Fatalf("progress.failed was not recorded")
	}
	if len(events(t, st, EventProgressSent)) != 0 {
		t.Fatalf("a failed send recorded progress.sent")
	}
	if len(*logged) != 1 || !strings.HasPrefix((*logged)[0], "alert: progress send: ") {
		t.Fatalf("log = %v, want one 'alert: progress send: …' line", *logged)
	}
}

// TestNotifySucceedsOnRetry: the retry that works is a normal send.
func TestNotifySucceedsOnRetry(t *testing.T) {
	sender := &fakeSender{failFor: 1}
	notifier, st, logged := newNotifier(t, sender)

	result, err := notifier.Notify(context.Background(), report("state", time.Now()))
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if !result.Sent || result.Failed {
		t.Fatalf("result = %+v, want Sent after the retry", result)
	}
	if sender.attempt != 2 || len(sender.bodies) != 2 {
		t.Fatalf("attempts = %d, want 2 (the failure and the retry)", sender.attempt)
	}
	if len(events(t, st, EventProgressSent)) != 1 || len(events(t, st, EventProgressFailed)) != 0 {
		t.Fatalf("ledger = %+v, want one sent and no failure", events(t, st, EventProgressSent))
	}
	if len(*logged) != 0 {
		t.Fatalf("log = %v, want quiet", *logged)
	}
}

// TestNotifyNotConfigured: a missing store or sender is a typed answer, never a
// panic and never a send.
func TestNotifyNotConfigured(t *testing.T) {
	sender := &fakeSender{}
	st := notifierStore(t)
	cases := map[string]*Notifier{
		"nil notifier": nil,
		"no store":     {Sender: sender},
		"no sender":    {Store: st},
	}
	for name, notifier := range cases {
		t.Run(name, func(t *testing.T) {
			if notifier.Configured() {
				t.Fatal("Configured = true, want false")
			}
			_, err := notifier.Notify(context.Background(), "body")
			if !errors.Is(err, ErrNotConfigured) {
				t.Fatalf("err = %v, want ErrNotConfigured", err)
			}
		})
	}
	if len(sender.bodies) != 0 {
		t.Fatalf("sends = %d, want none while unconfigured", len(sender.bodies))
	}
}

// TestNotifyCancelledContextIsNotRetried: the caller gave up during the attempt,
// so a second one would be wasted work — but the failure is still recorded.
func TestNotifyCancelledContextIsNotRetried(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sender := &cancellingSender{cancel: cancel}
	notifier, st, _ := newNotifier(t, sender)

	result, err := notifier.Notify(ctx, report("state", time.Now()))
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if !result.Failed {
		t.Fatalf("result = %+v, want Failed", result)
	}
	if sender.attempt != 1 {
		t.Fatalf("attempts = %d, want no retry for a cancelled context", sender.attempt)
	}
	if len(events(t, st, EventProgressFailed)) != 1 {
		t.Fatal("progress.failed was not recorded for the cancelled attempt")
	}
}

// cancellingSender cancels the caller's context and then fails, which is a tick
// hitting its deadline while Telegram is slow.
type cancellingSender struct {
	attempt int
	cancel  context.CancelFunc
}

func (c *cancellingSender) Send(context.Context, string) (string, error) {
	c.attempt++
	c.cancel()
	return "", context.Canceled
}

func (c *cancellingSender) Edit(context.Context, string, string) error { return nil }

// TestNotifySubjectsAreIndependent: two projects dedupe separately.
func TestNotifySubjectsAreIndependent(t *testing.T) {
	sender := &fakeSender{}
	notifier, _, _ := newNotifier(t, sender)
	body := report("state", time.Now())

	if _, err := notifier.Notify(context.Background(), body); err != nil {
		t.Fatalf("app: %v", err)
	}
	notifier.Subject = "other"
	result, err := notifier.Notify(context.Background(), body)
	if err != nil {
		t.Fatalf("other: %v", err)
	}
	if !result.Sent {
		t.Fatalf("result = %+v, want a send: another project's state is not this one's", result)
	}
	if len(sender.bodies) != 2 {
		t.Fatalf("sends = %d, want 2", len(sender.bodies))
	}
}

func TestHashProgressIgnoresTheClockOnly(t *testing.T) {
	base := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	first := report("▶ in progress (2)\n  #62 backend", base)
	later := report("▶ in progress (2)\n  #62 backend", base.Add(time.Hour))
	changed := report("▶ in progress (3)\n  #62 backend", base)

	if HashProgress(first) != HashProgress(later) {
		t.Fatal("a report that moved only its timestamp changed the hash")
	}
	if HashProgress(first) == HashProgress(changed) {
		t.Fatal("a changed bucket kept the hash")
	}
	if ProgressBody(first) != ProgressBody(later) {
		t.Fatal("ProgressBody kept the timestamp")
	}
	if strings.Contains(ProgressBody(first), base.Format(time.RFC3339)) {
		t.Fatal("ProgressBody still carries the timestamp")
	}
	// A body with no timestamp line is hashed whole.
	if ProgressBody("no clock here") != "no clock here" {
		t.Fatalf("ProgressBody = %q", ProgressBody("no clock here"))
	}
}

// TestTelegramClientIsBounded: an unset client gets the 15s bound design §5
// requires; a supplied one is used as-is.
func TestTelegramClientIsBounded(t *testing.T) {
	if got := (Telegram{}).client().Timeout; got != ClientTimeout {
		t.Fatalf("default client timeout = %s, want %s", got, ClientTimeout)
	}
	supplied := &http.Client{Timeout: time.Second}
	if (Telegram{HTTP: supplied}).client() != supplied {
		t.Fatal("the supplied client was replaced")
	}
}

// TestNotifyOverTheRealTelegramClient: the retry and the bound live in the
// transport the binary actually builds, not only in the fake. A Bot API that
// answers 502 twice produces exactly two HTTP requests, one progress.failed and
// no error out of Notify.
func TestNotifyOverTheRealTelegramClient(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"ok":false,"description":"Bad Gateway"}`))
	}))
	defer server.Close()

	st := notifierStore(t)
	var logged []string
	notifier := &Notifier{
		Store:  st,
		Sender: Telegram{Token: "t", ChatID: "c", BaseURL: server.URL, HTTP: server.Client()},
		Sleep:  func(time.Duration) {},
		Logf:   func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) },
	}
	result, err := notifier.Notify(context.Background(), report("state", time.Now()))
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if !result.Failed {
		t.Fatalf("result = %+v, want Failed", result)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("HTTP calls = %d, want 2 (the attempt and one retry)", got)
	}
	if len(events(t, st, EventProgressFailed)) != 1 {
		t.Fatal("progress.failed was not recorded")
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "Bad Gateway") {
		t.Fatalf("log = %v, want the Bot API description", logged)
	}
}

// TestNotifyOverTheRealTelegramClientSucceeds: the same client, a healthy API.
func TestNotifyOverTheRealTelegramClientSucceeds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		if body["disable_web_page_preview"] != true {
			t.Errorf("disable_web_page_preview = %v, want true", body["disable_web_page_preview"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":77}}`))
	}))
	defer server.Close()

	st := notifierStore(t)
	notifier := &Notifier{
		Store:  st,
		Sender: Telegram{Token: "t", ChatID: "c", BaseURL: server.URL, HTTP: server.Client()},
		Sleep:  func(time.Duration) {},
	}
	result, err := notifier.Notify(context.Background(), report("state", time.Now()))
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if !result.Sent || result.MessageID != "77" || result.Hash == "" {
		t.Fatalf("result = %+v, want one send of message 77", result)
	}
	stored := events(t, st, EventProgressSent)
	if len(stored) != 1 || stored[0].Detail != result.Hash+":77" {
		t.Fatalf("stored = %+v, want <hash>:77", stored)
	}
}
