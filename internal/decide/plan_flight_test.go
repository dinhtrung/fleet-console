package decide

import (
	"strings"
	"testing"
)

// The regression fixtures for #318, from the real incident: a live reviewer
// row on tsim-fleet carried the head abbreviated (`head cca2508c`) while the
// record held the full 40-character sha, so the old contains-match missed it
// and the operator question was raised over and over while the review was in
// flight.
const incidentHead = "cca2508c3550b539a3b5b44a6f5f91e31bf5e90d"

func incidentRow() Row {
	return Row{
		QItemID:     "qitem-450-review",
		State:       "in-progress",
		Pickup:      "claimed",
		Destination: "dev-review@tsim-fleet-claude",
		Body:        "Review #450 … MR !688, DRAFT … head cca2508c (commits ca919960 and cca2508c …)",
	}
}

// TestPlanNoVerdictWhileReviewInFlight is the incident's own fixture: a live
// reviewer row working the item, whatever sha its text carries, silences the
// operator's no-verdict question — workInFlight, not a sha match. A tick
// with no such row still asks, exactly once.
func TestPlanNoVerdictWhileReviewInFlight(t *testing.T) {
	s := base(Item{Number: 450, Kind: KindTask, State: StateOpen, Title: "Mirror classify"})
	s.CRs = []CRState{{Number: 688, Item: 450, State: "open", HeadSHA: incidentHead}}
	s.Rows = []Row{incidentRow()}
	plan := PlanTick(s, planNow)
	if len(findIntents(plan, IntentRaiseDecision)) != 0 {
		t.Fatalf("intents = %+v, want no decision.no_verdict while the review is in flight", plan.Intents)
	}
	// The silence is the row's, not the arm's: without the row the question
	// is asked.
	s.Rows = nil
	plan = PlanTick(s, planNow)
	asked := findIntents(plan, IntentRaiseDecision)
	if len(asked) != 1 || asked[0].Event.Kind != "decision.no_verdict" || asked[0].Audience != AudienceOperator {
		t.Fatalf("intents = %+v, want one operator question once the row is gone", plan.Intents)
	}
}

// TestPlanQuestionStandingPerHead is the per-head half: a live human row
// naming this head — abbreviated, matched through forge.SameSHA — is the
// same question standing, so nothing is planned a second time; a question
// for another head does not stand, and the question is asked again.
func TestPlanQuestionStandingPerHead(t *testing.T) {
	const head = "bbbb22223333444455556666777788889999cccc"
	const moved = "cccc3333444455556666777788889999aaaabbbb"
	s := base(Item{Number: 5, Kind: KindTask, State: StateOpen, Title: "Connection pool"})
	s.CRs = []CRState{{Number: 20, Item: 5, State: "open", HeadSHA: head}}
	s.Rows = []Row{operatorRow("qitem-op-1",
		"test #5 Connection pool: change request #20 has no verdict for head bbbb222. Approve or reject it on the item drawer or reply fleet: accept / fleet: reject on the item.")}
	plan := PlanTick(s, planNow)
	if len(findIntents(plan, IntentRaiseDecision)) != 0 {
		t.Fatalf("intents = %+v, want none while the same head's question stands", plan.Intents)
	}
	// A different head is a different question: asked again, naming the sha.
	s.CRs[0].HeadSHA = moved
	plan = PlanTick(s, planNow)
	asked := findIntents(plan, IntentRaiseDecision)
	if len(asked) != 1 || !strings.Contains(asked[0].Text, "cccc333") {
		t.Fatalf("intents = %+v, want one question naming the moved head", plan.Intents)
	}
}

// TestPlanStaleAsksReviewer pins the stale arm's boundary: a re-review of a
// moved head is a question, not work, so a live reviewer row on the item
// must not silence it — only a standing human question for this head does.
func TestPlanStaleAsksReviewer(t *testing.T) {
	const stale = "aaaa111122223333444455556666777788889999"
	const head = "bbbb22223333444455556666777788889999cccc"
	s := base(Item{Number: 5, Kind: KindTask, State: StateOpen, Title: "Connection pool"})
	s.CRs = []CRState{{Number: 20, Item: 5, State: "open", HeadSHA: head}}
	s.Verdicts = []VerdictState{{Item: 5, SHA: stale, Decision: "accept"}}
	// Work in flight on this very item, its row naming both the item and the
	// head the old askedForHead would have read as "the question stands".
	s.Rows = []Row{{
		QItemID: "qitem-rr-0", State: "in-progress", Pickup: "claimed",
		Destination: "dev-review@tsim-fleet-claude",
		Body:        "Review #5 … MR !20, DRAFT … head bbbb222 (commits aaaa111 and bbbb222 …)",
	}}
	plan := PlanTick(s, planNow)
	asked := findIntents(plan, IntentRaiseDecision)
	if len(asked) != 1 || asked[0].Event.Kind != "decision.stale" || asked[0].Audience != AudienceReviewer {
		t.Fatalf("intents = %+v, want one re-review question for the reviewer despite the live row", plan.Intents)
	}
}
