package act

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/dinhtrung/fleet-console/internal/decide"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// TickInput is one project's decide plan plus the snapshot it came from. Apply
// false makes the tick a pure no-op; the caller keeps the dry-run output.
type TickInput struct {
	Store        *store.Store
	Project      store.ProjectRow
	Forge        forge.Forge
	Rig          Rig
	Git          Git
	Gate         Gate
	Apply        bool
	Items        []forge.Item
	CRs          []forge.ChangeRequest
	Rows         []rig.QueueRow
	Plan         decide.Plan
	WorktreeRoot string
	Now          func() time.Time
	// OnDecision receives every decision this tick raised, so the caller can
	// put it on somebody's phone. It is called after the row exists. It cannot
	// fail the tick: a dead transport is not a dead fleet (design.md §5), so a
	// nil hook and a broken one are the same to the caller — nothing is sent,
	// and the tick carries on.
	OnDecision func(DecisionRaised)
}

// DecisionRaised is one decision a tick created, described the way the phone
// message needs it and the way the tick already knows it.
type DecisionRaised struct {
	QItemID string // the queue row holding the question
	Item    int    // the issue it is about
	Text    string // the question, as the plan worded it
}

// TickReport is what one applied tick did, for the log and the board.
type TickReport struct {
	Applied    bool
	Blocked    bool
	Reason     string
	Dispatches []DispatchResult
	Verdicts   []CapturedVerdict
	Merges     []*MergeResult
	Decisions  []string
	Skipped    []string
}

var tickIssueNumber = regexp.MustCompile(`#(\d+)\b`)

// RunTick executes one project's plan. The gate is checked first: a blocked
// gate performs no write and returns the one named reason. Without Apply the
// tick is a no-op. With Apply it captures verdicts, merges on the four facts,
// then dispatches the plan; every actual write goes through Actions, which
// records one audit event per write.
func RunTick(ctx context.Context, in TickInput) (TickReport, error) {
	report := TickReport{Applied: in.Apply}
	if err := in.Gate.Check(); err != nil {
		report.Blocked = true
		report.Reason = refusalReason(err)
		return report, nil
	}
	if !in.Apply {
		return report, nil
	}
	if in.Store == nil || in.Forge == nil || in.Rig == nil || in.Git == nil {
		return report, fmt.Errorf("act tick: store, forge, rig and git are required")
	}
	actions := &Actions{Store: in.Store, Forge: in.Forge, Rig: in.Rig, Git: in.Git, Now: in.Now}

	// Verdict capture and the merge watchdog run before new dispatches: a merged
	// task frees its seat, and the same tick must not re-dispatch it.
	dispatches, err := in.Store.DispatchesForProject(ctx, in.Project.ID)
	if err != nil {
		return report, err
	}
	for _, dispatch := range dispatches {
		if dispatch.EndedAt != nil || dispatch.ItemNumber == 0 {
			continue
		}
		request := changeRequestFor(in.CRs, dispatch.ItemNumber)
		if request == nil {
			continue
		}
		// The verdict is posted on the change request, under the change
		// request's own number; item is only for the report and the store.
		captured, err := actions.CaptureFromForge(ctx, dispatch.ID, dispatch.ItemNumber, request.Number, request.HeadSHA)
		if err != nil {
			report.Skipped = append(report.Skipped, fmt.Sprintf("#%d verdict: %v", dispatch.ItemNumber, err))
			continue
		}
		if captured != nil {
			report.Verdicts = append(report.Verdicts, *captured)
		}
		merged, err := actions.Merge(ctx, dispatch.ID, dispatch.ItemNumber)
		if err != nil {
			report.Skipped = append(report.Skipped, fmt.Sprintf("#%d merge waiting: %v", dispatch.ItemNumber, err))
			continue
		}
		report.Merges = append(report.Merges, merged)
	}

	for _, intent := range in.Plan.Intents {
		if intent.Kind != decide.IntentRaiseDecision {
			continue
		}
		result, err := actions.RaiseDecision(ctx, in.Project, intent.Item, intent.Text, intent.Event.Kind, in.WorktreeRoot)
		if err != nil {
			report.Skipped = append(report.Skipped, fmt.Sprintf("#%d decision: %v", intent.Item, err))
			continue
		}
		if !result.NoOp {
			report.Decisions = append(report.Decisions, result.QItemID)
			// The row exists before the phone is told about it, so a message
			// never points at something the store cannot show.
			if in.OnDecision != nil {
				in.OnDecision(DecisionRaised{QItemID: result.QItemID, Item: intent.Item, Text: intent.Text})
			}
		}
	}

	for _, intent := range in.Plan.Intents {
		if intent.Kind != decide.IntentDispatch {
			continue
		}
		item := itemFor(in.Items, intent.Item)
		if item == nil {
			report.Skipped = append(report.Skipped, fmt.Sprintf("#%d is not in the mirror", intent.Item))
			continue
		}
		if liveRowNames(in.Rows, intent.Item) {
			report.Skipped = append(report.Skipped, fmt.Sprintf("#%d has a live row", intent.Item))
			continue
		}
		if changeRequestFor(in.CRs, intent.Item) != nil {
			report.Skipped = append(report.Skipped, fmt.Sprintf("#%d already has a change request", intent.Item))
			continue
		}
		result, err := actions.Dispatch(ctx, DispatchInput{
			ProjectID: in.Project.ID, ProjectKey: in.Project.Key, Item: *item,
			Seat: intent.Seat, WorktreeRoot: in.WorktreeRoot,
		})
		if err != nil {
			report.Skipped = append(report.Skipped, fmt.Sprintf("#%d dispatch: %v", intent.Item, err))
			continue
		}
		if result.NoOp {
			report.Skipped = append(report.Skipped, fmt.Sprintf("#%d already dispatched %s", intent.Item, result.QItemID))
			continue
		}
		report.Dispatches = append(report.Dispatches, *result)
	}
	return report, nil
}

func refusalReason(err error) string {
	var refusal *Refusal
	if errors.As(err, &refusal) {
		return refusal.Reason
	}
	return err.Error()
}

func itemFor(items []forge.Item, number int) *forge.Item {
	for index := range items {
		if items[index].Number == number {
			return &items[index]
		}
	}
	return nil
}

func changeRequestFor(requests []forge.ChangeRequest, item int) *forge.ChangeRequest {
	for index := range requests {
		if requests[index].ClosesItem == item || forge.ParseCloses(requests[index].Body) == item {
			return &requests[index]
		}
	}
	return nil
}

func liveRowNames(rows []rig.QueueRow, item int) bool {
	want := fmt.Sprintf("%d", item)
	for _, row := range rows {
		state := strings.ToLower(row.PickupState)
		if state == "" {
			state = strings.ToLower(row.State)
		}
		switch state {
		case "terminal", "closed", "done", "cancelled", "canceled", "denied", "failed",
			"handed-off", "handed_off", "handedoff":
			continue
		}
		head := row.Body
		if len(head) > 400 {
			head = head[:400]
		}
		head += " " + row.Summary
		for _, match := range tickIssueNumber.FindAllStringSubmatch(head, -1) {
			if match[1] == want {
				return true
			}
		}
	}
	return false
}
