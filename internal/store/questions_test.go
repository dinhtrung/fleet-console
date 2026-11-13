package store

import (
	"context"
	"testing"
	"time"
)

// questionDetail is the text act.RaiseVerdictQuestion writes, verbatim. The
// query reads the ledger's own words, so the test states them the same way.
func questionDetail(project string, item, cr int, note, dest, head, row string) string {
	return project + " #" + itoaStore(item) + ": note " + note + " on change request #" + itoaStore(cr) +
		" cannot be read as a verdict (reads like a verdict but names no commit); asked " + dest +
		"; note=" + note + "; head=" + head + "; row " + row
}

func itoaStore(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

// TestUnansweredQuestionsReadsTheLedger (#377): the list is the ledger's
// verdict questions for one project, joined with the rig_queue snapshot — a
// finished row drops out, a row the snapshot does not hold stays, and another
// project's question never appears.
func TestUnansweredQuestionsReadsTheLedger(t *testing.T) {
	ctx := context.Background()
	st := upsertStore(t)
	_, id, err := st.UpsertProject(ctx, baseUpsert())
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	now := time.Now().UTC()
	events := []Event{
		{Actor: "act", Kind: "decision.verdict_question", Subject: "#63", Detail: questionDetail("app", 63, 75, "n1", "human@kernel", "aaa111", "qitem-open")},
		{Actor: "act", Kind: "decision.verdict_question", Subject: "#64", Detail: questionDetail("app", 64, 76, "n2", "operator-agent@kernel", "bbb222", "qitem-done")},
		{Actor: "act", Kind: "decision.verdict_question", Subject: "#63", Detail: questionDetail("other", 63, 90, "n3", "human@kernel", "ccc333", "qitem-other")},
		// A detail the vocabulary does not match is skipped, never guessed at.
		{Actor: "act", Kind: "decision.verdict_question", Subject: "#65", Detail: "app #65: something else entirely"},
		// Another kind on the same project is not a question.
		{Actor: "act", Kind: "decision.no_item", Subject: "cr-90", Detail: "cr=90; head=ddd; named=63; state=open; row qitem-x"},
	}
	for _, event := range events {
		if _, err := st.AppendEvent(ctx, event); err != nil {
			t.Fatalf("event: %v", err)
		}
	}

	// No snapshot yet: both of app's questions survive, without a row state.
	rows, err := st.UnansweredQuestions(ctx, id, "app")
	if err != nil {
		t.Fatalf("questions: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want two questions without a snapshot", rows)
	}
	if rows[0].Item != 63 || rows[0].ChangeRequest != 75 || rows[0].NoteID != "n1" ||
		rows[0].Head != "aaa111" || rows[0].Destination != "human@kernel" || rows[0].QItemID != "qitem-open" ||
		rows[0].RowState != "" {
		t.Fatalf("row = %+v, want the ledger's fields and no row state", rows[0])
	}

	// With the snapshot: the finished row drops out, the open one stays.
	if err := st.PutSnapshot(ctx, id, "rig_queue", []map[string]string{
		{"id": "qitem-open", "state": "pending", "pickup_state": "unclaimed"},
		{"id": "qitem-done", "state": "done", "pickup_state": "terminal"},
	}, now); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	rows, err = st.UnansweredQuestions(ctx, id, "app")
	if err != nil {
		t.Fatalf("questions: %v", err)
	}
	if len(rows) != 1 || rows[0].QItemID != "qitem-open" || rows[0].RowState != "pending" {
		t.Fatalf("rows = %+v, want only the open question with its state", rows)
	}

	empty, err := st.UnansweredQuestions(ctx, id, "nothing-here")
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty = %+v (%v), want none", empty, err)
	}
}
