package decide

import (
	"strings"
	"testing"
	"time"
)

// The two heads the dead-end cases compare: full shas, because that is what
// origin reports and what the store keeps.
const (
	deadHead  = "aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111"
	movedHead = "bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222"
)

// deadEndSnapshot is the production shape (#526): item 2 has a branch with no
// change request and a newest dispatch that already ended there without one.
// endHead is the head the attempt stopped at, branchHead what origin reports
// now; they are equal unless a case moves the branch. Item 3 has no branch, so
// every case also shows the rest of the walk running.
func deadEndSnapshot(branchHead string) Snapshot {
	ended := time.Date(2026, 11, 18, 4, 0, 0, 0, time.UTC)
	s := base(task(2), task(3))
	s.Branches = []Branch{{Name: "feature/2-live-check", Item: 2, Head: branchHead}}
	s.Dispatches = []DispatchState{{
		Item: 2, Seat: "build@rig", Branch: "feature/2-live-check", QItemID: "qitem-2",
		Attempt: 1, CreatedAt: ended.Add(-time.Hour), EndedAt: ended,
		EndReason: "superseded", EndHead: deadHead,
	}}
	s.Rows = []Row{{
		QItemID: "qitem-2", State: "done", Destination: "build@rig",
		Resolution: "blocked: needs a deployed instance",
	}}
	return s
}

func reasonsOf(result Result) map[int]Reason {
	reasons := make(map[int]Reason, len(result.Skips))
	for _, skip := range result.Skips {
		reasons[skip.Number] = skip.Reason
	}
	return reasons
}

// TestDeadEndIsNotReDispatched is the unit's first acceptance: an ended
// dispatch, a branch with no change request and an unchanged head produce no
// dispatch and a dead_end skip naming the branch and head.
func TestDeadEndIsNotReDispatched(t *testing.T) {
	result := Decide(deadEndSnapshot(deadHead))

	if got := dispatchNumbers(result); len(got) != 1 || got[0] != 3 {
		t.Fatalf("dispatches = %v, want only #3", got)
	}
	if got := reasonsOf(result)[2]; got != ReasonDeadEnd {
		t.Fatalf("#2 reason = %q, want %q", got, ReasonDeadEnd)
	}
	var detail string
	for _, skip := range result.Skips {
		if skip.Number == 2 {
			detail = skip.Detail
		}
	}
	if want := "feature/2-live-check@" + deadHead; detail != want {
		t.Fatalf("#2 detail = %q, want %q", detail, want)
	}
	if len(result.DeadEnds) != 1 || result.DeadEnds[0].Number != 2 ||
		result.DeadEnds[0].Branch != "feature/2-live-check" || result.DeadEnds[0].Head != deadHead {
		t.Fatalf("dead ends = %+v, want the one fact the question needs", result.DeadEnds)
	}
}

// TestDeadEndRaisesOneOperatorQuestion: the plan asks the operator once per
// (item, head), with the last row's own resolution in the question. Act's
// raise dedupes the repeat across ticks by (kind, item, head) — the act test
// covers that half; here the text and the routing are pinned.
func TestDeadEndRaisesOneOperatorQuestion(t *testing.T) {
	now := time.Date(2026, 11, 18, 4, 5, 0, 0, time.UTC)
	plan := PlanTick(deadEndSnapshot(deadHead), now)

	var questions []Intent
	for _, intent := range plan.Intents {
		if intent.Kind == IntentRaiseDecision && intent.Audience == AudienceOperator {
			questions = append(questions, intent)
		}
	}
	if len(questions) != 1 {
		t.Fatalf("operator questions = %+v, want exactly one", questions)
	}
	question := questions[0]
	if question.Item != 2 || question.HeadSHA != deadHead {
		t.Fatalf("question = %+v, want item 2 at the dead head", question)
	}
	for _, want := range []string{
		"test #2", "the last dispatch ended without a change request on feature/2-live-check@" + deadHead,
		"release it, put it on hold, or hand it to a person?",
		"blocked: needs a deployed instance",
	} {
		if !strings.Contains(question.Text, want) {
			t.Fatalf("question = %q, want it to carry %q", question.Text, want)
		}
	}
	if question.Event.Kind != "decision.dead_end" || question.Event.Subject != "#2" {
		t.Fatalf("event = %+v, want decision.dead_end on #2", question.Event)
	}
}

// TestDeadEndHeadMovedIsAdopted: a head that moved since the dispatch ended is
// new work, adopted exactly as before the rule existed.
func TestDeadEndHeadMovedIsAdopted(t *testing.T) {
	result := Decide(deadEndSnapshot(movedHead))

	got := dispatchNumbers(result)
	if len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Fatalf("dispatches = %v, want #2 adopted on the moved head beside #3", got)
	}
	if reason, skipped := reasonsOf(result)[2]; skipped {
		t.Fatalf("#2 reason = %q, want no skip", reason)
	}
	if len(result.DeadEnds) != 0 {
		t.Fatalf("dead ends = %+v, want none on a moved head", result.DeadEnds)
	}
}

// TestBranchNeverDispatchedIsAdopted pins the fourth acceptance: the rule adds
// nothing to an item that was never dispatched — a branch with no change
// request stays unfinished work.
func TestBranchNeverDispatchedIsAdopted(t *testing.T) {
	s := deadEndSnapshot(deadHead)
	s.Dispatches = nil
	s.Rows = nil

	if got := dispatchNumbers(Decide(s)); len(got) != 2 || got[0] != 2 {
		t.Fatalf("dispatches = %v, want #2 adopted", got)
	}
}

// TestEndedDispatchWithoutAHeadIsAdopted: attempts that ended before the end
// head existed carry none, and the plan keeps today's behaviour for them — a
// fact it cannot establish is not a dead end.
func TestEndedDispatchWithoutAHeadIsAdopted(t *testing.T) {
	s := deadEndSnapshot(deadHead)
	s.Dispatches[0].EndHead = ""

	if got := dispatchNumbers(Decide(s)); len(got) != 2 || got[0] != 2 {
		t.Fatalf("dispatches = %v, want #2 adopted when no end head was recorded", got)
	}
}

// TestReleasedDeadEndIsAdopted: the operator's "release" answer after the
// attempt ended makes the item eligible again at the same head.
func TestReleasedDeadEndIsAdopted(t *testing.T) {
	s := deadEndSnapshot(deadHead)
	ended := s.Dispatches[0].EndedAt
	s.ReleasedHeads = []ReleasedHead{{Item: 2, Head: deadHead, At: ended.Add(time.Minute)}}

	if got := dispatchNumbers(Decide(s)); len(got) != 2 || got[0] != 2 {
		t.Fatalf("dispatches = %v, want #2 adopted on the operator's release", got)
	}
}

// TestReleaseBeforeTheEndIsSpent: a release answered before the attempt ended
// is the release being spent; the same head parking again is a new dead end,
// not a standing permission to re-dispatch forever.
func TestReleaseBeforeTheEndIsSpent(t *testing.T) {
	s := deadEndSnapshot(deadHead)
	ended := s.Dispatches[0].EndedAt
	s.ReleasedHeads = []ReleasedHead{{Item: 2, Head: deadHead, At: ended.Add(-time.Minute)}}

	if got := reasonsOf(Decide(s))[2]; got != ReasonDeadEnd {
		t.Fatalf("#2 reason = %q, want %q: the release predates the end", got, ReasonDeadEnd)
	}
}
