package web

// The defect behind issue #143 was not one wrong predicate but a population
// split: `filterRows` and `openDecisions` each decided for themselves which
// rows are the operator's obligations, and drifted. The first fix covered one
// shape and the review caught two more.
//
// This test removes the possibility of a further split: it drives the SAME wire
// bytes through BOTH readers — the real /api/decisions handler and the real
// mirror -> progress.Build path — and asserts they agree. If either side grows
// its own rule again, one of these cases fails.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/config"
	"github.com/dinhtrung/fleet-console/internal/decide"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/mirror"
	"github.com/dinhtrung/fleet-console/internal/progress"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// row builds one `rig queue list` / `rig queue show` entry. `blockedOn` is
// omitted entirely when empty, which is what the list read does for a row that
// is not blocked on anything.
func wireRow(id, state, dest, blockedOn string) string {
	row := map[string]any{
		"qitemId": id, "sourceSession": "hermes-orchestrator",
		"destinationSession": dest, "state": state, "priority": "routine",
		"tsCreated": "2026-11-04T01:00:00.000Z",
		"summary":   "#12 " + id, "body": "body for " + id,
		"pickup": map[string]any{"state": "parked"},
	}
	if blockedOn != "" {
		row["blockedOn"] = blockedOn
	}
	raw, _ := json.Marshal(row)
	return string(raw)
}

// TestDecisionsAndQuietAgreeOnEveryShape is the acceptance guard for #143: the
// board's served population and the quiet predicate's counted population are
// the same set, whichever decision shape a row takes.
func TestDecisionsAndQuietAgreeOnEveryShape(t *testing.T) {
	cases := []struct {
		name      string
		rows      []string
		wantCount int
	}{
		{
			name:      "pending addressed to a person (adopt.go shape)",
			rows:      []string{wireRow("q-pending-human", "pending", "human@kernel", "")},
			wantCount: 1,
		},
		{
			name:      "blocked on a person, destination an agent seat",
			rows:      []string{wireRow("q-blocked-onhuman", "blocked", "operator-agent@kernel", "operator-human@kernel")},
			wantCount: 1,
		},
		{
			name:      "blocked, addressed to a person, no blocker",
			rows:      []string{wireRow("q-blocked-human", "blocked", "human@kernel", "")},
			wantCount: 1,
		},
		{
			name: "all three shapes at once",
			rows: []string{
				wireRow("q1", "pending", "human@kernel", ""),
				wireRow("q2", "blocked", "operator-agent@kernel", "operator-human@kernel"),
				wireRow("q3", "blocked", "human@kernel", ""),
			},
			wantCount: 3,
		},
		{
			name:      "a pending row addressed to an agent is not an obligation",
			rows:      []string{wireRow("q-bot", "pending", "dev-build-1@fleet-console", "")},
			wantCount: 0,
		},
		{
			name:      "a blocked row on a non-human gate is not an obligation",
			rows:      []string{wireRow("q-gate", "blocked", "dev-build-1@fleet-console", "external:someone-else")},
			wantCount: 0,
		},
		{
			name:      "a terminal row is not an obligation",
			rows:      []string{wireRow("q-done", "handed-off", "human@kernel", "")},
			wantCount: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			list := "[" + strings.Join(tc.rows, ",") + "]"
			// QueueShow returns the row as-is; build it from the same bytes.
			show := func(id string) (string, bool) {
				for _, r := range tc.rows {
					var row map[string]any
					if err := json.Unmarshal([]byte(r), &row); err != nil {
						t.Fatalf("decode fixture: %v", err)
					}
					if row["qitemId"] == id {
						return r, true
					}
				}
				return "", false
			}

			served := servedDecisionCount(t, list, show)
			counted, quiet := countedDecisionCount(t, list)

			if served != tc.wantCount {
				t.Fatalf("/api/decisions served %d, want %d", served, tc.wantCount)
			}
			if counted != served {
				t.Fatalf("DIVERGENCE: /api/decisions serves %d but OpenDecisions counts %d", served, counted)
			}
			if wantQuiet := tc.wantCount == 0; quiet != wantQuiet {
				t.Fatalf("Quiet() = %v with %d open decisions; want %v", quiet, counted, wantQuiet)
			}
		})
	}
}

// servedDecisionCount calls the real handler over the real adapter.
func servedDecisionCount(t *testing.T, list string, show func(string) (string, bool)) int {
	t.Helper()
	runner := decisionRunner(func(args []string) ([]byte, error) {
		line := strings.Join(args, " ")
		switch {
		case strings.HasPrefix(line, "queue list"):
			return []byte(list), nil
		case strings.HasPrefix(line, "queue show"):
			id := args[len(args)-3]
			row, ok := show(id)
			if !ok {
				return nil, fmt.Errorf("queue show %s: not in the fixture", id)
			}
			return []byte(row), nil
		}
		return nil, fmt.Errorf("unexpected call: %s", line)
	})
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "fleet.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer st.Close()
	app, err := New(Config{Token: "test-token", Store: st, ConfigPath: "/tmp/fake.yaml", Rig: rig.NewAdapter(runner)})
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/decisions", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	var out struct {
		Decisions []struct {
			ID string `json:"id"`
		} `json:"decisions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return len(out.Decisions)
}

// countedDecisionCount runs the same bytes through the real mirror (which is
// where filterRows lives) and the real Build, and returns the count plus Quiet.
func countedDecisionCount(t *testing.T, list string) (int, bool) {
	t.Helper()
	ctx := context.Background()
	runner := queueRowRunner(func(args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "queue" {
			return []byte(list), nil
		}
		return []byte("[]"), nil
	})
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fleet.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer st.Close()
	projectID, err := st.CreateProject(ctx, store.ProjectInput{
		Key: "fleet-console", Name: "fleet-console", ForgeKind: "github",
		ForgeProject: "dinhtrung/fleet-console", DefaultBranch: "main",
		Rigs: []string{"fleet-console"}, ConcurrencyCap: 2, Enabled: true,
	})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	m := &mirror.Mirror{Store: st, Rig: rig.NewAdapter(runner)}
	read, err := m.Tick(ctx, []mirror.Project{{
		ID: projectID, Key: "fleet-console",
		Forge: forge.NewFake(forge.KindGitHub), Rigs: []string{"fleet-console"},
	}})
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	project := store.ProjectRow{
		ID: projectID, Key: "fleet-console", Name: "fleet-console",
		ForgeKind: "github", ForgeProject: "dinhtrung/fleet-console", DefaultBranch: "main",
		Rigs: []string{"fleet-console"}, ConcurrencyCap: 2, Enabled: true,
	}
	eff := &config.Effective{Act: true, Projects: []config.Project{{
		Key: "fleet-console", Name: "fleet-console", Forge: "github",
		Repo: "dinhtrung/fleet-console", DefaultBranch: "main",
		Rigs: config.NamedRigs("fleet-console"), ConcurrencyCap: 2,
	}}}
	snap := progress.Build(read, nil, project, eff, progress.Roster{Plan: &decide.Plan{}})
	return snap.Counts.OpenDecisions, snap.Quiet()
}

// queueRowRunner answers the rig CLI for the counting side of the test.
type queueRowRunner func(...string) ([]byte, error)

func (f queueRowRunner) Run(_ context.Context, args ...string) ([]byte, error) { return f(args...) }
