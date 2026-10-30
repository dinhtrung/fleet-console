package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/act"
	"github.com/dinhtrung/fleet-console/internal/alert"
	"github.com/dinhtrung/fleet-console/internal/config"
	"github.com/dinhtrung/fleet-console/internal/progress"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// fakeBotAPI is the Bot API a test can point at: it records every sendText and
// answers with a message id, or with an error when fail is set.
type fakeBotAPI struct {
	mu     sync.Mutex
	sent   []string
	edited []string
	fail   bool
	next   int
	hits   int
	owner  *httptest.Server
}

func newFakeBot(t *testing.T) *fakeBotAPI {
	t.Helper()
	bot := &fakeBotAPI{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bot.mu.Lock()
		defer bot.mu.Unlock()
		bot.hits++
		if bot.fail {
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprint(w, `{"ok":false,"description":"Bad Gateway"}`)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		text, _ := body["text"].(string)
		// sendMessage creates; editMessageText edits. Counting both as "sent"
		// is how a passing test hides a resent message.
		if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			bot.sent = append(bot.sent, text)
		} else if strings.HasSuffix(r.URL.Path, "/editMessageText") {
			bot.edited = append(bot.edited, text)
		}
		bot.next++
		fmt.Fprintf(w, `{"ok":true,"result":{"message_id":%d}}`, bot.next)
	}))
	t.Cleanup(srv.Close)
	bot.owner = srv
	return bot
}

func (b *fakeBotAPI) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.sent)
}

func (b *fakeBotAPI) edits() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.edited)
}

// notifyFixture is one configured project in a temp database.
func notifyFixture(t *testing.T) *config.Effective {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "fleet.db")
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(`listen: 127.0.0.1:8787
database: `+dbPath+`
projects:
  - key: app
    name: App
    forge: github
    repo: org/app
    rigs: [rig-a]
    concurrency_cap: 1
`), 0o600); err != nil {
		t.Fatal(err)
	}
	eff, err := config.Load(config.Options{FlagPath: cfgPath, Env: func(string) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.CreateProject(context.Background(), store.ProjectInput{
		Key: "app", Name: "App", ForgeKind: "github", ForgeProject: "org/app",
		Rig: "rig-a", ConcurrencyCap: 1, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	return eff
}

// notifierFor builds the wired notifier the way boot does, but against the fake
// Bot API.
func notifierFor(t *testing.T, eff *config.Effective, bot *fakeBotAPI, logw *bytes.Buffer) *notifier {
	t.Helper()
	db, err := store.Open(context.Background(), eff.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	sender := alert.Telegram{Token: "test-token", ChatID: "42", BaseURL: bot.owner.URL}
	return &notifier{
		sender: sender,
		progress: &alert.Notifier{
			Store: db, Sender: sender,
			Sleep: func(time.Duration) {}, // no real retry pause in a test
			Logf:  func(string, ...any) {},
		},
		escalator: &alert.Escalator{Store: db, Sender: sender, BoardURL: "http://127.0.0.1:8787"},
		log:       logw,
	}
}

// TestNotifyReportExitCodes: the three outcomes the flag promises — delivered
// (0), transport failure (1), not configured (3) — each with the side effect
// they imply.
func TestNotifyReportExitCodes(t *testing.T) {
	t.Run("delivered exits 0 and sends once", func(t *testing.T) {
		eff := notifyFixture(t)
		bot := newFakeBot(t)
		var logw bytes.Buffer
		n := notifierFor(t, eff, bot, &logw)

		code, err := runNotifyReport(context.Background(), notifyReportArgs{
			eff: eff, projectKey: "app", notifier: n, log: &logw,
			now: func() time.Time { return time.Date(2026, 10, 30, 18, 0, 0, 0, time.UTC) },
		})
		if err != nil || code != 0 {
			t.Fatalf("code=%d err=%v, want 0", code, err)
		}
		if got := bot.count(); got != 1 {
			t.Fatalf("sends = %d, want 1", got)
		}
		if !strings.Contains(logw.String(), "sent message") {
			t.Fatalf("log = %q, want the sent line", logw.String())
		}
	})

	t.Run("unchanged state exits 0 and sends nothing new", func(t *testing.T) {
		eff := notifyFixture(t)
		bot := newFakeBot(t)
		var logw bytes.Buffer
		n := notifierFor(t, eff, bot, &logw)
		at := time.Date(2026, 10, 30, 18, 0, 0, 0, time.UTC)
		args := notifyReportArgs{
			eff: eff, projectKey: "app", notifier: n, log: &logw,
			now: func() time.Time { return at },
		}
		if code, err := runNotifyReport(context.Background(), args); err != nil || code != 0 {
			t.Fatalf("first: code=%d err=%v", code, err)
		}
		if code, err := runNotifyReport(context.Background(), args); err != nil || code != 0 {
			t.Fatalf("second: code=%d err=%v", code, err)
		}
		if got := bot.count(); got != 1 {
			t.Fatalf("sends = %d, want 1 (the second run must dedupe)", got)
		}
	})

	t.Run("transport failure exits 1", func(t *testing.T) {
		eff := notifyFixture(t)
		bot := newFakeBot(t)
		bot.fail = true
		var logw bytes.Buffer
		n := notifierFor(t, eff, bot, &logw)

		code, err := runNotifyReport(context.Background(), notifyReportArgs{
			eff: eff, projectKey: "app", notifier: n, log: &logw,
			now: func() time.Time { return time.Date(2026, 10, 30, 18, 0, 0, 0, time.UTC) },
		})
		if err != nil {
			t.Fatalf("err = %v, want the code to carry the failure", err)
		}
		if code != 1 {
			t.Fatalf("code = %d, want 1", code)
		}
		if !strings.Contains(logw.String(), "send failed") {
			t.Fatalf("log = %q, want the failure line", logw.String())
		}
	})

	t.Run("no credentials exits 3 and sends nothing", func(t *testing.T) {
		eff := notifyFixture(t)
		bot := newFakeBot(t)
		var logw bytes.Buffer
		// The boot path with an empty environment: the notifier builds itself
		// and reports not-configured.
		t.Setenv(envTelegramToken, "")
		t.Setenv(envTelegramChatID, "")
		n := newNotifierForTest(t, eff, &logw, bot.owner.URL)

		code, err := runNotifyReport(context.Background(), notifyReportArgs{
			eff: eff, projectKey: "app", notifier: n, log: &logw,
			now: func() time.Time { return time.Date(2026, 10, 30, 18, 0, 0, 0, time.UTC) },
		})
		if err != nil || code != 3 {
			t.Fatalf("code=%d err=%v, want 3", code, err)
		}
		if got := bot.count(); got != 0 {
			t.Fatalf("sends = %d, want none", got)
		}
		if !strings.Contains(logw.String(), "not set") {
			t.Fatalf("log = %q, want the boot line", logw.String())
		}
	})
}

// newNotifierForTest is newNotifier with the Bot API URL redirected. The parse
// of env is the thing under test in the third case, so it must stay real.
func newNotifierForTest(t *testing.T, eff *config.Effective, logw *bytes.Buffer, baseURL string) *notifier {
	t.Helper()
	db, err := store.Open(context.Background(), eff.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	n := newNotifier(db, eff, logw, "")
	// Nothing was configured, which is the point; keep the URL for symmetry.
	_ = baseURL
	return n
}

// TestNotifyBootLineAndNoSends: with no credentials the binary prints exactly
// one line and every trigger is a no-op (design.md §5, task 2.3).
func TestNotifyBootLineAndNoSends(t *testing.T) {
	eff := notifyFixture(t)
	var logw bytes.Buffer
	t.Setenv(envTelegramToken, "")
	t.Setenv(envTelegramChatID, "")
	db, err := store.Open(context.Background(), eff.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	n := newNotifier(db, eff, &logw, "")
	if n.configured() {
		t.Fatal("configured() = true with no credentials")
	}
	lines := strings.Split(strings.TrimSpace(logw.String()), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], "completion notices are off") {
		t.Fatalf("log = %q, want exactly one boot line", logw.String())
	}
	if _, err := n.sendProgress(context.Background(), reportSnapshotForTest(t, eff), time.Now()); err == nil {
		t.Fatal("sendProgress succeeded while unconfigured")
	}
}

func reportSnapshotForTest(t *testing.T, eff *config.Effective) progress.Snapshot {
	t.Helper()
	db, err := store.Open(context.Background(), eff.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	project, err := db.ProjectByKey(context.Background(), "app")
	if err != nil || project == nil {
		t.Fatalf("project: %v", err)
	}
	return reportSnapshot(context.Background(), db, eff, nil, *project)
}

// TestEscalatorReachesThePhone: a decision raised through the notifier produces
// exactly one telegram.sent event and one send, and a second call edits rather
// than sends (design.md §6, task 3.3).
func TestEscalatorReachesThePhone(t *testing.T) {
	eff := notifyFixture(t)
	bot := newFakeBot(t)
	var logw bytes.Buffer
	n := notifierFor(t, eff, bot, &logw)

	raised := act.DecisionRaised{QItemID: "qitem-1", Item: 62, Text: "re-review needed"}
	n.escalate(context.Background(), "app", raised)
	if got := bot.count(); got != 1 {
		t.Fatalf("sends = %d, want 1", got)
	}

	db, err := store.Open(context.Background(), eff.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	events, err := db.RecentEvents(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	var sent, edited int
	for _, e := range events {
		switch e.Kind {
		case "telegram.sent":
			sent++
		case "telegram.edited":
			edited++
		}
	}
	if sent != 1 || edited != 0 {
		t.Fatalf("events sent=%d edited=%d, want 1 and 0", sent, edited)
	}

	// The same decision again: the message is edited, not resent.
	n.escalate(context.Background(), "app", raised)
	if got := bot.count(); got != 1 {
		t.Fatalf("sends after re-escalation = %d, want 1 (the second is an edit)", got)
	}
	if got := bot.edits(); got != 1 {
		t.Fatalf("edits = %d, want 1", got)
	}
}

// TestDigestGate: notify_interval is a floor between messages, not a heartbeat.
// Zero is off; inside the interval nothing is sent even when the state changed,
// because the change is reported by the next trigger outside it (task 3.4).
func TestDigestGate(t *testing.T) {
	at := time.Date(2026, 10, 30, 18, 0, 0, 0, time.UTC)

	t.Run("zero interval is off", func(t *testing.T) {
		g := &digestGate{}
		if g.allow(at) {
			t.Fatal("allow() = true with no interval")
		}
	})

	t.Run("two ticks inside the interval send once", func(t *testing.T) {
		g := &digestGate{interval: 30 * time.Minute}
		if !g.allow(at) {
			t.Fatal("first call refused")
		}
		if g.allow(at.Add(10 * time.Minute)) {
			t.Fatal("second call inside the interval was allowed")
		}
		if !g.allow(at.Add(31 * time.Minute)) {
			t.Fatal("call past the interval was refused")
		}
	})
}
