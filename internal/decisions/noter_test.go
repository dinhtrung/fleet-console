package decisions

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// fakeForge counts comments and can fail on demand.
type fakeForge struct {
	mu       sync.Mutex
	comments []comment
	failWith error
}

type comment struct {
	number int
	body   string
}

func (f *fakeForge) Comment(_ context.Context, number int, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return f.failWith
	}
	f.comments = append(f.comments, comment{number: number, body: body})
	return nil
}

func (f *fakeForge) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.comments)
}

// fakeStore is the event ledger: enough of HasEvent/AppendEvent to prove the
// idempotency index without a database.
type fakeStore struct {
	mu     sync.Mutex
	events []Event
}

func (s *fakeStore) HasEvent(_ context.Context, kind, subject, contains string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.events {
		if e.Kind == kind && e.Subject == subject && containsSub(e.Detail, contains) {
			return true, nil
		}
	}
	return false, nil
}

func (s *fakeStore) AppendEvent(_ context.Context, e Event) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	return int64(len(s.events)), nil
}

func (s *fakeStore) kind(kind string) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Event
	for _, e := range s.events {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func containsSub(s, sub string) bool {
	if sub == "" {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func testDecision() Decision {
	return Decision{
		ID:       "qitem-abc123",
		Summary:  "#178 web: the drawer",
		Question: "Should the drawer read the pane?",
		Item:     178,
	}
}

// Two ticks over the same open decision produce one comment: the event log is the
// index (task 1.2).
func TestNoteTwicePostsOnce(t *testing.T) {
	ctx := context.Background()
	forge := &fakeForge{}
	store := &fakeStore{}
	n := &Noter{Forge: forge, Store: store}
	nc := NoteContext{ItemURL: "https://example.invalid/issues/178", OpenOnItem: 1}

	first, err := n.Note(ctx, testDecision(), nc)
	if err != nil {
		t.Fatalf("first note: %v", err)
	}
	if !first.Posted || first.Skipped {
		t.Fatalf("first note = %+v, want posted", first)
	}

	second, err := n.Note(ctx, testDecision(), nc)
	if err != nil {
		t.Fatalf("second note: %v", err)
	}
	if second.Posted || !second.Skipped || second.Reason != "already noted" {
		t.Fatalf("second note = %+v, want skipped as already noted", second)
	}

	if got := forge.count(); got != 1 {
		t.Fatalf("comments = %d, want 1", got)
	}
	noted := store.kind("decision.noted")
	if len(noted) != 1 {
		t.Fatalf("decision.noted events = %d, want 1", len(noted))
	}
	if noted[0].Subject != "#178" || !containsSub(noted[0].Detail, "qitem-abc123") {
		t.Fatalf("event = %+v, want subject #178 and the decision id", noted[0])
	}
}

// A failed post writes one decision.note_failed and is retried on the next call.
func TestNoteFailureRetries(t *testing.T) {
	ctx := context.Background()
	forge := &fakeForge{failWith: errors.New("forge: 502 bad gateway")}
	store := &fakeStore{}
	var logged []string
	n := &Noter{
		Forge: forge, Store: store,
		Logf: func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) },
	}
	nc := NoteContext{OpenOnItem: 1}

	if _, err := n.Note(ctx, testDecision(), nc); err == nil {
		t.Fatal("first note should fail")
	}
	if got := len(store.kind("decision.note_failed")); got != 1 {
		t.Fatalf("note_failed events after one failure = %d, want 1", got)
	}
	if got := len(store.kind("decision.noted")); got != 0 {
		t.Fatalf("decision.noted after a failure = %d, want 0", got)
	}

	// Second tick, forge still broken: another attempt is made, but the identical
	// failure is the same "change" and must not append a second event.
	if _, err := n.Note(ctx, testDecision(), nc); err == nil {
		t.Fatal("second note should also fail")
	}
	if got := len(store.kind("decision.note_failed")); got != 1 {
		t.Fatalf("note_failed events after two identical failures = %d, want 1", got)
	}

	// The forge recovers: the next tick posts and records the success.
	forge.mu.Lock()
	forge.failWith = nil
	forge.mu.Unlock()

	result, err := n.Note(ctx, testDecision(), nc)
	if err != nil {
		t.Fatalf("note after recovery: %v", err)
	}
	if !result.Posted {
		t.Fatalf("note after recovery = %+v, want posted", result)
	}
	if got := forge.count(); got != 1 {
		t.Fatalf("comments after recovery = %d, want 1", got)
	}
	if got := len(store.kind("decision.noted")); got != 1 {
		t.Fatalf("decision.noted after recovery = %d, want 1", got)
	}
}

// No item number means no note and one log line, never a guess (task 1.3).
func TestNoteWithoutItemNumberPostsNothing(t *testing.T) {
	ctx := context.Background()
	forge := &fakeForge{}
	store := &fakeStore{}
	var logged []string
	n := &Noter{
		Forge: forge, Store: store,
		Logf: func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) },
	}

	d := Decision{ID: "qitem-abc123", Question: "Which?", Summary: "a row with no number"}
	result, err := n.Note(ctx, d, NoteContext{OpenOnItem: 1})
	if err != nil {
		t.Fatalf("note: %v", err)
	}
	if !result.Skipped || result.Reason != "no item number" {
		t.Fatalf("result = %+v, want skipped for no item number", result)
	}
	if forge.count() != 0 {
		t.Fatalf("comments = %d, want 0", forge.count())
	}
	if len(store.events) != 0 {
		t.Fatalf("events = %+v, want none", store.events)
	}
	if len(logged) != 1 {
		t.Fatalf("log lines = %d (%v), want 1", len(logged), logged)
	}
}

// The decision's item number resolves from the row summary when the app did not
// raise it, so a hand-created row still gets a note on the right item.
func TestNoteFallsBackToTheRowSummary(t *testing.T) {
	ctx := context.Background()
	forge := &fakeForge{}
	store := &fakeStore{}
	n := &Noter{Forge: forge, Store: store}

	d := Decision{
		ID: "qitem-abc123", Question: "Which?",
		Summary: "#157 act: stop an idle rig", Item: ItemNumber(0, "#157 act: stop an idle rig"),
	}
	result, err := n.Note(ctx, d, NoteContext{OpenOnItem: 1})
	if err != nil {
		t.Fatalf("note: %v", err)
	}
	if !result.Posted {
		t.Fatalf("result = %+v, want posted", result)
	}
	if got := forge.count(); got != 1 {
		t.Fatalf("comments = %d, want 1", got)
	}
	if forge.comments[0].number != 157 {
		t.Fatalf("commented on #%d, want #157", forge.comments[0].number)
	}
}
