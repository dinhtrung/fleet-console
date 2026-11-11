package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
	"github.com/gofiber/fiber/v3"
)

// answerApp builds the board over a scripted rig: queue show answers with the
// given row, resolve and update are recorded and answer with the closed row.
func answerApp(t *testing.T, show string) (*fiber.App, *[]string) {
	t.Helper()
	var calls []string
	runner := decisionRunner(func(args []string) ([]byte, error) {
		line := strings.Join(args, " ")
		calls = append(calls, line)
		switch {
		case strings.HasPrefix(line, "queue show"):
			return []byte(show), nil
		case strings.HasPrefix(line, "queue resolve"):
			return []byte(`{"qitemId":"q1","destinationSession":"human@kernel","state":"done","pickup":{"state":"terminal"}}`), nil
		case strings.HasPrefix(line, "queue update"):
			return []byte(`{"qitemId":"q1","destinationSession":"human@kernel","state":"done","pickup":{"state":"terminal"}}`), nil
		}
		return nil, fmt.Errorf("unexpected call: %s", line)
	})
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "fleet.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	app, err := New(Config{Token: "test-token", Store: st, ConfigPath: "/tmp/fake-config.yaml", Rig: rig.NewAdapter(runner)})
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	return app, &calls
}

func postAnswer(t *testing.T, app *fiber.App, id string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/decisions/"+id+"/answer", strings.NewReader(`{"decision":"accept"}`))
	req.Header.Set(fiber.HeaderAuthorization, "Bearer test-token")
	req.Header.Set(fiber.HeaderContentType, "application/json")
	req.Header.Set("Idempotency-Key", "answer-"+id)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("POST answer: %v", err)
	}
	return resp
}

// TestAnswerResolvesABlockedRow: the leg-1 parked shape goes through the
// resolve verb and nothing else (#253).
func TestAnswerResolvesABlockedRow(t *testing.T) {
	show := `{"qitemId":"q1","sourceSession":"a","destinationSession":"bot@rig","state":"blocked","pickup":{"state":"parked"},"blockedOn":"human@kernel"}`
	app, calls := answerApp(t, show)
	resp := postAnswer(t, app, "q1")
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body = %s, want 200", resp.StatusCode, body)
	}
	if len(*calls) != 2 || !strings.HasPrefix((*calls)[1], "queue resolve") {
		t.Fatalf("calls = %v, want show then resolve", *calls)
	}
	for _, call := range *calls {
		if strings.HasPrefix(call, "queue update") {
			t.Fatalf("a blocked row was answered with an update: %v", *calls)
		}
	}
}

// TestAnswerClosesAPendingHumanRow: the shape act's raiser creates is pending
// and addressed to a human, and `queue resolve` refuses it — the board closes
// it with an update instead, so the one-click answer works for both shapes
// (#253).
func TestAnswerClosesAPendingHumanRow(t *testing.T) {
	show := `{"qitemId":"q1","sourceSession":"a","destinationSession":"human@kernel","state":"pending","pickup":{"state":"unclaimed"},"waiting":{"owner":"human@kernel","state":"pending"}}`
	app, calls := answerApp(t, show)
	resp := postAnswer(t, app, "q1")
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body = %s, want 200", resp.StatusCode, body)
	}
	var updated bool
	for _, call := range *calls {
		switch {
		case strings.HasPrefix(call, "queue resolve"):
			t.Fatalf("a pending row was resolved, which the verb refuses: %v", *calls)
		case strings.HasPrefix(call, "queue update"):
			updated = true
			if !strings.Contains(call, "--state done") || !strings.Contains(call, "--note decision: accept") {
				t.Fatalf("update argv = %q, want the answered closure with the decision", call)
			}
		}
	}
	if !updated {
		t.Fatalf("calls = %v, want the answered closure", *calls)
	}
}

// TestAnswerRefusesAPendingBotRow: a pending row addressed to a seat is work
// in flight, not a decision; the board must not close it (#253).
func TestAnswerRefusesAPendingBotRow(t *testing.T) {
	show := `{"qitemId":"q1","sourceSession":"a","destinationSession":"dev-build@rig","state":"pending","pickup":{"state":"unclaimed"}}`
	app, calls := answerApp(t, show)
	resp := postAnswer(t, app, "q1")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	for _, call := range *calls {
		if strings.HasPrefix(call, "queue resolve") || strings.HasPrefix(call, "queue update") {
			t.Fatalf("a pending bot row was written to: %v", *calls)
		}
	}
}

// TestAnswerAuditEventRecordsTheDecision keeps the decision.answer event on
// both answer paths.
func TestAnswerAuditEventRecordsTheDecision(t *testing.T) {
	for name, show := range map[string]string{
		"blocked": `{"qitemId":"q1","destinationSession":"bot@rig","state":"blocked","pickup":{"state":"parked"},"blockedOn":"human@kernel"}`,
		"pending": `{"qitemId":"q1","destinationSession":"human@kernel","state":"pending","pickup":{"state":"unclaimed"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			runner := decisionRunner(func(args []string) ([]byte, error) {
				line := strings.Join(args, " ")
				switch {
				case strings.HasPrefix(line, "queue show"):
					return []byte(show), nil
				case strings.HasPrefix(line, "queue resolve"), strings.HasPrefix(line, "queue update"):
					return []byte(`{"qitemId":"q1","destinationSession":"human@kernel","state":"done","pickup":{"state":"terminal"}}`), nil
				}
				return nil, fmt.Errorf("unexpected call: %s", line)
			})
			st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "fleet.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { st.Close() })
			app, err := New(Config{Token: "test-token", Store: st, ConfigPath: "/tmp/fake-config.yaml", Rig: rig.NewAdapter(runner)})
			if err != nil {
				t.Fatal(err)
			}
			if resp := postAnswer(t, app, "q1"); resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			events, err := st.RecentEvents(context.Background(), 10)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, e := range events {
				if e.Kind == "decision.answer" && e.Subject == "q1" {
					if e.Detail != "accept" {
						t.Fatalf("detail = %q, want the decision", e.Detail)
					}
					found = true
				}
			}
			if !found {
				t.Fatalf("events = %+v, want decision.answer", events)
			}
		})
	}
}
