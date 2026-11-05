package decisions

import (
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/forge"
)

// The spec's seven cases, one table. Each row is a comment on an item with one or two open decisions.
func TestPickReply(t *testing.T) {
	asked := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	one := []Note{{Decision: Decision{ID: "qitem-a", Item: 178}, AskedAt: asked}}
	two := []Note{
		{Decision: Decision{ID: "qitem-a", Item: 178}, AskedAt: asked},
		{Decision: Decision{ID: "qitem-b", Item: 178}, AskedAt: asked},
	}
	allowed := []string{"dinhtrung"}

	tests := []struct {
		name     string
		comment  forge.Comment
		notes    []Note
		want     bool
		wantID   string
		wantText string
	}{
		{
			name:     "a valid reply resolves with the text after the prefix",
			comment:  forge.Comment{ID: "c1", Author: "dinhtrung", CreatedAt: "2026-11-05T09:00:30Z", Body: "fleet: accept"},
			notes:    one,
			want:     true,
			wantID:   "qitem-a",
			wantText: "accept",
		},
		{
			name:    "a reply from someone else resolves nothing",
			comment: forge.Comment{ID: "c2", Author: "contributor", CreatedAt: "2026-11-05T09:00:30Z", Body: "fleet: accept"},
			notes:   one,
		},
		{
			name:    "ordinary conversation resolves nothing",
			comment: forge.Comment{ID: "c3", Author: "dinhtrung", CreatedAt: "2026-11-05T09:00:30Z", Body: "looks good to me, accept it"},
			notes:   one,
		},
		{
			name:    "a comment from before the question resolves nothing",
			comment: forge.Comment{ID: "c4", Author: "dinhtrung", CreatedAt: "2026-11-04T09:00:00Z", Body: "fleet: reject"},
			notes:   one,
		},
		{
			name:    "a prefix naming an unknown decision resolves nothing",
			comment: forge.Comment{ID: "c5", Author: "dinhtrung", CreatedAt: "2026-11-05T09:00:30Z", Body: "fleet: accept qitem-zzz"},
			notes:   one,
		},
		{
			name:     "a reply naming one of two open decisions resolves that one",
			comment:  forge.Comment{ID: "c6", Author: "dinhtrung", CreatedAt: "2026-11-05T09:00:30Z", Body: "fleet: accept qitem-b"},
			notes:    two,
			want:     true,
			wantID:   "qitem-b",
			wantText: "accept qitem-b",
		},
		{
			name:    "an unnamed reply on an ambiguous item resolves nothing",
			comment: forge.Comment{ID: "c7", Author: "dinhtrung", CreatedAt: "2026-11-05T09:00:30Z", Body: "fleet: accept"},
			notes:   two,
		},
		{
			name:     "an empty author resolves nothing",
			comment:  forge.Comment{ID: "c8", Author: "", CreatedAt: "2026-11-05T09:00:30Z", Body: "fleet: accept"},
			notes:    one,
			want:     false,
			wantID:   "",
			wantText: "",
		},
		{
			name:     "the answer is capped at MaxAnswer",
			comment:  forge.Comment{ID: "c9", Author: "dinhtrung", CreatedAt: "2026-11-05T09:00:30Z", Body: "fleet: " + strings.Repeat("x", 400)},
			notes:    one,
			want:     true,
			wantID:   "qitem-a",
			wantText: strings.Repeat("x", MaxAnswer),
		},
		{
			name:     "an indented first line still counts",
			comment:  forge.Comment{ID: "c10", Author: "dinhtrung", CreatedAt: "2026-11-05T09:00:30Z", Body: "\n\n   fleet:   reject  \n"},
			notes:    one,
			want:     true,
			wantID:   "qitem-a",
			wantText: "reject",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reply, ok := PickReply(tt.comment, tt.notes, allowed)
			if ok != tt.want {
				t.Fatalf("PickReply = %v (%+v), want %v", ok, reply, tt.want)
			}
			if !tt.want {
				return
			}
			if reply.DecisionID != tt.wantID {
				t.Fatalf("DecisionID = %q, want %q", reply.DecisionID, tt.wantID)
			}
			if reply.Answer != tt.wantText {
				t.Fatalf("Answer = %q, want %q", reply.Answer, tt.wantText)
			}
			if reply.CommentID != tt.comment.ID {
				t.Fatalf("CommentID = %q, want %q", reply.CommentID, tt.comment.ID)
			}
		})
	}
}

func TestAmbiguousListsOpenIDs(t *testing.T) {
	asked := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	two := []Note{
		{Decision: Decision{ID: "qitem-b", Item: 178}, AskedAt: asked},
		{Decision: Decision{ID: "qitem-a", Item: 178}, AskedAt: asked},
	}
	comment := forge.Comment{ID: "c1", Author: "dinhtrung", CreatedAt: "2026-11-05T09:00:30Z", Body: "fleet: accept"}
	ids := Ambiguous(comment, two, []string{"dinhtrung"})
	if len(ids) != 2 || ids[0] != "qitem-a" || ids[1] != "qitem-b" {
		t.Fatalf("Ambiguous = %v, want both ids sorted", ids)
	}
	// One open decision is not ambiguous; it resolves.
	if got := Ambiguous(comment, two[:1], []string{"dinhtrung"}); got != nil {
		t.Fatalf("Ambiguous with one open = %v, want nil", got)
	}
}

func TestClosingNoteNamesAnswerAndAuthor(t *testing.T) {
	reply := Reply{Answer: "accept", Author: "dinhtrung", CommentID: "c1"}
	note := ClosingNote(reply, "https://example.invalid/c1")
	if !strings.Contains(note, "answered: accept — dinhtrung") {
		t.Fatalf("note = %q", note)
	}
	if !strings.Contains(note, "https://example.invalid/c1") {
		t.Fatalf("note does not link the comment: %q", note)
	}
	// An absent URL must not leave a dangling "from ".
	if bare := ClosingNote(reply, ""); strings.Contains(bare, "from") {
		t.Fatalf("note with no URL = %q, want no dangling from", bare)
	}
}
