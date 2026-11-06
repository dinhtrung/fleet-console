package decisions

import (
	"strings"
	"testing"
	"time"
)

// The single-decision note, byte for byte. A golden test rather than an assertion
// soup: the body is what the operator reads on a phone, and "contains fleet:" would
// pass for a body that renders the reply line as prose instead of a copyable block.
func TestRenderSingleDecision(t *testing.T) {
	d := Decision{
		ID:       "qitem-20261105062511-c947ef978a6e13a4",
		Summary:  "#178 web: the task drawer reads the dispatch seat's pane",
		Question: "Should the drawer read the pane of the seat that owns the dispatch?",
		Item:     178,
	}
	nc := NoteContext{
		ItemURL:    "https://github.com/dinhtrung/fleet-console/issues/178",
		BoardURL:   "https://board.example/decisions#qitem-20261105062511-c947ef978a6e13a4",
		OpenOnItem: 1,
	}

	body, replyLine := Render(d, nc)

	const wantReply = "fleet: accept"
	if replyLine != wantReply {
		t.Fatalf("reply line = %q, want %q", replyLine, wantReply)
	}

	const want = "**Should the drawer read the pane of the seat that owns the dispatch?**\n" +
		"\n" +
		"Reply with this line:\n" +
		"\n" +
		"```\n" +
		"fleet: accept\n" +
		"```\n" +
		"\n" +
		"Item: https://github.com/dinhtrung/fleet-console/issues/178\n" +
		"Board: https://board.example/decisions#qitem-20261105062511-c947ef978a6e13a4\n" +
		"\n" +
		"<!-- fleet-console:decision qitem-20261105062511-c947ef978a6e13a4 -->\n"
	if body != want {
		t.Fatalf("body mismatch\n--- got ---\n%q\n--- want ---\n%q", body, want)
	}
}

// Two open decisions on one item: the reply line must name the id, or "accept"
// would not say which question it answers (design D3).
func TestRenderMultipleOpenDecisionsNamesTheID(t *testing.T) {
	d := Decision{
		ID:       "qitem-20261105062511-c947ef978a6e13a4",
		Summary:  "#178 web: the task drawer reads the dispatch seat's pane",
		Question: "Should the drawer read the pane of the seat that owns the dispatch?",
		Item:     178,
	}
	nc := NoteContext{ItemURL: "https://github.com/dinhtrung/fleet-console/issues/178", OpenOnItem: 2}

	body, replyLine := Render(d, nc)

	const wantReply = "fleet: accept qitem-20261105062511-c947ef978a6e13a4"
	if replyLine != wantReply {
		t.Fatalf("reply line = %q, want %q", replyLine, wantReply)
	}
	if !strings.Contains(body, wantReply) {
		t.Fatalf("body does not carry the reply line %q:\n%s", wantReply, body)
	}
	// The marker must be the qitem id, and must be the invisible comment form.
	if !strings.Contains(body, "<!-- fleet-console:decision qitem-20261105062511-c947ef978a6e13a4 -->") {
		t.Fatalf("body does not carry the decision marker:\n%s", body)
	}
}

// Options appear when the decision has them, and are absent when it does not.
func TestRenderOptionsOnlyWhenPresent(t *testing.T) {
	base := Decision{ID: "q1", Question: "Which?", Item: 7}
	nc := NoteContext{OpenOnItem: 1}

	without, _ := Render(base, nc)
	if strings.Contains(without, "Options:") {
		t.Fatalf("a decision with no options rendered an Options block:\n%s", without)
	}

	with := base
	with.Options = []string{"accept", "reject"}
	body, _ := Render(with, nc)
	if !strings.Contains(body, "Options:\n- accept\n- reject\n") {
		t.Fatalf("options missing or misordered:\n%s", body)
	}
}

// A decision with no question falls back to the row summary, and with neither it
// still renders something an operator can act on rather than an empty comment.
func TestRenderQuestionFallbacks(t *testing.T) {
	nc := NoteContext{OpenOnItem: 1}

	fromSummary, _ := Render(Decision{ID: "q1", Summary: "#7 needs a decision", Item: 7}, nc)
	if !strings.Contains(fromSummary, "#7 needs a decision") {
		t.Fatalf("summary fallback missing:\n%s", fromSummary)
	}

	empty, _ := Render(Decision{ID: "q1", Item: 7}, nc)
	if !strings.Contains(empty, "a decision is needed on this item") {
		t.Fatalf("empty decision rendered no question:\n%s", empty)
	}
}

// The note must not repeat the item's own text: no title, no body, no labels.
// The summary fallback is the one exception, and only when there is no question.
func TestRenderDoesNotRepeatTheItem(t *testing.T) {
	d := Decision{
		ID:       "q1",
		Question: "Ship it?",
		Summary:  "#9 a long item summary that should not be echoed",
		Item:     9,
	}
	body, _ := Render(d, NoteContext{OpenOnItem: 1})
	if strings.Contains(body, "should not be echoed") {
		t.Fatalf("body repeated the row summary despite having a question:\n%s", body)
	}
}

func TestItemNumber(t *testing.T) {
	tests := []struct {
		name   string
		raised int
		text   string
		want   int
	}{
		{name: "the raised number wins", raised: 178, text: "#999 ignored", want: 178},
		{name: "falls back to the summary", raised: 0, text: "#157 act: stop an idle rig", want: 157},
		{name: "closes form", raised: 0, text: "Closes #158 mirror: back off", want: 158},
		{name: "trailing colon", raised: 0, text: "#178 web: the drawer", want: 178},
		{name: "no number at all", raised: 0, text: "a row with no item", want: 0},
		{name: "leading zeros are not a number", raised: 0, text: "#007 not an item", want: 0},
		{name: "a number later in the text", raised: 0, text: "see also #42 for context", want: 42},
		{name: "empty", raised: 0, text: "", want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ItemNumber(tc.raised, tc.text); got != tc.want {
				t.Fatalf("ItemNumber(%d, %q) = %d, want %d", tc.raised, tc.text, got, tc.want)
			}
		})
	}
}

// The marker must carry the decision id, not the item number: two decisions on one
// item are told apart by the id.
func TestRenderMarkerCarriesTheDecisionID(t *testing.T) {
	body, _ := Render(Decision{ID: "qitem-abc123", Question: "Which?", Item: 5}, NoteContext{OpenOnItem: 1})
	if !strings.Contains(body, "<!-- fleet-console:decision qitem-abc123 -->") {
		t.Fatalf("marker does not carry the decision id:\n%s", body)
	}
	if strings.Contains(body, "decision 5") {
		t.Fatalf("marker carries the item number instead of the id:\n%s", body)
	}
}

// A deadline, when present, is shown so the operator knows the urgency. Absent
// means no line, not "no deadline" boilerplate in every note.
func TestRenderDeadlineOnlyWhenPresent(t *testing.T) {
	nc := NoteContext{OpenOnItem: 1}
	d := Decision{ID: "q1", Question: "Which?", Item: 3}

	without, _ := Render(d, nc)
	if strings.Contains(without, "Deadline") {
		t.Fatalf("no deadline but the note mentions one:\n%s", without)
	}

	d.Deadline = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	with, _ := Render(d, nc)
	if !strings.Contains(with, "Deadline: 2026-11-05T12:00:00Z") {
		t.Fatalf("deadline missing:\n%s", with)
	}
}
