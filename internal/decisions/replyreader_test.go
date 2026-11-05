package decisions

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/forge"
)

// notesForge serves an item's conversation and records closing notes.
type notesForge struct {
	mu       sync.Mutex
	comments []forge.Comment
	posted   []comment
	failWith error
}

func (f *notesForge) Notes(_ context.Context, _ int) ([]forge.Comment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return nil, f.failWith
	}
	return append([]forge.Comment(nil), f.comments...), nil
}

func (f *notesForge) Comment(_ context.Context, number int, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.posted = append(f.posted, comment{number: number, body: body})
	return nil
}

func (f *notesForge) postedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.posted)
}

func (f *notesForge) lastPosted() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.posted) == 0 {
		return ""
	}
	return f.posted[len(f.posted)-1].body
}

// stubResolver records resolutions and can refuse them as a terminal row would.
type stubResolver struct {
	mu        sync.Mutex
	resolved  []string
	decisions []string
	err       error
	attempts  int
}

func (s *stubResolver) ResolveRow(_ context.Context, id, decision string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts++
	if s.err != nil {
		return s.err
	}
	s.resolved = append(s.resolved, id)
	s.decisions = append(s.decisions, decision)
	return nil
}

func (s *stubResolver) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.resolved)
}

func noteForOne() []Note {
	asked := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	return []Note{{
		Decision: Decision{ID: "qitem-abc123", Summary: "#178 drawer", Item: 178},
		AskedAt:  asked,
	}}
}

func validComment() forge.Comment {
	return forge.Comment{
		ID:        "c-100",
		Author:    "dinhtrung",
		CreatedAt: "2026-11-05T09:00:30Z",
		Body:      "fleet: accept",
	}
}

// A valid reply resolves the row once, posts one closing note, and records decision.answered with the
// comment id (task 2.2).
func TestReadResolvesAValidReply(t *testing.T) {
	ctx := context.Background()
	fg := &notesForge{comments: []forge.Comment{validComment()}}
	st := &fakeStore{}
	rs := &stubResolver{}
	r := &ReplyReader{Forge: fg, Store: st, Resolver: rs}

	out := r.Read(ctx, noteForOne(), []string{"dinhtrung"})
	if len(out) != 1 || !out[0].Resolved {
		t.Fatalf("outcomes = %+v, want one resolved", out)
	}
	if rs.count() != 1 || rs.resolved[0] != "qitem-abc123" || rs.decisions[0] != "accept" {
		t.Fatalf("resolved = %v / %v", rs.resolved, rs.decisions)
	}
	if fg.postedCount() != 1 {
		t.Fatalf("closing notes = %d, want 1", fg.postedCount())
	}
	if body := fg.lastPosted(); !strings.Contains(body, "answered: accept — dinhtrung") {
		t.Fatalf("closing note = %q", body)
	}
	answered := st.kind("decision.answered")
	if len(answered) != 1 || !strings.Contains(answered[0].Detail, "c-100") {
		t.Fatalf("decision.answered = %+v, want one naming comment c-100", answered)
	}
}

// A row the board already answered is terminal: the resolver refuses, nothing is written to the row,
// and one decision.reply_ignored records the late comment (task 2.2, design D5).
func TestReadLeavesATerminalRowAlone(t *testing.T) {
	ctx := context.Background()
	fg := &notesForge{comments: []forge.Comment{validComment()}}
	st := &fakeStore{}
	rs := &stubResolver{err: errors.New("queue resolve: row is already done")}
	r := &ReplyReader{Forge: fg, Store: st, Resolver: rs}

	out := r.Read(ctx, noteForOne(), []string{"dinhtrung"})
	if len(out) != 1 || out[0].Resolved {
		t.Fatalf("outcomes = %+v, want one skipped", out)
	}
	if rs.count() != 0 {
		t.Fatalf("resolver recorded a resolution, want none")
	}
	if fg.postedCount() != 0 {
		t.Fatalf("closing notes = %d, want none on a terminal row", fg.postedCount())
	}
	ignored := st.kind("decision.reply_ignored")
	if len(ignored) != 1 || !strings.Contains(ignored[0].Detail, "c-100") {
		t.Fatalf("decision.reply_ignored = %+v, want one naming c-100", ignored)
	}
}

// The same comment read on the next tick resolves nothing and posts nothing (task 2.2/2.3).
func TestReadTwiceIsSilent(t *testing.T) {
	ctx := context.Background()
	fg := &notesForge{comments: []forge.Comment{validComment()}}
	st := &fakeStore{}
	rs := &stubResolver{}
	r := &ReplyReader{Forge: fg, Store: st, Resolver: rs}

	r.Read(ctx, noteForOne(), []string{"dinhtrung"})
	before := fg.postedCount()
	out := r.Read(ctx, noteForOne(), []string{"dinhtrung"})
	if rs.count() != 1 {
		t.Fatalf("resolved %d times, want 1", rs.count())
	}
	if fg.postedCount() != before {
		t.Fatalf("closing notes grew from %d to %d on a re-read", before, fg.postedCount())
	}
	if len(out) != 1 || !out[0].Skipped {
		t.Fatalf("second read outcomes = %+v, want one skipped", out)
	}
}

// A wrong author and a prefix-less comment are silence: no note, no resolution, no correction
// (design D7, first case).
func TestReadIgnoresConversation(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		comment forge.Comment
	}{
		{"wrong author", forge.Comment{ID: "c1", Author: "contributor", CreatedAt: "2026-11-05T09:00:30Z", Body: "fleet: accept"}},
		{"no prefix", forge.Comment{ID: "c2", Author: "dinhtrung", CreatedAt: "2026-11-05T09:00:30Z", Body: "looks good, accept it"}},
		{"older than the question", forge.Comment{ID: "c3", Author: "dinhtrung", CreatedAt: "2026-11-04T09:00:00Z", Body: "fleet: reject"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fg := &notesForge{comments: []forge.Comment{tt.comment}}
			st := &fakeStore{}
			rs := &stubResolver{}
			r := &ReplyReader{Forge: fg, Store: st, Resolver: rs}
			out := r.Read(ctx, noteForOne(), []string{"dinhtrung"})
			if rs.count() != 0 || fg.postedCount() != 0 {
				t.Fatalf("resolved %d, posted %d; want silence", rs.count(), fg.postedCount())
			}
			if len(out) != 0 {
				t.Fatalf("outcomes = %+v, want none", out)
			}
			if len(st.kind("decision.reply_ignored")) != 0 {
				t.Fatalf("a non-reply must not earn a correction")
			}
		})
	}
}

// An ambiguous prefixed reply earns exactly one correction, keyed by comment id, so a re-read posts
// nothing more (task 2.3, design D7 third case).
func TestReadCorrectsAmbiguityOnce(t *testing.T) {
	ctx := context.Background()
	asked := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	notes := []Note{
		{Decision: Decision{ID: "qitem-aaa", Item: 178}, AskedAt: asked},
		{Decision: Decision{ID: "qitem-bbb", Item: 178}, AskedAt: asked},
	}
	fg := &notesForge{comments: []forge.Comment{validComment()}}
	st := &fakeStore{}
	rs := &stubResolver{}
	r := &ReplyReader{Forge: fg, Store: st, Resolver: rs}

	out := r.Read(ctx, notes, []string{"dinhtrung"})
	if len(out) != 1 || !out[0].Corrected {
		t.Fatalf("outcomes = %+v, want one correction", out)
	}
	if rs.count() != 0 {
		t.Fatalf("an ambiguous reply must resolve nothing")
	}
	body := fg.lastPosted()
	if !strings.Contains(body, "qitem-aaa") || !strings.Contains(body, "qitem-bbb") {
		t.Fatalf("correction does not list both ids: %q", body)
	}
	if len(st.kind("decision.reply_ignored")) != 1 {
		t.Fatalf("decision.reply_ignored = %d, want 1", len(st.kind("decision.reply_ignored")))
	}

	// The next tick reads the same comment: no second correction.
	posted := fg.postedCount()
	again := r.Read(ctx, notes, []string{"dinhtrung"})
	if fg.postedCount() != posted {
		t.Fatalf("a re-read posted a second correction (%d → %d)", posted, fg.postedCount())
	}
	if len(again) != 1 || !again[0].Skipped {
		t.Fatalf("re-read outcomes = %+v, want one skipped", again)
	}
}

// A forge read failure is one outcome naming the error and resolves nothing.
func TestReadReportsAFailedRead(t *testing.T) {
	ctx := context.Background()
	fg := &notesForge{failWith: errors.New("forge down")}
	r := &ReplyReader{Forge: fg, Store: &fakeStore{}, Resolver: &stubResolver{}}
	out := r.Read(ctx, noteForOne(), []string{"dinhtrung"})
	if len(out) != 1 || !out[0].Skipped || !strings.Contains(out[0].Reason, "forge down") {
		t.Fatalf("outcomes = %+v, want one skipped naming the error", out)
	}
}

// A resolve that keeps failing must record one decision.reply_ignored per comment id, not one per
// tick (task 2.3). Without the ledger check this appends a second event on the next tick.
func TestReadResolveFailureRecordsOneEventPerComment(t *testing.T) {
	ctx := context.Background()
	fg := &notesForge{comments: []forge.Comment{validComment()}}
	st := &fakeStore{}
	rs := &stubResolver{err: errors.New("queue resolve: row is already done")}
	r := &ReplyReader{Forge: fg, Store: st, Resolver: rs}

	for range 3 {
		out := r.Read(ctx, noteForOne(), []string{"dinhtrung"})
		if len(out) != 1 || out[0].Resolved {
			t.Fatalf("outcomes = %+v, want one skipped", out)
		}

	}
	if ignored := st.kind("decision.reply_ignored"); len(ignored) != 1 {
		t.Fatalf("decision.reply_ignored events = %d over three ticks, want 1 per comment id", len(ignored))
	}
	// The retry must still happen: a row that frees up later has to be resolvable. The stub
	// refuses every attempt, so its own error is the proof the resolve was reached each tick.
	if rs.attempts != 3 {
		t.Fatalf("resolve attempts = %d, want 3: each tick retries the resolve", rs.attempts)
	}
	if fg.postedCount() != 0 {
		t.Fatalf("closing notes = %d, want 0 for a refused resolve", fg.postedCount())
	}
}
