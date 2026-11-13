package act

import (
	"context"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/rig"
)

// TestRaisedVerdictQuestionIsReadableFromTheLedger (#377): the board lists the
// question from the ledger, so the words the raiser writes are the words the
// store's question query reads. This test is the join between them: a real
// raise, then UnansweredQuestions, with no hand-written detail in between.
func TestRaisedVerdictQuestionIsReadableFromTheLedger(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)
	queue := &queueStub{item: &rig.QueueItem{ID: "qitem-42", State: "pending"}, created: &rig.CreateRequest{}}
	actions := &Actions{Store: st, Rig: queue, ProjectID: pid, ProjectKey: "app", WorktreeRoot: t.TempDir()}

	_, err := actions.RaiseVerdictQuestion(ctx, "app", 63, 75,
		VerdictQuestion{NoteID: "n1", Head: "aaa111", Reason: "reads like a verdict but names no commit"},
		"operator-agent@kernel", actions.WorktreeRoot)
	if err != nil {
		t.Fatalf("raise: %v", err)
	}

	rows, err := st.UnansweredQuestions(ctx, pid, "app")
	if err != nil {
		t.Fatalf("questions: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want the raised question", rows)
	}
	got := rows[0]
	if got.Item != 63 || got.ChangeRequest != 75 || got.NoteID != "n1" || got.Head != "aaa111" ||
		got.Destination != "operator-agent@kernel" || got.QItemID != "qitem-42" {
		t.Fatalf("row = %+v, want the raise's own fields", got)
	}
	if got.At == "" {
		t.Fatal("row carries no timestamp; the board cannot say how long it waited")
	}
}
