package decide

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

var planNow = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

func hasEvent(plan Plan, kind, subject string) bool {
	for _, event := range plan.Events {
		if event.Kind == kind && event.Subject == subject {
			return true
		}
	}
	return false
}

func findIntents(plan Plan, kind IntentKind) []Intent {
	var out []Intent
	for _, intent := range plan.Intents {
		if intent.Kind == kind {
			out = append(out, intent)
		}
	}
	return out
}

func closedTask(number int, parent *int) Item {
	return Item{Number: number, Kind: KindTask, State: StateClosed, Parent: parent}
}

func TestHousekeepingPrune(t *testing.T) {
	s := base(closedTask(1, nil))
	s.Dispatches = []DispatchState{{Item: 1, Attempt: 1, EndedAt: planNow.Add(-time.Hour)}}
	plan := PlanTick(s, planNow)
	if !hasEvent(plan, "dispatch.prune", "#1") {
		t.Fatalf("events = %+v, want dispatch.prune for #1", plan.Events)
	}
}

func TestHousekeepingResetStalled(t *testing.T) {
	s := base(task(1))
	s.Dispatches = []DispatchState{{Item: 1, Attempt: 1, CreatedAt: planNow.Add(-2 * time.Hour), EndedAt: planNow.Add(-45 * time.Minute)}}
	plan := PlanTick(s, planNow)
	if !hasEvent(plan, "dispatch.reset", "#1") {
		t.Fatalf("events = %+v, want dispatch.reset for #1", plan.Events)
	}
}

func TestHousekeepingAbandonsAtMaxAttempts(t *testing.T) {
	s := base(task(1))
	s.Dispatches = []DispatchState{{Item: 1, Attempt: MaxAttempts, EndedAt: planNow.Add(-time.Hour)}}
	plan := PlanTick(s, planNow)
	if !hasEvent(plan, "dispatch.abandon", "#1") {
		t.Fatalf("events = %+v, want dispatch.abandon for #1", plan.Events)
	}
}

func TestHousekeepingReconcileMarker(t *testing.T) {
	s := base(Item{Number: 1, Kind: KindTask, State: StateClosed, Labels: []string{MarkerLabel}})
	plan := PlanTick(s, planNow)
	intents := findIntents(plan, IntentClearMarker)
	if len(intents) != 1 || intents[0].Item != 1 {
		t.Fatalf("intents = %+v, want one clear_marker for #1", plan.Intents)
	}
	if !hasEvent(plan, "marker.clear", "#1") {
		t.Fatalf("events = %+v, want marker.clear", plan.Events)
	}
}

func TestHousekeepingRenudge(t *testing.T) {
	s := base(task(1))
	s.Rows = []Row{{
		QItemID: "qitem-1", State: "pending", Pickup: "unclaimed", Destination: "seat-1",
		CreatedAt: planNow.Add(-20 * time.Minute), LastNudgeAt: planNow.Add(-40 * time.Minute),
	}}
	plan := PlanTick(s, planNow)
	intents := findIntents(plan, IntentNudge)
	if len(intents) != 1 || intents[0].QItemID != "qitem-1" {
		t.Fatalf("intents = %+v, want one nudge for qitem-1", plan.Intents)
	}
	if !hasEvent(plan, "row.renudge", "qitem-1") {
		t.Fatalf("events = %+v, want row.renudge", plan.Events)
	}
}

func TestHousekeepingRenudgeHonoursCooldown(t *testing.T) {
	s := base(task(1))
	s.Rows = []Row{{
		QItemID: "qitem-1", State: "pending", Pickup: "unclaimed", Destination: "seat-1",
		CreatedAt: planNow.Add(-20 * time.Minute), LastNudgeAt: planNow.Add(-10 * time.Minute),
	}}
	plan := PlanTick(s, planNow)
	if len(findIntents(plan, IntentNudge)) != 0 {
		t.Fatalf("intents = %+v, want no nudge inside the cooldown", plan.Intents)
	}
}

func TestHousekeepingCloseEpic(t *testing.T) {
	parent := 10
	s := base(epic(10), closedTask(11, &parent), closedTask(12, &parent))
	plan := PlanTick(s, planNow)
	intents := findIntents(plan, IntentCloseEpic)
	if len(intents) != 1 || intents[0].Item != 10 {
		t.Fatalf("intents = %+v, want close_epic for #10", plan.Intents)
	}
	if !hasEvent(plan, "epic.close", "#10") {
		t.Fatalf("events = %+v, want epic.close", plan.Events)
	}
}

// TestSilentDispatchIntent is the #261 table: a live dispatch that produced no
// change request becomes one dispatch.silent intent after SilentDispatchAfter,
// and the four look-alike states stay silent — an open change request (the
// merge gate's case), an ended dispatch (the reset rule's), a young dispatch,
// and a closed item.
func TestSilentDispatchIntent(t *testing.T) {
	live := func(age time.Duration) DispatchState {
		return DispatchState{Item: 1, Seat: "dev-build-1@r", Branch: "feature/1-x",
			Attempt: 1, CreatedAt: planNow.Add(-age)}
	}
	ended := func(age, ago time.Duration) DispatchState {
		d := live(age)
		d.EndedAt = planNow.Add(-ago)
		return d
	}
	cases := []struct {
		name      string
		dispatch  DispatchState
		crs       []CRState
		item      Item
		wantCount int
	}{
		{"open 31m, no change request", live(31 * time.Minute), nil, task(1), 1},
		{"open 31m, open change request", live(31 * time.Minute),
			[]CRState{{Number: 9, Item: 1, State: "open"}}, task(1), 0},
		{"ended 31m ago", ended(2*time.Hour, 31*time.Minute), nil, task(1), 0},
		{"open 10m", live(10 * time.Minute), nil, task(1), 0},
		{"item closed", live(31 * time.Minute), nil, closedTask(1, nil), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := base(tc.item)
			s.Dispatches = []DispatchState{tc.dispatch}
			s.CRs = tc.crs
			plan := PlanTick(s, planNow)
			intents := findIntents(plan, IntentSilentDispatch)
			if len(intents) != tc.wantCount {
				t.Fatalf("intents = %+v, want %d silent dispatch intents", plan.Intents, tc.wantCount)
			}
			if tc.wantCount == 0 {
				return
			}
			if !hasEvent(plan, "dispatch.silent", "#1") {
				t.Fatalf("events = %+v, want dispatch.silent for #1", plan.Events)
			}
			want := "seat=dev-build-1@r branch=feature/1-x attempt=1 age=31m0s"
			if intents[0].Event.Detail != want {
				t.Fatalf("detail = %q, want %q", intents[0].Event.Detail, want)
			}
			if intents[0].Seat != "dev-build-1@r" {
				t.Fatalf("intent seat = %q, want the dispatch's seat", intents[0].Seat)
			}
		})
	}
}

func TestIncidentParked(t *testing.T) {
	s := base(Item{Number: 5, Kind: KindTask, State: StateOpen, Title: "Connection pool"})
	s.CRs = []CRState{{Number: 20, Item: 5, State: "open", HeadSHA: "abc123"}}
	plan := PlanTick(s, planNow)
	intents := findIntents(plan, IntentRaiseDecision)
	want := "test #5 Connection pool: change request #20 has no verdict for head abc123. Approve or reject it on the item drawer (POST /api/items/5/verdict) or reply fleet: accept / fleet: reject on the item."
	if len(intents) != 1 || intents[0].Text != want {
		t.Fatalf("intents = %+v, want question %q", plan.Intents, want)
	}
}

// TestNoVerdictAsksTheOperator is #265's audience split: a missing verdict is
// the operator's question, not the reviewer seat's — the intent says who it
// is for, carries the facts the brief prints verbatim, and its event names
// the kind and the head the dedupe keys on.
func TestNoVerdictAsksTheOperator(t *testing.T) {
	s := base(Item{Number: 5, Kind: KindTask, State: StateOpen, Title: "Connection pool"})
	s.CRs = []CRState{{Number: 20, Item: 5, State: "open", HeadSHA: "aaaa111"}}
	plan := PlanTick(s, planNow)
	intents := findIntents(plan, IntentRaiseDecision)
	if len(intents) != 1 {
		t.Fatalf("intents = %+v, want one decision", plan.Intents)
	}
	intent := intents[0]
	if intent.Audience != AudienceOperator {
		t.Fatalf("audience = %v, want operator", intent.Audience)
	}
	if intent.Event.Kind != "decision.no_verdict" {
		t.Fatalf("event kind = %q, want decision.no_verdict", intent.Event.Kind)
	}
	if intent.ChangeRequest != 20 || intent.HeadSHA != "aaaa111" {
		t.Fatalf("intent = %+v, want the change request and head carried for the brief", intent)
	}
	if !strings.Contains(intent.Event.Detail, "head=aaaa111") {
		t.Fatalf("detail = %q, want head=aaaa111", intent.Event.Detail)
	}
	for _, want := range []string{"#5", "#20", "aaaa111", "POST /api/items/5/verdict", "fleet: accept", "fleet: reject"} {
		if !strings.Contains(intent.Text, want) {
			t.Fatalf("text = %q, want it to name %q", intent.Text, want)
		}
	}
}

// TestPlanRaisesRepairInsteadOfNoVerdictOnConflict: a head the forge has
// scored as conflicting is a repair, not a review question (epic #341,
// design D3). No decision.no_verdict is raised; one decision.merge_repair
// intent names the change request, the head and the forge's word, for the
// human who reworks or defers.
func TestPlanRaisesRepairInsteadOfNoVerdictOnConflict(t *testing.T) {
	s := base(Item{Number: 5, Kind: KindTask, State: StateOpen, Title: "Connection pool"})
	s.CRs = []CRState{{Number: 20, Item: 5, State: "open", HeadSHA: "abc123", MergeState: "dirty"}}
	plan := PlanTick(s, planNow)
	if hasEvent(plan, "decision.no_verdict", "#5") {
		t.Fatalf("events = %+v, want no approval question for a conflicting head", plan.Events)
	}
	intents := findIntents(plan, IntentRaiseDecision)
	if len(intents) != 1 {
		t.Fatalf("intents = %+v, want exactly the repair ask", plan.Intents)
	}
	intent := intents[0]
	if intent.Event.Kind != "decision.merge_repair" {
		t.Fatalf("event kind = %q, want decision.merge_repair", intent.Event.Kind)
	}
	if intent.Audience != AudienceOperator {
		t.Fatalf("audience = %v, want operator — the repair row is the human's", intent.Audience)
	}
	if intent.ChangeRequest != 20 || intent.HeadSHA != "abc123" {
		t.Fatalf("intent = %+v, want the change request and head carried for the brief", intent)
	}
	for _, want := range []string{"#5", "#20", "abc123", "rework or defer"} {
		if !strings.Contains(intent.Text, want) {
			t.Fatalf("text = %q, want it to name %q", intent.Text, want)
		}
	}
	if !strings.Contains(intent.Event.Detail, "dirty") {
		t.Fatalf("detail = %q, want the forge's word", intent.Event.Detail)
	}
	// GitLab's word routes the same way: the plan never spends a review on it.
	s.CRs[0].MergeState = "cannot_be_merged"
	plan = PlanTick(s, planNow)
	intents = findIntents(plan, IntentRaiseDecision)
	if len(intents) != 1 || intents[0].Event.Kind != "decision.merge_repair" {
		t.Fatalf("intents = %+v, want the GitLab conflict word to raise the repair too", plan.Intents)
	}
}

// TestPlanRaisesRepairInsteadOfStaleOnConflict: a conflicting head never asks
// for a re-review — neither on an older head's verdict, nor when the newest
// verdict already names the head, where today nothing is raised and the merge
// gate refuses later (epic #341, design D3).
func TestPlanRaisesRepairInsteadOfStaleOnConflict(t *testing.T) {
	s := base(Item{Number: 5, Kind: KindTask, State: StateOpen, Title: "Connection pool"})
	s.CRs = []CRState{{Number: 20, Item: 5, State: "open", HeadSHA: "bbbb222", MergeState: "dirty"}}
	// A verdict on an older head would have asked the reviewer to re-review.
	s.Verdicts = []VerdictState{{Item: 5, SHA: "aaaa111", Decision: "accept"}}
	plan := PlanTick(s, planNow)
	if hasEvent(plan, "decision.stale", "#5") {
		t.Fatalf("events = %+v, want no re-review for a conflicting head", plan.Events)
	}
	intents := findIntents(plan, IntentRaiseDecision)
	if len(intents) != 1 || intents[0].Event.Kind != "decision.merge_repair" {
		t.Fatalf("intents = %+v, want exactly the repair ask", plan.Intents)
	}
	// The newest verdict names the head itself: still the repair — the merge
	// gate refuses the head later regardless, and the review would be spent.
	s.Verdicts = []VerdictState{{Item: 5, SHA: "bbbb222", Decision: "accept"}}
	plan = PlanTick(s, planNow)
	intents = findIntents(plan, IntentRaiseDecision)
	if len(intents) != 1 || intents[0].Event.Kind != "decision.merge_repair" {
		t.Fatalf("intents = %+v, want the repair even when the verdict names the head", plan.Intents)
	}
}

// TestNoVerdictIsAskedOncePerHead is the per-(item, head) dedupe, as the plan
// can see it: the question a raise left behind is a live row in the snapshot,
// and the same head stays quiet while a moved head asks again.
func TestNoVerdictIsAskedOncePerHead(t *testing.T) {
	s := base(Item{Number: 5, Kind: KindTask, State: StateOpen, Title: "Connection pool"})
	s.CRs = []CRState{{Number: 20, Item: 5, State: "open", HeadSHA: "aaaa111"}}
	plan := PlanTick(s, planNow)
	asked := findIntents(plan, IntentRaiseDecision)
	if len(asked) != 1 || !strings.Contains(asked[0].Text, "aaaa111") {
		t.Fatalf("intents = %+v, want one question naming aaaa111", plan.Intents)
	}
	// The raise happened: the operator's row is live and its body names the
	// head, the way D3's question body does.
	s.Rows = append(s.Rows, Row{QItemID: "qitem-op-1", State: "pending", Pickup: "unclaimed",
		Destination: "human@kernel", Body: asked[0].Text})
	plan = PlanTick(s, planNow)
	if len(findIntents(plan, IntentRaiseDecision)) != 0 {
		t.Fatalf("intents = %+v, want none while the same head's question stands", plan.Intents)
	}
	// The head moves: the standing question is about a different commit, so
	// one new question names the new sha.
	s.CRs[0].HeadSHA = "bbbb222"
	plan = PlanTick(s, planNow)
	moved := findIntents(plan, IntentRaiseDecision)
	if len(moved) != 1 || !strings.Contains(moved[0].Text, "bbbb222") {
		t.Fatalf("intents = %+v, want one question naming bbbb222", plan.Intents)
	}
	if moved[0].HeadSHA != "bbbb222" || moved[0].ChangeRequest != 20 {
		t.Fatalf("intent = %+v, want the new head carried for the brief", moved[0])
	}
}

// TestStaleVerdictAsksTheReviewerOncePerHead: the re-review stays the
// reviewer's job (D4). In the epic's vocabulary, a reviewer row is not
// silence — the arm keeps naming the question and act's ledger turns the
// repeat into a NoOp — only a human question for this head silences it, and
// a moved head earns its own question again.
func TestStaleVerdictAsksTheReviewerOncePerHead(t *testing.T) {
	s := base(Item{Number: 5, Kind: KindTask, State: StateOpen, Title: "Connection pool"})
	s.CRs = []CRState{{Number: 20, Item: 5, State: "open", HeadSHA: "bbbb222"}}
	s.Verdicts = []VerdictState{{Item: 5, SHA: "aaaa111", Decision: "accept"}}
	plan := PlanTick(s, planNow)
	asked := findIntents(plan, IntentRaiseDecision)
	if len(asked) != 1 {
		t.Fatalf("intents = %+v, want one re-review question", plan.Intents)
	}
	if asked[0].Audience != AudienceReviewer {
		t.Fatalf("audience = %v, want reviewer", asked[0].Audience)
	}
	if asked[0].Event.Kind != "decision.stale" {
		t.Fatalf("event kind = %q, want decision.stale", asked[0].Event.Kind)
	}
	// The reviewer's own standing question does not silence the arm: the plan
	// keeps naming the same re-review, and act's ledger turns the repeat into
	// a NoOp. Work in flight is silence for the operator's question, not for
	// this one.
	s.Rows = append(s.Rows, Row{QItemID: "qitem-rr-1", State: "pending", Pickup: "unclaimed",
		Destination: "dev-review@r", Body: asked[0].Text})
	plan = PlanTick(s, planNow)
	still := findIntents(plan, IntentRaiseDecision)
	if len(still) != 1 || still[0].Event.Kind != "decision.stale" || !strings.Contains(still[0].Text, "bbbb222") {
		t.Fatalf("intents = %+v, want the re-review still named while the reviewer row stands", plan.Intents)
	}
	// A human question for this head is the silence: the stall escalation's
	// operator row names the head, and it stands until answered or moved.
	s.Rows = append(s.Rows, operatorRow("qitem-op-1",
		"test #5: review stalled - the re-review of change request #20's head bbbb222 has waited 46m with 2 unanswered nudges."))
	plan = PlanTick(s, planNow)
	if len(findIntents(plan, IntentRaiseDecision)) != 0 {
		t.Fatalf("intents = %+v, want none while the same head's human question stands", plan.Intents)
	}
	s.CRs[0].HeadSHA = "cccc333"
	plan = PlanTick(s, planNow)
	moved := findIntents(plan, IntentRaiseDecision)
	if len(moved) != 1 || !strings.Contains(moved[0].Text, "cccc333") {
		t.Fatalf("intents = %+v, want one re-review naming cccc333", plan.Intents)
	}
	if moved[0].Audience != AudienceReviewer {
		t.Fatalf("audience = %v, want reviewer", moved[0].Audience)
	}
}

// stallIntents picks the decision.stall raises out of a plan.
func stallIntents(plan Plan) []Intent {
	var out []Intent
	for _, intent := range findIntents(plan, IntentRaiseDecision) {
		if intent.Event.Kind == "decision.stall" {
			out = append(out, intent)
		}
	}
	return out
}

// staleRow is the reviewer's re-review question as a live snapshot row: the
// text the raise posts, aged and nudged as the test asks.
func staleRow(head string, age, nudgeAge time.Duration) Row {
	return Row{
		QItemID: "qitem-rr-1", State: "pending", Pickup: "unclaimed",
		Destination: "dev-review@r",
		Body:        fmt.Sprintf("test #5: verdict stale - ACCEPT aaaa111 is on aaaa111, head is %s; review %s", head, head),
		CreatedAt:   planNow.Add(-age),
		LastNudgeAt: planNow.Add(-nudgeAge),
	}
}

// operatorRow is a raised question as a live snapshot row, the way the
// raise leaves it behind for the next tick's plan.
func operatorRow(qitem, body string) Row {
	return Row{QItemID: qitem, State: "pending", Pickup: "unclaimed",
		Destination: "human@kernel", Body: body}
}

// malformedRow is the reviewer row the malformed ask left behind: its text is
// the §0 literal, which carries the note id and the head but no item token —
// so, unlike staleRow, it names no #5 for the plan to find.
func malformedRow(head string, age, nudgeAge time.Duration) Row {
	return Row{
		QItemID: "qitem-mv-1", State: "pending", Pickup: "unclaimed",
		Destination: "dev-review@r",
		Body:        fmt.Sprintf("The merge gate reads one line: ACCEPT %s or REJECT %s. Your note 6847905312's first line was \"## QA Verdict: ACCEPT ✅\", which names no commit. Post a new comment whose first line is:\n\nACCEPT %s", head, head, head),
		CreatedAt:   planNow.Add(-age),
		LastNudgeAt: planNow.Add(-nudgeAge),
	}
}

// TestStaleStallAsksOnce: a re-review that outlives its nudges asks the human
// once per head — quiet at 30 minutes, exactly one at the 45-minute
// threshold, and quiet again while the raised question stands.
func TestStaleStallAsksOnce(t *testing.T) {
	s := base(Item{Number: 5, Kind: KindTask, State: StateOpen, Title: "Connection pool"})
	s.CRs = []CRState{{Number: 20, Item: 5, State: "open", HeadSHA: "bbbb222"}}
	s.Verdicts = []VerdictState{{Item: 5, SHA: "aaaa111", Decision: "accept"}}
	s.Rows = []Row{staleRow("bbbb222", 30*time.Minute, 5*time.Minute)}
	plan := PlanTick(s, planNow)
	if got := stallIntents(plan); len(got) != 0 {
		t.Fatalf("stalls = %+v, want none at 30 minutes — inside the threshold", got)
	}
	s.Rows[0].CreatedAt = planNow.Add(-46 * time.Minute)
	s.Rows[0].LastNudgeAt = planNow.Add(-5 * time.Minute)
	plan = PlanTick(s, planNow)
	stalls := stallIntents(plan)
	if len(stalls) != 1 {
		t.Fatalf("stalls = %d, want exactly one at the threshold", len(stalls))
	}
	stall := stalls[0]
	if stall.Audience != AudienceOperator || stall.HeadSHA != "bbbb222" || stall.ChangeRequest != 20 {
		t.Fatalf("stall = %+v, want the operator asked for #20 at bbbb222", stall)
	}
	if !strings.Contains(stall.Event.Detail, "head=bbbb222; row qitem-rr-1; age=46m; nudges=2") {
		t.Fatalf("detail = %q, want the facts grammar", stall.Event.Detail)
	}
	for _, want := range []string{"test #5", "#20", "bbbb222", "46m", "2 unanswered nudges"} {
		if !strings.Contains(stall.Text, want) {
			t.Fatalf("text = %q, want it to name %q", stall.Text, want)
		}
	}
	// The operator row the raise left behind is the record: ten more ticks at
	// the same head stay quiet (act's ledger turns a repeat into a NoOp).
	s.Rows = append(s.Rows, operatorRow("qitem-op-1", stall.Text))
	for tick := 0; tick < 10; tick++ {
		plan = PlanTick(s, planNow)
		if got := stallIntents(plan); len(got) != 0 {
			t.Fatalf("tick %d raised %+v, want none while the question stands", tick, got)
		}
	}
}

// TestStaleStallNewHeadAsksAgain: one question per head. The old head's
// standing question is not repeated, and a moved head earns its own after its
// re-review row has outlived the nudges too.
func TestStaleStallNewHeadAsksAgain(t *testing.T) {
	s := base(Item{Number: 5, Kind: KindTask, State: StateOpen, Title: "Connection pool"})
	s.CRs = []CRState{{Number: 20, Item: 5, State: "open", HeadSHA: "bbbb222"}}
	s.Verdicts = []VerdictState{{Item: 5, SHA: "aaaa111", Decision: "accept"}}
	s.Rows = []Row{staleRow("bbbb222", 46*time.Minute, 5*time.Minute)}
	plan := PlanTick(s, planNow)
	stalls := stallIntents(plan)
	if len(stalls) != 1 || stalls[0].HeadSHA != "bbbb222" {
		t.Fatalf("stalls = %+v, want one for bbbb222", stalls)
	}
	s.Rows = append(s.Rows, operatorRow("qitem-op-1", stalls[0].Text))
	// The head moves: the old question stands, nothing new is asked yet.
	s.CRs[0].HeadSHA = "cccc333"
	plan = PlanTick(s, planNow)
	if got := stallIntents(plan); len(got) != 0 {
		t.Fatalf("stalls = %+v, want none for the old head once asked", got)
	}
	// The new head's re-review row, aged and nudged, is a new question.
	s.Rows = append(s.Rows, staleRow("cccc333", 46*time.Minute, 5*time.Minute))
	plan = PlanTick(s, planNow)
	stalls = stallIntents(plan)
	if len(stalls) != 1 || stalls[0].HeadSHA != "cccc333" {
		t.Fatalf("stalls = %+v, want one for the new head cccc333", stalls)
	}
}

// TestStaleStallAnsweredBeforeThresholdIsQuiet: an answer or a young row asks
// nothing — the reviewer answered at 10 minutes, or the re-review row is
// simply not old enough yet.
func TestStaleStallAnsweredBeforeThresholdIsQuiet(t *testing.T) {
	s := base(Item{Number: 5, Kind: KindTask, State: StateOpen, Title: "Connection pool"})
	s.CRs = []CRState{{Number: 20, Item: 5, State: "open", HeadSHA: "bbbb222"}}
	// Answered at 10 minutes: the verdict now names the head, so nothing is
	// stale and no question is raised — the aged reviewer row notwithstanding.
	s.Verdicts = []VerdictState{{Item: 5, SHA: "bbbb222", Decision: "accept"}}
	s.Rows = []Row{staleRow("bbbb222", 46*time.Minute, 5*time.Minute)}
	plan := PlanTick(s, planNow)
	if got := findIntents(plan, IntentRaiseDecision); len(got) != 0 {
		t.Fatalf("raises = %+v, want none once the review is answered", got)
	}
	// Not yet answered, but young: the reviewer row keeps its own nudges.
	s.Verdicts = []VerdictState{{Item: 5, SHA: "aaaa111", Decision: "accept"}}
	s.Rows = []Row{staleRow("bbbb222", 10*time.Minute, 5*time.Minute)}
	plan = PlanTick(s, planNow)
	if got := stallIntents(plan); len(got) != 0 {
		t.Fatalf("stalls = %+v, want none inside the threshold", got)
	}
}

// TestStaleStallWithoutNudgeIsQuiet: a row no nudge ever reached has not
// outlived its nudges — asking the human would skip the reviewer entirely.
func TestStaleStallWithoutNudgeIsQuiet(t *testing.T) {
	s := base(Item{Number: 5, Kind: KindTask, State: StateOpen, Title: "Connection pool"})
	s.CRs = []CRState{{Number: 20, Item: 5, State: "open", HeadSHA: "bbbb222"}}
	s.Verdicts = []VerdictState{{Item: 5, SHA: "aaaa111", Decision: "accept"}}
	row := staleRow("bbbb222", 2*time.Hour, 0)
	row.LastNudgeAt = time.Time{}
	s.Rows = []Row{row}
	plan := PlanTick(s, planNow)
	if got := stallIntents(plan); len(got) != 0 {
		t.Fatalf("stalls = %+v, want none while no nudge was sent", got)
	}
}

// TestMalformedStallAfterTwoNudgeCycles: the malformed ask escalates to the
// operator on the same deadline the stale re-review uses — quiet at 44
// minutes, exactly one decision.stall at 46, and once a parseable verdict
// stands for the head, neither a nudge nor an escalation.
func TestMalformedStallAfterTwoNudgeCycles(t *testing.T) {
	s := base(Item{Number: 5, Kind: KindTask, State: StateOpen, Title: "Connection pool"})
	s.CRs = []CRState{{Number: 20, Item: 5, State: "open", HeadSHA: "cccc333"}}
	s.MalformedVerdicts = map[int]MalformedVerdict{
		5: {NoteID: "6847905312", Head: "cccc333", Line: "## QA Verdict: ACCEPT ✅"},
	}

	t.Run("44_minutes_raises_nothing_to_the_operator", func(t *testing.T) {
		s.Rows = []Row{malformedRow("cccc333", 44*time.Minute, 1*time.Minute)}
		plan := PlanTick(s, planNow)
		if got := stallIntents(plan); len(got) != 0 {
			t.Fatalf("stalls = %+v, want none at 44 minutes — inside the threshold", got)
		}
	})

	t.Run("46_minutes_asks_the_operator_once", func(t *testing.T) {
		s.Rows = []Row{malformedRow("cccc333", 46*time.Minute, 1*time.Minute)}
		plan := PlanTick(s, planNow)
		stalls := stallIntents(plan)
		if len(stalls) != 1 {
			t.Fatalf("stalls = %d, want exactly one at the threshold", len(stalls))
		}
		stall := stalls[0]
		if stall.Event.Kind != "decision.stall" || stall.Audience != AudienceOperator ||
			stall.HeadSHA != "cccc333" || stall.ChangeRequest != 20 {
			t.Fatalf("stall = %+v, want the operator asked for #20 at cccc333", stall)
		}
		if !strings.Contains(stall.Event.Detail, "head=cccc333; note=6847905312; row qitem-mv-1; age=46m; nudges=2") {
			t.Fatalf("detail = %q, want the facts grammar with the note id", stall.Event.Detail)
		}
		for _, want := range []string{
			"test #5", "malformed verdict stalled", "6847905312", "## QA Verdict: ACCEPT ✅",
			"cccc333", "46m", "2 unanswered nudges",
			"The merge gate reads one line: ACCEPT cccc333 or REJECT cccc333",
			"Post a new comment whose first line is:\n\nACCEPT cccc333",
			"POST /api/items/5/verdict", "instruct the reviewer", "A hand merge is not one of the moves",
		} {
			if !strings.Contains(stall.Text, want) {
				t.Fatalf("text = %q, want it to name %q", stall.Text, want)
			}
		}
	})

	t.Run("a_parseable_verdict_in_between_stops_both", func(t *testing.T) {
		s.Rows = []Row{malformedRow("cccc333", 46*time.Minute, 1*time.Minute)}
		s.Verdicts = []VerdictState{{Item: 5, SHA: "cccc333", Decision: "accept"}}
		plan := PlanTick(s, planNow)
		if got := stallIntents(plan); len(got) != 0 {
			t.Fatalf("stalls = %+v, want none once the verdict stands for the head", got)
		}
		if hasEvent(plan, "decision.malformed_verdict", "#5") {
			t.Fatalf("events = %+v, want no re-ask beside the captured verdict", plan.Events)
		}
		if got := findIntents(plan, IntentNudge); len(got) != 0 {
			t.Fatalf("nudges = %+v, want none once the verdict stands for the head", got)
		}
	})
}

// TestMalformedStallAsksOnce: the operator row the escalation leaves behind is
// the record — later ticks stay quiet, the reviewer's literal is not re-asked
// beside it, and the operator's own aged row is never mistaken for the
// reviewer row a new escalation would key on.
func TestMalformedStallAsksOnce(t *testing.T) {
	s := base(Item{Number: 5, Kind: KindTask, State: StateOpen, Title: "Connection pool"})
	s.CRs = []CRState{{Number: 20, Item: 5, State: "open", HeadSHA: "cccc333"}}
	s.MalformedVerdicts = map[int]MalformedVerdict{
		5: {NoteID: "6847905312", Head: "cccc333", Line: "## QA Verdict: ACCEPT ✅"},
	}
	s.Rows = []Row{malformedRow("cccc333", 46*time.Minute, 1*time.Minute)}
	plan := PlanTick(s, planNow)
	stalls := stallIntents(plan)
	if len(stalls) != 1 {
		t.Fatalf("stalls = %d, want exactly one at the threshold", len(stalls))
	}
	// Ten more ticks at the same head with the question standing: no second
	// stall, and no re-ask of the reviewer beside it (act's ledger turns a
	// repeat of either into a NoOp).
	s.Rows = append(s.Rows, operatorRow("qitem-op-1", stalls[0].Text))
	for tick := 0; tick < 10; tick++ {
		plan = PlanTick(s, planNow)
		if got := stallIntents(plan); len(got) != 0 {
			t.Fatalf("tick %d raised %+v, want none while the question stands", tick, got)
		}
		if hasEvent(plan, "decision.malformed_verdict", "#5") {
			t.Fatalf("events = %+v, want no re-ask while the operator question stands", plan.Events)
		}
	}
	// The reviewer's row has since been ended, but the operator's aged row
	// remains: no fresh escalation keys on it — act's ledger is the backstop.
	s.Rows = s.Rows[1:]
	s.Rows[0].CreatedAt = planNow.Add(-2 * time.Hour)
	s.Rows[0].LastNudgeAt = planNow.Add(-5 * time.Minute)
	plan = PlanTick(s, planNow)
	if got := stallIntents(plan); len(got) != 0 {
		t.Fatalf("stalls = %+v, want none keyed on the operator's own row", got)
	}
}

func TestIncidentParkedSuppressedByLiveRow(t *testing.T) {
	s := base(Item{Number: 5, Kind: KindTask, State: StateOpen, Title: "Connection pool"})
	s.CRs = []CRState{{Number: 20, Item: 5, State: "open", HeadSHA: "abc123"}}
	s.Rows = []Row{{QItemID: "qitem-5", State: "in-progress", Pickup: "working", Destination: "seat-1", Body: "#5"}}
	plan := PlanTick(s, planNow)
	if len(findIntents(plan, IntentRaiseDecision)) != 0 {
		t.Fatalf("intents = %+v, want no decision while a row is live", plan.Intents)
	}
}

func TestIncidentMarkerLeak(t *testing.T) {
	s := base(Item{Number: 5, Kind: KindTask, State: StateOpen, Title: "Connection pool"})
	s.CRs = []CRState{{Number: 20, Item: 5, State: "merged", Merged: true}}
	plan := PlanTick(s, planNow)
	intents := findIntents(plan, IntentRaiseDecision)
	want := "test #5: change request #20 merged but the item is still open. Close it now?"
	if len(intents) != 1 || intents[0].Text != want {
		t.Fatalf("intents = %+v, want question %q", plan.Intents, want)
	}
}

func TestPlanStaleNeverDispatches(t *testing.T) {
	s := base(task(1))
	s.ForgeOK = false
	s.ForgeError = "gh: 401 unauthorized"
	plan := PlanTick(s, planNow)
	if plan.Status != StatusStale || plan.Error != s.ForgeError {
		t.Fatalf("plan = %+v, want stale with the forge error", plan)
	}
	if len(findIntents(plan, IntentDispatch)) != 0 {
		t.Fatalf("intents = %+v, want no dispatch on a stale snapshot", plan.Intents)
	}
	if !hasEvent(plan, "tick.stale", "test") {
		t.Fatalf("events = %+v, want tick.stale", plan.Events)
	}
}

func TestPlanDispatchIntent(t *testing.T) {
	plan := PlanTick(base(task(1)), planNow)
	intents := findIntents(plan, IntentDispatch)
	if len(intents) != 1 || intents[0].Item != 1 || intents[0].Seat != "seat-1" {
		t.Fatalf("intents = %+v, want one dispatch for #1 on seat-1", plan.Intents)
	}
	if !hasEvent(plan, "dispatch.create", "#1") {
		t.Fatalf("events = %+v, want dispatch.create", plan.Events)
	}
}

// TestIncidentStaleVerdict: D2 — a verdict on a sha that is no longer the head is
// its own state and asks for a re-review, with both shas named.
func TestIncidentStaleVerdict(t *testing.T) {
	s := base(Item{Number: 5, Kind: KindTask, State: StateOpen, Title: "Connection pool"})
	s.CRs = []CRState{{Number: 20, Item: 5, State: "open", HeadSHA: "newsha1"}}
	s.Verdicts = []VerdictState{{Item: 5, SHA: "oldsha1", Decision: "accept"}}
	plan := PlanTick(s, planNow)
	intents := findIntents(plan, IntentRaiseDecision)
	want := "test #5: verdict stale - ACCEPT oldsha1 is on oldsha1, head is newsha1; review newsha1"
	if len(intents) != 1 || intents[0].Text != want {
		t.Fatalf("intents = %+v, want %q", plan.Intents, want)
	}
	if intents[0].Event.Kind != "decision.stale" {
		t.Fatalf("event kind = %q, want decision.stale", intents[0].Event.Kind)
	}
}

// TestIncidentVerdictForHeadRaisesNothing: an accept that names the head is the
// normal review state, not a decision.
func TestIncidentVerdictForHeadRaisesNothing(t *testing.T) {
	s := base(Item{Number: 5, Kind: KindTask, State: StateOpen, Title: "Connection pool"})
	s.CRs = []CRState{{Number: 20, Item: 5, State: "open", HeadSHA: "head123"}}
	s.Verdicts = []VerdictState{{Item: 5, SHA: "head123", Decision: "accept"}}
	plan := PlanTick(s, planNow)
	if len(findIntents(plan, IntentRaiseDecision)) != 0 {
		t.Fatalf("intents = %+v, want none for an accepted head", plan.Intents)
	}
}

// relaySnapshot is the shared shape for the relay arm's tests: an open item,
// an open unmerged change request on head aaaa1111111111111111111111111111111a,
// and a project that opted into the scribe relay mode. Everything a case needs
// differently, it changes on this base.
func relaySnapshot() Snapshot {
	s := base(task(5))
	s.Project.RelayMode = "scribe"
	s.CRs = []CRState{{Number: 20, Item: 5, State: "open", HeadSHA: "aaaa1111111111111111111111111111111a"}}
	return s
}

// reviewerRow is the row a raise leaves for the reviewer seat, naming the item
// and the head.
func reviewerRow(state, head string) Row {
	return Row{
		QItemID:     "q-1",
		State:       state,
		Destination: "dev-review@rig-a",
		Body:        "test #5: review change request #20, head " + head,
		Summary:     "re-review #5",
	}
}

func TestPlanRelayReviewerRowPending(t *testing.T) {
	s := relaySnapshot()
	s.Rows = []Row{reviewerRow("pending", "aaaa1111111111111111111111111111111a")}
	plan := PlanTick(s, planNow)
	if relays := findIntents(plan, IntentRelayVerdict); len(relays) != 0 {
		t.Fatalf("relays = %+v, want none: the reviewer is still working", relays)
	}
}

func TestPlanRelayReviewerRowEnded(t *testing.T) {
	s := relaySnapshot()
	s.Rows = []Row{reviewerRow("done", "aaaa1111111111111111111111111111111a")}
	plan := PlanTick(s, planNow)
	relays := findIntents(plan, IntentRelayVerdict)
	if len(relays) != 1 {
		t.Fatalf("relays = %d, want 1: the review ended without a verdict", len(relays))
	}
	relay := relays[0]
	if relay.Item != 5 || relay.ChangeRequest != 20 || relay.HeadSHA != "aaaa1111111111111111111111111111111a" {
		t.Fatalf("relay = %+v, want item 5 cr 20 head aaaa111", relay)
	}
	if relay.Seat != "" {
		t.Fatalf("relay.Seat = %q, want empty: act resolves the destination", relay.Seat)
	}
	for _, fact := range []string{"#5", "#20", "aaaa1111111111111111111111111111111a", "without a verdict"} {
		if !strings.Contains(relay.Text, fact) {
			t.Fatalf("relay text %q missing %q", relay.Text, fact)
		}
	}
	if !hasEvent(plan, "verdict.relayed", "#5") {
		t.Fatalf("events = %+v, want verdict.relayed for #5", plan.Events)
	}
}

func TestPlanRelayVerdictAtHeadSuppresses(t *testing.T) {
	full := "aaaa1111111111111111111111111111111a"
	for _, sha := range []string{full, "aaaa111", "AAAA1111111111111111111111111111111A"} {
		s := relaySnapshot()
		s.Verdicts = []VerdictState{{Item: 5, SHA: sha, Decision: "accept"}}
		s.Rows = []Row{reviewerRow("done", full)}
		plan := PlanTick(s, planNow)
		if relays := findIntents(plan, IntentRelayVerdict); len(relays) != 0 {
			t.Fatalf("sha %q: relays = %+v, want none: a verdict at the head suppresses the relay", sha, relays)
		}
	}
}

func TestPlanRelayMovedHeadAsksAgain(t *testing.T) {
	s := relaySnapshot()
	s.RelayedHeads = []RelayedHead{{Item: 5, Head: "aaaa1111111111111111111111111111111a"}}
	s.Rows = []Row{
		reviewerRow("done", "aaaa1111111111111111111111111111111a"),
		reviewerRow("done", "bbbb2222222222222222222222222222222b"),
	}
	s.CRs = []CRState{{Number: 20, Item: 5, State: "open", HeadSHA: "bbbb2222222222222222222222222222222b"}}
	plan := PlanTick(s, planNow)
	relays := findIntents(plan, IntentRelayVerdict)
	if len(relays) != 1 {
		t.Fatalf("relays = %d, want 1: the moved head is a new fact the ledger has not seen", len(relays))
	}
	if relays[0].HeadSHA != "bbbb2222222222222222222222222222222b" {
		t.Fatalf("relay head = %q, want the moved head", relays[0].HeadSHA)
	}
}

func TestPlanRelayOffWithoutMode(t *testing.T) {
	s := relaySnapshot()
	s.Project.RelayMode = ""
	s.Rows = []Row{reviewerRow("done", "aaaa1111111111111111111111111111111a")}
	before := PlanTick(s, planNow)
	if relays := findIntents(before, IntentRelayVerdict); len(relays) != 0 {
		t.Fatalf("relays = %+v, want none: the project never opted in", relays)
	}
	decisions := findIntents(before, IntentRaiseDecision)
	if len(decisions) == 0 || decisions[0].Event.Kind != "decision.no_verdict" {
		t.Fatalf("decisions = %+v, want the unchanged decision.no_verdict question", decisions)
	}
}

func TestPlanRelayLedgerDedupe(t *testing.T) {
	s := relaySnapshot()
	s.Rows = []Row{reviewerRow("done", "aaaa1111111111111111111111111111111a")}
	plan := PlanTick(s, planNow)
	if relays := findIntents(plan, IntentRelayVerdict); len(relays) != 1 {
		t.Fatalf("first plan: relays = %d, want 1", len(relays))
	}
	// The ledger holds the event; the next plan over the same snapshot differs
	// by zero relays (tasks 2.2). The snapshot carries the fact; the plan has
	// no memory of its own.
	s.RelayedHeads = []RelayedHead{{Item: 5, Head: "aaaa1111111111111111111111111111111a"}}
	after := PlanTick(s, planNow)
	if relays := findIntents(after, IntentRelayVerdict); len(relays) != 0 {
		t.Fatalf("after ledger: relays = %+v, want none", relays)
	}
}

func TestPlanRelayLedgerAbbreviatedEntryCounts(t *testing.T) {
	s := relaySnapshot()
	s.Rows = []Row{reviewerRow("done", "aaaa1111111111111111111111111111111a")}
	s.RelayedHeads = []RelayedHead{{Item: 5, Head: "aaaa111"}}
	plan := PlanTick(s, planNow)
	if relays := findIntents(plan, IntentRelayVerdict); len(relays) != 0 {
		t.Fatalf("relays = %+v, want none: an abbreviated ledger entry still names the head", relays)
	}
}

func TestPlanRelayDoesNotTouchDecisionArms(t *testing.T) {
	// The relay arm must not change what the existing decision arms plan: the
	// same snapshot with and without relay facts plans the same decision.*.
	with := relaySnapshot()
	with.Rows = []Row{reviewerRow("done", "aaaa1111111111111111111111111111111a")}
	without := relaySnapshot()
	without.Project.RelayMode = ""
	without.Rows = with.Rows
	a, b := PlanTick(with, planNow), PlanTick(without, planNow)
	var sa, sb []string
	for _, intent := range a.Intents {
		if intent.Kind == IntentRaiseDecision {
			sa = append(sa, intent.Event.Kind+" "+fmt.Sprint(intent.Item))
		}
	}
	for _, intent := range b.Intents {
		if intent.Kind == IntentRaiseDecision {
			sb = append(sb, intent.Event.Kind+" "+fmt.Sprint(intent.Item))
		}
	}
	if strings.Join(sa, "|") != strings.Join(sb, "|") {
		t.Fatalf("decision intents differ with relay on: %v vs off: %v", sa, sb)
	}
}

// TestMalformedVerdictAsksTheReviewerWithTheLiteralLine: the reviewer's own
// failed attempt is its question, not the operator's (epic #345, design
// D2/D4). Exactly one decision.malformed_verdict carries the note id, the
// line as written and the head, and decision.no_verdict does not fire beside
// it — "has no verdict" would be false when the reviewer plainly tried.
func TestMalformedVerdictAsksTheReviewerWithTheLiteralLine(t *testing.T) {
	s := base(Item{Number: 5, Kind: KindTask, State: StateOpen, Title: "Connection pool"})
	s.CRs = []CRState{{Number: 20, Item: 5, State: "open", HeadSHA: "cccc333"}}
	s.MalformedVerdicts = map[int]MalformedVerdict{5: {
		NoteID: "6847905312", Head: "cccc333", Line: "## QA Verdict: ACCEPT ✅",
	}}
	plan := PlanTick(s, planNow)
	asked := findIntents(plan, IntentRaiseDecision)
	if len(asked) != 1 {
		t.Fatalf("intents = %+v, want exactly one decision", plan.Intents)
	}
	intent := asked[0]
	if intent.Event.Kind != "decision.malformed_verdict" {
		t.Fatalf("event kind = %q, want decision.malformed_verdict", intent.Event.Kind)
	}
	if intent.Audience != AudienceReviewer {
		t.Fatalf("audience = %v, want reviewer", intent.Audience)
	}
	if intent.ChangeRequest != 20 || intent.HeadSHA != "cccc333" {
		t.Fatalf("intent = %+v, want the change request and head carried for the brief", intent)
	}
	for _, want := range []string{
		"The merge gate reads one line: ACCEPT cccc333 or REJECT cccc333",
		"note 6847905312", `"## QA Verdict: ACCEPT ✅"`, "names no commit",
		"Post a new comment whose first line is:", "ACCEPT cccc333",
	} {
		if !strings.Contains(intent.Text, want) {
			t.Fatalf("text = %q, want it to carry %q", intent.Text, want)
		}
	}
	if hasEvent(plan, "decision.no_verdict", "#5") {
		t.Fatalf("events = %+v, want no decision.no_verdict beside a malformed question", plan.Events)
	}
}

// TestMalformedQuestionYieldsToAStandingQuestionForTheHead: a live human row
// naming the head is the same question standing (design D4) — neither the
// malformed ask nor `no_verdict` fires again while it stands.
func TestMalformedQuestionYieldsToAStandingQuestionForTheHead(t *testing.T) {
	s := base(Item{Number: 5, Kind: KindTask, State: StateOpen, Title: "Connection pool"})
	s.CRs = []CRState{{Number: 20, Item: 5, State: "open", HeadSHA: "cccc333"}}
	s.MalformedVerdicts = map[int]MalformedVerdict{5: {
		NoteID: "6847905312", Head: "cccc333", Line: "## QA Verdict: ACCEPT ✅",
	}}
	s.Rows = append(s.Rows, Row{QItemID: "qitem-mv-1", State: "pending", Pickup: "unclaimed",
		Destination: "human@kernel", Body: "app #5: your note 6847905312 names no commit; head cccc333"})
	plan := PlanTick(s, planNow)
	if asked := findIntents(plan, IntentRaiseDecision); len(asked) != 0 {
		t.Fatalf("intents = %+v, want neither question while one stands for the head", plan.Intents)
	}
}

// TestMalformedNoteForAnOlderHeadYieldsNeither: the fact was recorded against
// a head that is gone. The reviewer's malformed row is still the live
// question, and the operator's "no verdict" would still be false — so the
// plan raises neither until a note names the current head.
func TestMalformedNoteForAnOlderHeadYieldsNeither(t *testing.T) {
	s := base(Item{Number: 5, Kind: KindTask, State: StateOpen, Title: "Connection pool"})
	s.CRs = []CRState{{Number: 20, Item: 5, State: "open", HeadSHA: "cccc333"}}
	s.MalformedVerdicts = map[int]MalformedVerdict{5: {
		NoteID: "6847905312", Head: "aaaa111", Line: "## QA Verdict: ACCEPT ✅",
	}}
	plan := PlanTick(s, planNow)
	if asked := findIntents(plan, IntentRaiseDecision); len(asked) != 0 {
		t.Fatalf("intents = %+v, want neither question for a stale malformed fact", plan.Intents)
	}
	if hasEvent(plan, "decision.no_verdict", "#5") {
		t.Fatalf("events = %+v, want no no_verdict beside a stale malformed fact", plan.Events)
	}
	if hasEvent(plan, "decision.malformed_verdict", "#5") {
		t.Fatalf("events = %+v, want no malformed question for another head", plan.Events)
	}
}
