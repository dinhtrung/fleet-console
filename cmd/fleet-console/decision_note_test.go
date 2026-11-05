package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/act"
	"github.com/dinhtrung/fleet-console/internal/alert"
	"github.com/dinhtrung/fleet-console/internal/config"
	"github.com/dinhtrung/fleet-console/internal/decisions"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// countingForge records comments and can fail on demand. The narrow surface the
// noter needs is decisions.Forge, so this is the whole seam.
type countingForge struct {
	mu       sync.Mutex
	comments []countingComment
	failWith error
}

type countingComment struct {
	number int
	body   string
}

func (f *countingForge) Comment(_ context.Context, number int, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return f.failWith
	}
	f.comments = append(f.comments, countingComment{number: number, body: body})
	return nil
}

func (f *countingForge) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.comments)
}

func decisionNotifier(t *testing.T, eff *config.Effective, logw *bytes.Buffer, forge decisions.Forge) *notifier {
	t.Helper()
	db, err := store.Open(context.Background(), eff.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	sender := alert.Telegram{Token: "test-token", ChatID: "42"}
	return &notifier{
		sender:        sender,
		escalator:     &alert.Escalator{Store: db, Sender: sender, BoardURL: "http://127.0.0.1:8787"},
		notes:         &decisions.Noter{Forge: forge, Store: &decisionStore{db: db}, Logf: func(string, ...any) {}},
		decisionNotes: eff.DecisionNotes,
		log:           logw,
	}
}

// Two ticks over the same open decision produce one comment: the brief's "two
// ticks → one comment" evidence, through the real notifier and the real store.
func TestDecisionNotePostedOnceAcrossTwoTicks(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "fleet.db")
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := "listen: 127.0.0.1:8787\ndatabase: " + dbPath + "\ndecision_notes: true\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	eff, err := config.Load(config.Options{FlagPath: cfgPath, Env: func(string) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	if !eff.DecisionNotes {
		t.Fatal("decision_notes did not resolve to true")
	}

	forge := &countingForge{}
	var logw bytes.Buffer
	n := decisionNotifier(t, eff, &logw, forge)

	raised := act.DecisionRaised{QItemID: "qitem-abc123", Item: 178, Text: "Should the drawer read the pane?"}
	note := decisions.NoteContext{ItemURL: "https://github.com/dinhtrung/fleet-console/issues/178", OpenOnItem: 1}

	n.noteDecision(context.Background(), raised, note, forge)
	if got := forge.count(); got != 1 {
		t.Fatalf("comments after first tick = %d, want 1", got)
	}

	n.noteDecision(context.Background(), raised, note, forge)
	if got := forge.count(); got != 1 {
		t.Fatalf("comments after second tick = %d, want 1 (the event log already says it was asked)", got)
	}

	// The comment went on the item, and carries the marker and the reply line.
	body := forge.comments[0].body
	if forge.comments[0].number != 178 {
		t.Fatalf("commented on #%d, want #178", forge.comments[0].number)
	}
	for _, want := range []string{"Should the drawer read the pane?", "fleet: accept", "<!-- fleet-console:decision qitem-abc123 -->"} {
		if !strings.Contains(body, want) {
			t.Fatalf("note body missing %q:\n%s", want, body)
		}
	}
}

// A failing forge writes one decision.note_failed per change and does not stop the
// escalation. The second identical failure appends no second event.
func TestDecisionNoteFailureIsRecordedOnceAndEscalationStillSends(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "fleet.db")
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := "listen: 127.0.0.1:8787\ndatabase: " + dbPath + "\ndecision_notes: true\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	eff, err := config.Load(config.Options{FlagPath: cfgPath, Env: func(string) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}

	forge := &countingForge{failWith: errors.New("forge: 502 bad gateway")}
	bot := newFakeBot(t)
	var logw bytes.Buffer
	n := decisionNotifier(t, eff, &logw, forge)
	// Point the escalator at the fake Bot API so "the escalation still goes out"
	// is a real send through the real code path, not a network call.
	sender := alert.Telegram{Token: "test-token", ChatID: "42", BaseURL: bot.owner.URL}
	n.escalator.Sender = sender

	raised := act.DecisionRaised{QItemID: "qitem-abc123", Item: 178, Text: "Should the drawer read the pane?"}
	note := decisions.NoteContext{ItemURL: "https://example.invalid/issues/178", OpenOnItem: 1}

	n.noteDecision(context.Background(), raised, note, forge)
	n.noteDecision(context.Background(), raised, note, forge)

	if got := forge.count(); got != 0 {
		t.Fatalf("comments while the forge fails = %d, want 0", got)
	}
	db, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	failed, err := db.Events(context.Background(), store.EventFilter{Kind: "decision.note_failed"})
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(failed.Events) != 1 {
		t.Fatalf("decision.note_failed events = %d, want 1 for one repeated change", len(failed.Events))
	}
	if !strings.Contains(logw.String(), "decision.note") {
		t.Fatalf("no log line about the failed note:\n%s", logw.String())
	}

	// The escalation is unaffected: a failed note must not stop the message.
	result, err := n.escalator.Escalate(context.Background(), alert.EscalationInput{
		DecisionID: "qitem-abc123", Project: "app", Item: 178, Question: "Should the drawer read the pane?",
		ItemURL: note.ItemURL, ReplyLine: "fleet: accept",
	})
	if err != nil {
		t.Fatalf("escalation after a failed note: %v", err)
	}
	if !result.Sent {
		t.Fatalf("escalation after a failed note = %+v, want sent", result)
	}
	if got := bot.count(); got != 1 {
		t.Fatalf("telegram sends = %d, want 1", got)
	}
}
