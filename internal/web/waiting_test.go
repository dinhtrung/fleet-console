package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
	"github.com/gofiber/fiber/v3"
)

// waitingNow is the harness's fixed clock: the panel measures every age against
// it, so an assertion is a fact rather than a race.
var waitingNow = time.Date(2026, 11, 14, 1, 30, 0, 0, time.UTC)

const waitingConfig = `listen: 127.0.0.1:8787

projects:
  - key: app
    name: app
    forge: github
    repo: o/r
    rigs: [rig]
    concurrency_cap: 2
`

// waitingHarness builds the app over a store holding one project row, a config
// naming the same project, and a rig adapter whose `status --json` answers the
// UI port. The panel is the only surface under test.
type waitingHarness struct {
	app    *fiber.App
	st     *store.Store
	pid    int64
	dbPath string
}

func newWaitingHarness(t *testing.T, statusJSON string) *waitingHarness {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(waitingConfig), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	ctx := context.Background()
	dbPath := filepath.Join(dir, "fleet.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if _, _, err := st.UpsertProject(ctx, store.UpsertInput{
		Project: store.ProjectInput{
			Key: "app", Name: "app", ForgeKind: "github", ForgeProject: "o/r",
			Rig: "rig", ConcurrencyCap: 2, Enabled: true,
		},
	}); err != nil {
		t.Fatalf("project: %v", err)
	}
	projects, err := st.Projects(ctx)
	if err != nil || len(projects) != 1 {
		t.Fatalf("projects = %v (%v)", projects, err)
	}
	adapter := rig.NewAdapter(scriptedWaitingRunner{status: statusJSON})
	app, err := New(Config{
		Token: testToken, Store: st, ConfigPath: configPath, Rig: adapter,
		Now: func() time.Time { return waitingNow },
	})
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	return &waitingHarness{app: app, st: st, pid: projects[0].ID, dbPath: dbPath}
}

// scriptedWaitingRunner answers only `rig status --json`; any other call fails
// loudly, because the list endpoint must not make a second rig call.
type scriptedWaitingRunner struct{ status string }

func (r scriptedWaitingRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	if strings.Join(args, " ") != "status --json" {
		return nil, errors.New("unexpected rig call: " + strings.Join(args, " "))
	}
	return []byte(r.status), nil
}

// seedSeats writes the mirror's `rig_seats` snapshot the board reads.
func (h *waitingHarness) seedSeats(t *testing.T, seats []rig.Seat, at time.Time) {
	t.Helper()
	if err := h.st.PutSnapshot(context.Background(), h.pid, "rig_seats", seats, at); err != nil {
		t.Fatalf("seats snapshot: %v", err)
	}
}

// seedStatus writes the tick's status block: the rig read's health.
func (h *waitingHarness) seedStatus(t *testing.T, block map[string]any, at time.Time) {
	t.Helper()
	if err := h.st.PutSnapshot(context.Background(), h.pid, "status", block, at); err != nil {
		t.Fatalf("status snapshot: %v", err)
	}
}

// waiting reads GET /api/projects and returns the one project's panel.
func (h *waitingHarness) waiting(t *testing.T) WaitingPanel {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer "+testToken)
	resp, err := h.app.Test(req)
	if err != nil {
		t.Fatalf("projects: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var answer struct {
		Projects []struct {
			Key     string       `json:"key"`
			Waiting WaitingPanel `json:"waiting"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	for _, project := range answer.Projects {
		if project.Key == "app" {
			return project.Waiting
		}
	}
	t.Fatalf("no app project in %s", body)
	return WaitingPanel{}
}

// TestWaitingPanelListsOneSeat is the panel's first acceptance: a seat the rig
// reports as stopped on a person is listed with its seat, rig, question and
// age, and the panel links to the UI where the person answers it (#429).
func TestWaitingPanelListsOneSeat(t *testing.T) {
	now := time.Date(2026, 11, 14, 1, 30, 0, 0, time.UTC)
	h := newWaitingHarness(t, `{"daemon":{"state":"running","reachable":true,"port":7433}}`)
	h.seedSeats(t, []rig.Seat{{
		Session: "dev-review@rig", Rig: "rig", Lifecycle: "attention_required",
		Attention: []rig.SeatAttention{{
			Seat: "dev-review@rig", Rig: "rig", Kind: rig.AttentionKindAttention,
			Question: "Harness launch requires attention: press enter",
		}},
	}}, now.Add(-4*time.Minute))

	panel := h.waiting(t)
	if !panel.Known {
		t.Fatalf("panel = %+v, want known", panel)
	}
	if len(panel.Seats) != 1 {
		t.Fatalf("seats = %+v, want one", panel.Seats)
	}
	seat := panel.Seats[0]
	if seat.Seat != "dev-review@rig" || seat.Rig != "rig" || seat.Kind != rig.AttentionKindAttention {
		t.Fatalf("seat = %+v, want the seat, its rig and kind", seat)
	}
	if !strings.Contains(seat.Question, "Harness launch requires attention") {
		t.Fatalf("question = %q, want the rig's text", seat.Question)
	}
	if seat.Since != "4m" {
		t.Fatalf("since = %q, want the age of the read", seat.Since)
	}
	if panel.UIURL != "http://127.0.0.1:7433" {
		t.Fatalf("ui url = %q, want the daemon's own address", panel.UIURL)
	}
}

// TestWaitingPanelUnreadRigIsUnknown is the spec's third scenario: a failed rig
// read must render unknown, never as a project with nobody waiting.
func TestWaitingPanelUnreadRigIsUnknown(t *testing.T) {
	now := time.Date(2026, 11, 14, 1, 30, 0, 0, time.UTC)
	h := newWaitingHarness(t, `{"daemon":{"state":"running","reachable":true,"port":7433}}`)
	h.seedSeats(t, []rig.Seat{{Session: "dev-review@rig", Rig: "rig"}}, now)
	h.seedStatus(t, map[string]any{"rig_stale": true, "rig_error": "rig ps: timeout"}, now)

	panel := h.waiting(t)
	if panel.Known {
		t.Fatalf("panel = %+v, want unknown after a failed rig read", panel)
	}
	if !strings.Contains(panel.Reason, "rig ps: timeout") {
		t.Fatalf("reason = %q, want the read's own words", panel.Reason)
	}
	if len(panel.Seats) != 0 {
		t.Fatalf("seats = %+v, want none listed as fact", panel.Seats)
	}
	if panel.UIURL != "" {
		t.Fatalf("ui url = %q, want no link when nothing is known", panel.UIURL)
	}
}

// TestWaitingPanelNeverReadIsUnknown: no snapshot at all is a rig the app has
// never read — unknown, with the reason in words.
func TestWaitingPanelNeverReadIsUnknown(t *testing.T) {
	h := newWaitingHarness(t, `{"daemon":{"state":"running","reachable":true,"port":7433}}`)
	panel := h.waiting(t)
	if panel.Known {
		t.Fatalf("panel = %+v, want unknown before any read", panel)
	}
	if panel.Reason == "" {
		t.Fatalf("panel = %+v, want a stated reason", panel)
	}
}

// TestWaitingPanelSeatWithoutQuestion: the seat is still listed, saying the rig
// gave no question — a silent prompt is a wait, not an idle seat.
func TestWaitingPanelSeatWithoutQuestion(t *testing.T) {
	now := time.Date(2026, 11, 14, 1, 30, 0, 0, time.UTC)
	h := newWaitingHarness(t, `{"daemon":{"state":"running","reachable":true,"port":7433}}`)
	h.seedSeats(t, []rig.Seat{{
		Session: "dev-builder-1@rig", Rig: "rig", Lifecycle: "attention_required",
		Attention: []rig.SeatAttention{{Seat: "dev-builder-1@rig", Rig: "rig", Kind: rig.AttentionKindArbitrated}},
	}}, now)

	panel := h.waiting(t)
	if len(panel.Seats) != 1 {
		t.Fatalf("seats = %+v, want the silent seat listed", panel.Seats)
	}
	if panel.Seats[0].Question != "" || !panel.Seats[0].Asking {
		t.Fatalf("seat = %+v, want no text and Asking set", panel.Seats[0])
	}
}

// TestWaitingPanelAgesFromTheLedger: a question first raised 20 minutes ago and
// still standing is 20m old, not as new as the last read (#427's ledger is the
// age's source; the snapshot is the fallback).
func TestWaitingPanelAgesFromTheLedger(t *testing.T) {
	now := time.Date(2026, 11, 14, 1, 30, 0, 0, time.UTC)
	h := newWaitingHarness(t, `{"daemon":{"state":"running","reachable":true,"port":7433}}`)
	h.seedSeats(t, []rig.Seat{{
		Session: "dev-review@rig", Rig: "rig", Lifecycle: "attention_required",
		Attention: []rig.SeatAttention{{
			Seat: "dev-review@rig", Rig: "rig", Kind: rig.AttentionKindAttention, Question: "press enter",
		}},
	}}, now.Add(-30*time.Second))
	// The ledger's raise predates the read: the wait began 20 minutes ago.
	if _, err := h.st.AppendEvent(context.Background(), store.Event{
		Actor: "act", Kind: "attention.raised", Subject: "dev-review@rig",
		Detail: "kind=attention; rig=rig; question=press enter;",
	}); err != nil {
		t.Fatalf("event: %v", err)
	}
	// Rewrite the raise's timestamp: AppendEvent stamps now, and the case is
	// about an older raise, not about the writer.
	h.backdateEvents(t, "attention.raised", now.Add(-20*time.Minute))

	panel := h.waiting(t)
	if len(panel.Seats) != 1 {
		t.Fatalf("seats = %+v, want one", panel.Seats)
	}
	if panel.Seats[0].Since != "20m" {
		t.Fatalf("since = %q, want the ledger's age, not the read's", panel.Seats[0].Since)
	}
}

// backdateEvents rewrites one kind's timestamps, so a test can model a wait that
// began before the read without waiting for wall-clock time. It uses a second
// connection to the same file: the store exposes no raw handle on purpose, and
// the driver it registers is process-wide.
func (h *waitingHarness) backdateEvents(t *testing.T, kind string, at time.Time) {
	t.Helper()
	db, err := sql.Open("sqlite", h.dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE event SET at = ? WHERE kind = ?`, at.UTC().Format(time.RFC3339Nano), kind); err != nil {
		t.Fatalf("backdate: %v", err)
	}
}
