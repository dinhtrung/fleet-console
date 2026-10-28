package web

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
	"github.com/gofiber/fiber/v3"
)

// Fleet is the read model behind the product screens.
type Fleet struct {
	Store *store.Store
	Rig   *rig.Adapter
	Hub   *Hub
	Now   func() time.Time
}

type boardItem struct {
	Number int      `json:"number"`
	Title  string   `json:"title"`
	State  string   `json:"state"`
	Seat   string   `json:"seat,omitempty"`
	Age    string   `json:"age"`
	Reason string   `json:"reason"`
	URL    string   `json:"url,omitempty"`
	Labels []string `json:"labels"`
}

type boardCounts struct {
	Building int `json:"building"`
	Review   int `json:"review"`
	Waiting  int `json:"waiting"`
	Parked   int `json:"parked"`
	Blocked  int `json:"blocked"`
}

type boardProject struct {
	Key    string      `json:"key"`
	Name   string      `json:"name"`
	Paused bool        `json:"paused"`
	Counts boardCounts `json:"counts"`
	Items  []boardItem `json:"items"`
}

func (f *Fleet) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now().UTC()
}

func age(since string) string {
	if since == "" {
		return "—"
	}
	t, err := time.Parse(time.RFC3339Nano, since)
	if err != nil {
		if t, err = time.Parse(time.RFC3339, since); err != nil {
			return "—"
		}
	}
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func splitLabels(labels string) []string {
	fields := strings.FieldsFunc(labels, func(r rune) bool { return r == ',' || r == ' ' })
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

func hasLabel(labels []string, want string) bool {
	for _, l := range labels {
		if l == want {
			return true
		}
	}
	return false
}

func labelValue(labels []string, prefix string) string {
	for _, l := range labels {
		if strings.HasPrefix(l, prefix) {
			return strings.TrimPrefix(l, prefix)
		}
	}
	return ""
}

func parseInts(raw string) []int {
	var out []int
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

// Board assembles projects, counts and one reason per task from stored facts.
func (f *Fleet) Board(c fiber.Ctx) error {
	ctx := c.Context()
	projects, err := f.Store.Projects(ctx)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	items, err := f.Store.Items(ctx)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	byProject := map[int64][]store.ItemRow{}
	for _, item := range items {
		byProject[item.ProjectID] = append(byProject[item.ProjectID], item)
	}
	openNumbers := map[int64]map[int]string{}
	for _, p := range projects {
		openNumbers[p.ID] = map[int]string{}
		for _, item := range byProject[p.ID] {
			openNumbers[p.ID][item.Number] = item.State
		}
	}

	result := make([]boardProject, 0, len(projects))
	for _, p := range projects {
		dispatches := map[int]store.DispatchRow{}
		live := 0
		for _, item := range byProject[p.ID] {
			list, err := f.Store.Dispatches(ctx, p.ID, item.Number)
			if err != nil {
				return fiber.NewError(fiber.StatusInternalServerError, err.Error())
			}
			if len(list) > 0 {
				dispatches[item.Number] = list[0]
				if list[0].EndedAt == nil {
					live++
				}
			}
		}
		bp := boardProject{Key: p.Key, Name: p.Name, Paused: p.OnHold, Items: []boardItem{}}
		for _, item := range byProject[p.ID] {
			if item.State != "open" {
				continue
			}
			labels := splitLabels(item.Labels)
			row := boardItem{
				Number: item.Number, Title: item.Title, State: "queued", Reason: "queued",
				Age: age(item.UpdatedAt), URL: item.URL, Labels: labels,
			}
			d := dispatches[item.Number]
			switch {
			case p.OnHold:
				row.State, row.Reason = "paused", "project paused"
			case item.ParentNumber != nil && openNumbers[p.ID][*item.ParentNumber] != "open":
				row.State, row.Reason = "blocked", fmt.Sprintf("parent #%d is not open", *item.ParentNumber)
			}
			if row.State == "queued" {
				for _, blocked := range parseInts(item.BlockedByJSON) {
					if openNumbers[p.ID][blocked] == "open" {
						row.State, row.Reason = "waiting", fmt.Sprintf("waits on #%d", blocked)
						break
					}
				}
			}
			if row.State == "queued" && d.ItemNumber != 0 {
				row.Seat = d.Seat
				switch {
				case d.EndedAt == nil:
					row.State, row.Reason = "building", ""
				case hasLabel(labels, "cr:open"):
					verdicts, err := f.Store.Verdicts(ctx, d.ID)
					if err != nil {
						return fiber.NewError(fiber.StatusInternalServerError, err.Error())
					}
					if len(verdicts) == 0 {
						row.State, row.Reason = "parked", "parked: change request open, no verdict"
					} else {
						row.State, row.Reason = "review", "verdict "+verdicts[0].Decision+", waits for merge"
					}
				case d.EndReason == "merged":
					row.State, row.Reason = "review", "merged"
				case d.EndReason != "":
					row.State, row.Reason = "waiting", "dispatch "+d.EndReason
				}
				row.Age = age(d.CreatedAt)
			}
			if row.State == "queued" && live >= p.ConcurrencyCap && p.ConcurrencyCap > 0 {
				row.State, row.Reason = "waiting", "no free builder"
			}
			switch row.State {
			case "building":
				bp.Counts.Building++
			case "review":
				bp.Counts.Review++
			case "parked":
				bp.Counts.Parked++
			case "waiting":
				bp.Counts.Waiting++
			case "blocked", "paused":
				bp.Counts.Blocked++
			}
			bp.Items = append(bp.Items, row)
		}
		sort.Slice(bp.Items, func(i, j int) bool { return bp.Items[i].Number < bp.Items[j].Number })
		result = append(result, bp)
	}
	// The mirror's stamps decide the board's sync time and stale flag. A failed
	// forge read keeps the previous items and stamp; it never reads as an empty board.
	synced := time.Time{}
	stale := false
	for _, p := range projects {
		status, err := f.Store.MirrorStatus(ctx, p.ID)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		if status.LastForgeAt != nil && status.LastForgeAt.After(synced) {
			synced = *status.LastForgeAt
		}
		stale = stale || status.ForgeStale
	}
	syncedAt := f.now().UTC().Format(time.RFC3339)
	if !synced.IsZero() {
		syncedAt = synced.UTC().Format(time.RFC3339)
	}
	return c.JSON(fiber.Map{"projects": result, "synced_at": syncedAt, "stale": stale})
}

type checkOut struct {
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
}

type itemDetail struct {
	Project  boardProject       `json:"project"`
	Item     boardItem          `json:"item"`
	Dispatch *store.DispatchRow `json:"dispatch,omitempty"`
	Brief    string             `json:"brief,omitempty"`
	Change   *changeRequest     `json:"change_request,omitempty"`
	Verdicts []store.VerdictRow `json:"verdicts"`
	Events   []store.EventRow   `json:"events"`
}

type changeRequest struct {
	Number int        `json:"number"`
	Checks []checkOut `json:"checks"`
}

// Item returns the task drawer's facts: brief, branch, worktree, CR, checks,
// verdict and events.
func (f *Fleet) Item(c fiber.Ctx) error {
	number, err := strconv.Atoi(c.Params("number"))
	if err != nil || number <= 0 {
		return fiber.NewError(fiber.StatusBadRequest, "item number is required")
	}
	ctx := c.Context()
	projects, err := f.Store.Projects(ctx)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	projectKey := c.Query("project")
	for _, p := range projects {
		if projectKey != "" && p.Key != projectKey {
			continue
		}
		item, err := f.Store.Item(ctx, p.ID, number)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		if item == nil {
			continue
		}
		labels := splitLabels(item.Labels)
		detail := itemDetail{
			Project: boardProject{Key: p.Key, Name: p.Name, Paused: p.OnHold},
			Item: boardItem{
				Number: item.Number, Title: item.Title, State: item.State,
				Age: age(item.UpdatedAt), URL: item.URL, Labels: labels,
			},
		}
		dispatches, err := f.Store.Dispatches(ctx, p.ID, number)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		if len(dispatches) > 0 {
			d := dispatches[0]
			detail.Dispatch = &d
			detail.Item.Seat = d.Seat
			if d.BriefPath != "" {
				if data, err := readCapped(d.BriefPath, 8192); err == nil {
					detail.Brief = data
				}
			}
			if pr := labelValue(labels, "pr:"); pr != "" {
				if n, err := strconv.Atoi(pr); err == nil {
					cr := changeRequest{Number: n}
					for _, l := range labels {
						if strings.HasPrefix(l, "check:") {
							parts := strings.SplitN(strings.TrimPrefix(l, "check:"), "=", 2)
							if len(parts) == 2 {
								cr.Checks = append(cr.Checks, checkOut{Name: parts[0], Conclusion: parts[1]})
							}
						}
					}
					detail.Change = &cr
				}
			}
			verdicts, err := f.Store.Verdicts(ctx, d.ID)
			if err != nil {
				return fiber.NewError(fiber.StatusInternalServerError, err.Error())
			}
			detail.Verdicts = verdicts
		}
		events, err := f.Store.EventsForItem(ctx, number, 20)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		detail.Events = events
		return c.JSON(detail)
	}
	return fiber.NewError(fiber.StatusNotFound, "item not found")
}

func readCapped(path string, max int) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(data) > max {
		data = append(data[:max], []byte("\n… truncated")...)
	}
	return string(data), nil
}

type decisionOut struct {
	ID          string   `json:"id"`
	Summary     string   `json:"summary"`
	Body        string   `json:"body"`
	Destination string   `json:"destination"`
	State       string   `json:"state"`
	CreatedAt   string   `json:"created_at"`
	DeadlineAt  string   `json:"deadline_at,omitempty"`
	Tier        string   `json:"tier,omitempty"`
	EvidenceRef string   `json:"evidence_ref,omitempty"`
	Options     []string `json:"options"`
}

func humanDecision(destination string) bool {
	return strings.Contains(destination, "human@") || strings.HasSuffix(destination, "@external")
}

// Decisions reads obligations waiting on a person from the rig, oldest first.
func (f *Fleet) Decisions(c fiber.Ctx) error {
	if f.Rig == nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "rig adapter is not configured")
	}
	rows, err := f.Rig.QueueList(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, err.Error())
	}
	out := make([]decisionOut, 0)
	for _, row := range rows {
		if !humanDecision(row.Destination) {
			continue
		}
		if row.State != "blocked" {
			continue
		}
		item, err := f.Rig.QueueShow(c.Context(), row.ID)
		if err != nil {
			return fiber.NewError(fiber.StatusBadGateway, err.Error())
		}
		d := decisionOut{
			ID: item.ID, Summary: item.Summary, Body: item.Body, Destination: item.Destination,
			State: item.State, CreatedAt: item.CreatedAt, DeadlineAt: item.DeadlineAt,
			Tier: item.Tier, EvidenceRef: item.EvidenceRef,
			Options: []string{"Approve", "Reject"},
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return c.JSON(fiber.Map{"decisions": out})
}

// AnswerDecision records the answer with the rig and one audit event.
func (f *Fleet) AnswerDecision(c fiber.Ctx) error {
	if f.Rig == nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "rig adapter is not configured")
	}
	id := c.Params("id")
	var body struct {
		Decision string `json:"decision"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "body must be JSON")
	}
	decision := strings.TrimSpace(body.Decision)
	if id == "" || decision == "" {
		return fiber.NewError(fiber.StatusBadRequest, "id and decision are required")
	}
	item, err := f.Rig.QueueResolve(c.Context(), id, decision)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, err.Error())
	}
	eventID, err := f.Store.AppendEvent(c.Context(), store.Event{
		Actor: "board", Kind: "decision.answer", Subject: id, Detail: decision,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	f.Hub.Publish("decision.answer", fiber.Map{"id": id, "state": item.State, "event_id": eventID})
	return c.JSON(fiber.Map{"id": id, "state": item.State, "event_id": eventID})
}

type seatOut struct {
	Session      string `json:"session"`
	Rig          string `json:"rig"`
	LogicalID    string `json:"logical_id"`
	State        string `json:"state"`
	Runtime      string `json:"runtime"`
	Model        string `json:"model"`
	Permission   string `json:"permission"`
	LastActivity string `json:"last_activity"`
}

// Seats reads the live topology.
func (f *Fleet) Seats(c fiber.Ctx) error {
	if f.Rig == nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "rig adapter is not configured")
	}
	seats, err := f.Rig.Seats(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, err.Error())
	}
	out := make([]seatOut, 0, len(seats))
	for _, seat := range seats {
		out = append(out, seatOut{
			Session: seat.Session, Rig: seat.Rig, LogicalID: seat.LogicalID, State: seat.Lifecycle,
			Runtime: seat.Runtime, Model: seat.Model, Permission: seat.Permission,
			LastActivity: seat.LastActivity,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Session < out[j].Session })
	projects, err := f.Store.Projects(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	synced := time.Time{}
	stale := false
	for _, project := range projects {
		status, err := f.Store.MirrorStatus(c.Context(), project.ID)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		if status.LastRigAt != nil && status.LastRigAt.After(synced) {
			synced = *status.LastRigAt
		}
		stale = stale || status.RigStale
	}
	syncedAt := f.now().UTC().Format(time.RFC3339)
	if !synced.IsZero() {
		syncedAt = synced.UTC().Format(time.RFC3339)
	}
	return c.JSON(fiber.Map{"seats": out, "synced_at": syncedAt, "stale": stale})
}

// SeatTail reads a read-only pane tail.
func (f *Fleet) SeatTail(c fiber.Ctx) error {
	if f.Rig == nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "rig adapter is not configured")
	}
	session, err := url.PathUnescape(c.Params("session"))
	if err != nil {
		session = c.Params("session")
	}
	lines := 20
	if raw := c.Query("lines"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 200 {
			lines = n
		}
	}
	tail, err := f.Rig.Capture(c.Context(), session, lines)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, err.Error())
	}
	return c.JSON(fiber.Map{"session": session, "lines": lines, "tail": tail})
}

// Events returns the newest audit rows.
func (f *Fleet) Events(c fiber.Ctx) error {
	limit := 50
	if raw := c.Query("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	events, err := f.Store.RecentEvents(c.Context(), limit)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	return c.JSON(fiber.Map{"events": events})
}
