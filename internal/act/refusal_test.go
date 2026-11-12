package act

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// forgePermissionRefusal is the forge's own sentence the permission raise
// carries: the credential may not write.
var forgePermissionRefusal = forge.PermissionError{
	Operation: "merge", Forge: "github",
	Detail: "gh: HTTP 403: Resource not accessible by personal access token",
}

// refusalEvidence runs one human raise against a recording stub and returns
// the create it recorded. Every raise test here asserts the same contract:
// exactly one create, and its EvidenceRef is the brief path the body was
// written to — the value a human judges the row by (epic #281, C3).
func refusalEvidence(t *testing.T, created **rig.CreateRequest, run func(a *Actions) error) {
	t.Helper()
	st := newStore(t)
	queue := &queueStub{created: &rig.CreateRequest{}}
	actions := &Actions{Store: st, Rig: queue, ProjectID: 1, ProjectKey: "app", WorktreeRoot: t.TempDir()}
	if err := run(actions); err != nil {
		t.Fatalf("raise: %v", err)
	}
	if queue.createCalls != 1 {
		t.Fatalf("queue creates = %d, want 1", queue.createCalls)
	}
	*created = queue.created
	if *created != queue.created {
		t.Fatal("recording stub was not the one the raise used")
	}
	ref := (*created).EvidenceRef
	if ref == "" || ref != (*created).BodyFile {
		t.Fatalf("evidence_ref = %q, want the brief path %q", ref, (*created).BodyFile)
	}
	if _, err := os.Stat(ref); err != nil {
		t.Fatalf("evidence path %s does not exist: %v", ref, err)
	}
}

// TestRaiseOperatorQuestionCarriesEvidence: the operator question's row names
// its brief as evidence.
func TestRaiseOperatorQuestionCarriesEvidence(t *testing.T) {
	var created *rig.CreateRequest
	refusalEvidence(t, &created, func(a *Actions) error {
		project := store.ProjectRow{ID: a.ProjectID, Key: "app", ForgeKind: "github", ForgeProject: "o/r"}
		_, err := a.RaiseOperatorQuestion(context.Background(), project, 5, noVerdictQuestion,
			"decision.no_verdict", "AAAA111", a.WorktreeRoot)
		return err
	})
	if !strings.Contains(created.EvidenceRef, "operator-app-5-") {
		t.Fatalf("evidence_ref = %q, want the operator question's brief name", created.EvidenceRef)
	}
}

// TestRaisePermissionDecisionCarriesEvidence: the forge-permission row names
// its brief as evidence.
func TestRaisePermissionDecisionCarriesEvidence(t *testing.T) {
	var created *rig.CreateRequest
	refusalEvidence(t, &created, func(a *Actions) error {
		_, err := a.RaisePermissionDecision(context.Background(), PermissionInput{
			Item: 5, Title: "Merge", Operation: "merge",
			Refusal: &forgePermissionRefusal, Attempt: 1, CR: 20, SHA: "aaaa111", ProjectKey: "app",
		})
		return err
	})
	// The permission brief's name slugs the raise's dedupe key, not the
	// project key: permission-<attempt-operation-detail>-<item>-<operation>.md.
	if base := filepath.Base(created.EvidenceRef); !strings.HasPrefix(base, "permission-") || !strings.Contains(base, "-5-merge.md") {
		t.Fatalf("evidence_ref = %q, want the permission brief name", created.EvidenceRef)
	}
}

// TestRaiseSilentDispatchCarriesEvidence: the silent-dispatch row names its
// brief as evidence.
func TestRaiseSilentDispatchCarriesEvidence(t *testing.T) {
	var created *rig.CreateRequest
	refusalEvidence(t, &created, func(a *Actions) error {
		_, err := a.RaiseSilentDispatch(context.Background(), SilentDispatchInput{
			Item: 5, Title: "One", Seat: "dev-build-1@rig", Branch: "feature/5",
			Worktree: "/tmp/wt", Attempt: 1, Age: 30 * time.Minute, Repo: "o/r", ProjectKey: "app",
		})
		return err
	})
	if !strings.Contains(created.EvidenceRef, "silent-app-5-attempt1") {
		t.Fatalf("evidence_ref = %q, want the silent-dispatch brief name", created.EvidenceRef)
	}
}

// TestRaiseMergeRepairCarriesEvidence: the merge-repair row names its brief as
// evidence.
func TestRaiseMergeRepairCarriesEvidence(t *testing.T) {
	var created *rig.CreateRequest
	refusalEvidence(t, &created, func(a *Actions) error {
		_, err := a.RaiseMergeRepair(context.Background(), "app",
			&NotMergeableError{Number: 20, Item: 5, SHA: "aaaa111", Branch: "feature/5", Reason: "dirty"},
			a.WorktreeRoot)
		return err
	})
	if !strings.Contains(created.EvidenceRef, "merge-repair-app-5-") {
		t.Fatalf("evidence_ref = %q, want the merge-repair brief name", created.EvidenceRef)
	}
}

// TestAdoptionDecisionCarriesEvidence: the ambiguous-dispatch row names its
// brief as evidence. raiseAdoptionDecision is a free function, so the store
// rides in as a parameter — the refusal ledger needs it too.
func TestAdoptionDecisionCarriesEvidence(t *testing.T) {
	st := newStore(t)
	queue := &queueStub{created: &rig.CreateRequest{}}
	project := store.ProjectRow{ID: 1, Key: "app"}
	dispatch := store.DispatchRow{ID: 7, ProjectID: 1, ItemNumber: 5, Attempt: 1, Branch: "feature/5"}
	matches := []rig.QueueRow{{ID: "qitem-a"}, {ID: "qitem-b"}}
	row, err := raiseAdoptionDecision(context.Background(), st, queue, project, dispatch, matches, t.TempDir())
	if err != nil {
		t.Fatalf("raise: %v", err)
	}
	if row == nil {
		t.Fatal("row = nil, want the ambiguity decision")
	}
	if queue.createCalls != 1 {
		t.Fatalf("queue creates = %d, want 1", queue.createCalls)
	}
	created := queue.created
	if created.EvidenceRef == "" || created.EvidenceRef != created.BodyFile {
		t.Fatalf("evidence_ref = %q, want the brief path %q", created.EvidenceRef, created.BodyFile)
	}
	if !strings.Contains(created.EvidenceRef, "ambiguous-app-5") {
		t.Fatalf("evidence_ref = %q, want the ambiguity brief name", created.EvidenceRef)
	}
}

// TestNoteRefusal: one `queue.refused` event per (item, code) — the same code
// refused twice is one row, a different code on the same item is a second row,
// and a non-refusal error writes nothing. The needle's `;` terminator keeps
// `code=human_route_fields_required;` from matching a longer code.
func TestNoteRefusal(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	refusal := func(code string) error {
		return &rig.RefusalError{Code: code, Missing: []string{"evidence_ref"}, Command: "queue create"}
	}
	refusedEvents := func() []store.EventRow {
		t.Helper()
		events, err := st.EventsForItem(ctx, 5, 20)
		if err != nil {
			t.Fatalf("events: %v", err)
		}
		var out []store.EventRow
		for _, event := range events {
			if event.Kind == "queue.refused" {
				out = append(out, event)
			}
		}
		return out
	}

	if err := noteRefusal(ctx, st, 5, refusal("human_route_fields_required")); err != nil {
		t.Fatalf("first noteRefusal: %v", err)
	}
	if err := noteRefusal(ctx, st, 5, refusal("human_route_fields_required")); err != nil {
		t.Fatalf("second noteRefusal: %v", err)
	}
	events := refusedEvents()
	if len(events) != 1 {
		t.Fatalf("queue.refused events = %d, want exactly 1 across two calls with the same code", len(events))
	}
	if events[0].Actor != "act" || events[0].Subject != "#5" {
		t.Fatalf("event = actor %q subject %q, want act / #5", events[0].Actor, events[0].Subject)
	}
	if events[0].Detail != "code=human_route_fields_required; missing=evidence_ref" {
		t.Fatalf("detail = %q, want code and missing rendered", events[0].Detail)
	}

	if err := noteRefusal(ctx, st, 5, refusal("human_route_fields_required_later")); err != nil {
		t.Fatalf("different code: %v", err)
	}
	events = refusedEvents()
	if len(events) != 2 {
		t.Fatalf("queue.refused events = %d, want a second row for the different code", len(events))
	}

	if err := noteRefusal(ctx, st, 5, errors.New("plain failure")); err != nil {
		t.Fatalf("non-refusal error: %v", err)
	}
	if err := noteRefusal(ctx, nil, 5, refusal("human_route_fields_required")); err != nil {
		t.Fatalf("nil store: %v", err)
	}
	if got := refusedEvents(); len(got) != 2 {
		t.Fatalf("queue.refused events = %d, want the plain error and nil store to write nothing", len(got))
	}
}

// refusingQueue wraps the recording stub so QueueCreate fails the way the
// daemon does when it refuses a human-routed create: a RefusalError.
type refusingQueue struct {
	queueStub
	err error
}

func (q *refusingQueue) QueueCreate(ctx context.Context, req rig.CreateRequest) (*rig.QueueItem, error) {
	_, _ = q.queueStub.QueueCreate(ctx, req)
	return nil, q.err
}

// TestNoteRefusalLeavesTheRefusalErrorIntactThroughARaise: the raise's answer
// is still the refusal — `errors.As` finds it, so the caller's `act-skip:`
// line keeps carrying the code — and the ledger now also says a raise was
// refused. A ledger failure never masks it.
func TestNoteRefusalLeavesTheRefusalErrorIntactThroughARaise(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	want := &rig.RefusalError{Code: "human_route_fields_required", Missing: []string{"evidence_ref"}, Command: "queue create"}
	queue := &refusingQueue{err: want}
	actions := &Actions{Store: st, Rig: queue, ProjectKey: "app", WorktreeRoot: t.TempDir()}
	project := store.ProjectRow{ID: 1, Key: "app"}

	_, err := actions.RaiseOperatorQuestion(ctx, project, 5, noVerdictQuestion,
		"decision.no_verdict", "AAAA111", actions.WorktreeRoot)
	if err == nil {
		t.Fatal("raise answered nil, want the refusal")
	}
	var refused *rig.RefusalError
	if !errors.As(err, &refused) || refused.Code != want.Code {
		t.Fatalf("returned error = %v, want errors.As to find the refusal with its code", err)
	}
	exists, err := st.HasEvent(ctx, "queue.refused", "#5", "code=human_route_fields_required;")
	if err != nil || !exists {
		t.Fatalf("HasEvent = %v, %v; want the refusal audited", exists, err)
	}
}
