package web

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/dinhtrung/fleet-console/internal/act"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/store"
	"github.com/gofiber/fiber/v3"
)

// operatorActor is the audit identity a board verdict is recorded under. Every
// operator on the box shares one bearer token, so the record names the board
// rather than inventing a person.
const operatorActor = "operator@board"

// verdictOut is the answer to one approve/reject: the note that was posted, the
// sha it names, and where the merge is expected to happen.
type verdictOut struct {
	Project       string     `json:"project"`
	Item          int        `json:"item"`
	Verdict       string     `json:"verdict"`
	SHA           string     `json:"sha"`
	ChangeRequest int        `json:"change_request"`
	Ready         bool       `json:"ready"`
	Merge         string     `json:"merge"` // act-tick | manual
	Checks        []checkOut `json:"checks,omitempty"`
}

// ItemVerdict posts the operator's approve/reject on an item's open change
// request in the shape the merge gate already reads: the first line of the note
// is "ACCEPT <sha>" or "REJECT <sha>". A seat verdict and a human verdict are
// then the same record, which is the point — the gate takes the newest one, so a
// human can overrule a seat instead of waiting for one.
//
// An ACCEPT also takes the change request out of draft, because the fleet opens
// every pull request as a draft on purpose and a merge refuses a draft. The
// merge itself stays with the act tick: it re-reads the checks and the
// mergeability at the judged head, so a note posted against a moved head parks
// instead of merging stale work.
func (f *Fleet) ItemVerdict(c fiber.Ctx) error {
	if f.ForgeFor == nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "forge adapter is not configured")
	}
	number, err := strconv.Atoi(c.Params("number"))
	if err != nil || number <= 0 {
		return fiber.NewError(fiber.StatusBadRequest, "item number is required")
	}
	var body struct {
		Verdict string `json:"verdict"`
		Reason  string `json:"reason"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "body must be JSON")
	}
	verdict := strings.ToLower(strings.TrimSpace(body.Verdict))
	if verdict != "accept" && verdict != "reject" {
		return fiber.NewError(fiber.StatusBadRequest, `verdict must be "accept" or "reject"`)
	}
	reason := strings.TrimSpace(body.Reason)
	if verdict == "reject" && reason == "" {
		return fiber.NewError(fiber.StatusBadRequest, "a reject needs a reason: it is the work list the builder reads")
	}
	if verdict == "accept" && reason == "" {
		reason = "approved on the board"
	}

	ctx := c.Context()
	projects, err := f.Store.Projects(ctx)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	projectKey := c.Query("project")
	for _, project := range projects {
		if projectKey != "" && project.Key != projectKey {
			continue
		}
		item, err := f.Store.Item(ctx, project.ID, number)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		if item == nil {
			continue
		}
		source, err := f.ForgeFor(ctx, project)
		if err != nil {
			return fiber.NewError(fiber.StatusServiceUnavailable, err.Error())
		}
		requests, err := source.ChangeRequests(ctx)
		if err != nil {
			return fiber.NewError(fiber.StatusBadGateway, err.Error())
		}
		request, found := openChangeRequest(requests, number)
		if !found {
			return fiber.NewError(fiber.StatusConflict, "this item has no open change request to judge")
		}
		if request.HeadSHA == "" {
			return fiber.NewError(fiber.StatusConflict, "the change request has no head sha to name")
		}

		// The dispatch row (when the item has one) names the attempt both the
		// merge field below and a permission refusal dedupe on (#260).
		dispatches, err := f.Store.Dispatches(ctx, project.ID, number)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		attempt := 0
		if len(dispatches) > 0 {
			attempt = dispatches[0].Attempt
		}

		note := strings.ToUpper(verdict) + " " + request.HeadSHA + "\n\n" + reason
		if err := source.Comment(ctx, request.Number, note); err != nil {
			f.raisePermission(ctx, project, item, request, "comment", err, attempt)
			return fiber.NewError(fiber.StatusBadGateway, err.Error())
		}
		// The verdict is on the forge; the tick that reads it should not wait out
		// the idle backoff (up to idle_poll_max) to capture and merge on it.
		wake(f.Waker)
		ready := false
		if verdict == "accept" && request.Draft {
			if err := source.Ready(ctx, request.Number); err != nil {
				f.raisePermission(ctx, project, item, request, "ready", err, attempt)
				return fiber.NewError(fiber.StatusBadGateway, err.Error())
			}
			ready = true
		}

		// The verdict row is written by the act tick's capture, from the note
		// this request just posted, so there is one record and not two. An item
		// with no dispatch row is the documented adoption gap: the note stands as
		// evidence and the merge stays a human's, which the answer says out loud.
		merge := "act-tick"
		if len(dispatches) == 0 {
			merge = "manual"
		}
		if _, err := f.Store.AppendEvent(ctx, store.Event{
			Actor:   operatorActor,
			Kind:    "human-verdict",
			Subject: subjectFor(project.Key, number),
			Detail: strings.ToUpper(verdict) + " " + request.HeadSHA + " on change request #" +
				strconv.Itoa(request.Number) + ": " + reason,
		}); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		checks := make([]checkOut, 0, len(request.Checks))
		for _, check := range request.Checks {
			checks = append(checks, checkOut{Name: check.Name, Conclusion: check.Conclusion})
		}
		if f.Hub != nil {
			f.Hub.Publish("verdict", map[string]any{
				"project": project.Key, "item": number, "verdict": verdict, "sha": request.HeadSHA,
			})
		}
		return c.JSON(verdictOut{
			Project: project.Key, Item: number, Verdict: verdict, SHA: request.HeadSHA,
			ChangeRequest: request.Number, Ready: ready, Merge: merge, Checks: checks,
		})
	}
	return fiber.NewError(fiber.StatusNotFound, "item not found")
}

// raisePermission asks the operator to fix the forge token when the board's
// own write was refused for permission (#260): the same raise the act loop
// runs, deduped on the same event ledger, so a refusal the board and a tick
// both hit is one question, not two. The 502 still answers — the note did not
// land — and a failed raise is silent: the refusal the operator is looking at
// already names the permission class, and the board retry re-raises.
func (f *Fleet) raisePermission(ctx context.Context, project store.ProjectRow, item *store.ItemRow, request forge.ChangeRequest, operation string, err error, attempt int) {
	var refusal *forge.PermissionError
	if !errors.As(err, &refusal) {
		return
	}
	if f.Rig == nil || f.Store == nil {
		return
	}
	actions := &act.Actions{Store: f.Store, Rig: f.Rig, ProjectID: project.ID, ProjectKey: project.Key}
	_ = actions.RefusePermission(ctx, item.Number, item.Title, operation, err, attempt, request.Number, request.HeadSHA)
}

// openChangeRequest picks the change request this item's verdict belongs to:
// the newest open, unmerged request that closes it. A body that names the item
// twice (two open branches) resolves to the higher number, which is the one the
// operator saw on the board.
func openChangeRequest(requests []forge.ChangeRequest, item int) (forge.ChangeRequest, bool) {
	var best forge.ChangeRequest
	found := false
	for _, request := range requests {
		if request.Merged || request.ClosesItem != item || request.State == forge.StateClosed {
			continue
		}
		if !found || request.Number > best.Number {
			best, found = request, true
		}
	}
	return best, found
}

func subjectFor(project string, number int) string {
	return project + "#" + strconv.Itoa(number)
}
