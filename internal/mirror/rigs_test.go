package mirror

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/decide"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// rigRunner answers the three rig reads one tick makes: queue list, seats, and
// queue show for a row body.
type rigRunner struct {
	mu     sync.Mutex
	queues []byte
	seats  []byte
}

func (r *rigRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case len(args) >= 2 && args[0] == "queue" && args[1] == "list":
		return r.queues, nil
	case len(args) >= 2 && args[0] == "queue" && args[1] == "show":
		return []byte(`{"qitemId":"` + args[2] + `","destinationSession":"x@y","state":"pending","body":""}`), nil
	case len(args) >= 2 && args[0] == "ps" && args[1] == "--nodes":
		return r.seats, nil
	}
	return nil, fmt.Errorf("unexpected rig call: %s", strings.Join(args, " "))
}

func seatJSON(rigName, session, logicalID, lifecycle string) string {
	return fmt.Sprintf(`{"nodeId":"%s-%s","rigId":"%s","rigName":"%s","canonicalSessionName":"%s","logicalId":"%s","lifecycleState":"%s"}`,
		rigName, session, rigName, rigName, session, logicalID, lifecycle)
}

// TestTwoRigsDispatchInListedOrderAndStayIsolated: two projects, each with its
// own rigs, dispatch in the same tick — each seat order follows its project's
// listed rigs, rows stay inside the owning project, and an unknown rig warns
// once without touching the other project.
func TestTwoRigsDispatchInListedOrderAndStayIsolated(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	mnpID, err := st.CreateProject(ctx, store.ProjectInput{
		Key: "mnpserver2", Name: "mnpserver2", ForgeKind: "github", ForgeProject: "o/mnp",
		Rigs: []string{"mnp-fleet-claude", "ghost"}, ConcurrencyCap: 2, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create mnpserver2: %v", err)
	}
	tsimID, err := st.CreateProject(ctx, store.ProjectInput{
		Key: "tsim-v2", Name: "tsim-v2", ForgeKind: "github", ForgeProject: "o/tsim",
		Rigs: []string{"tsim-fleet-claude"}, ConcurrencyCap: 2, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create tsim-v2: %v", err)
	}
	runner := &rigRunner{
		queues: []byte(`[
			{"qitemId":"q1","destinationSession":"dev-build-1@mnp-fleet-claude","state":"pending","pickup":{"state":"unclaimed"}},
			{"qitemId":"q2","destinationSession":"dev-build-1@tsim-fleet-claude","state":"pending","pickup":{"state":"unclaimed"}}
		]`),
		seats: []byte(`[
			` + seatJSON("tsim-fleet-claude", "dev-build-1@tsim-fleet-claude", "dev.build-1", "running") + `,
			` + seatJSON("tsim-fleet-claude", "dev-build-2@tsim-fleet-claude", "dev.build-2", "running") + `,
			` + seatJSON("mnp-fleet-claude", "dev-build-2@mnp-fleet-claude", "dev.build-2", "running") + `,
			` + seatJSON("mnp-fleet-claude", "dev-build-1@mnp-fleet-claude", "dev.build-1", "running") + `
		]`),
	}
	m := &Mirror{Store: st, Rig: rig.NewAdapter(runner)}
	var logs []string
	m.Logf = func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) }

	mnpForge := forge.NewFake(forge.KindGitHub)
	mnpForge.ItemsValue = []forge.Item{{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Pool"}}
	tsimForge := forge.NewFake(forge.KindGitHub)
	tsimForge.ItemsValue = []forge.Item{{Number: 9, Kind: forge.KindTask, State: forge.StateOpen, Title: "Harness"}}

	result, err := m.Tick(ctx, []Project{
		{ID: mnpID, Key: "mnpserver2", Forge: mnpForge, Rigs: []string{"mnp-fleet-claude", "ghost"}},
		{ID: tsimID, Key: "tsim-v2", Forge: tsimForge, Rigs: []string{"tsim-fleet-claude"}},
	})
	if err != nil {
		t.Fatalf("tick: %v", err)
	}

	mnp, tsim := result.Projects["mnpserver2"], result.Projects["tsim-v2"]
	for _, row := range mnp.Rows {
		if !strings.HasSuffix(row.Destination, "@mnp-fleet-claude") {
			t.Fatalf("mnpserver2 row %s belongs to another rig", row.Destination)
		}
	}
	for _, row := range tsim.Rows {
		if !strings.HasSuffix(row.Destination, "@tsim-fleet-claude") {
			t.Fatalf("tsim-v2 row %s belongs to another rig", row.Destination)
		}
	}
	if len(mnp.Rows) != 1 || len(tsim.Rows) != 1 {
		t.Fatalf("rows mnp=%+v tsim=%+v, want one each", mnp.Rows, tsim.Rows)
	}
	if len(mnp.Seats) != 2 || mnp.Seats[0].Rig != "mnp-fleet-claude" || mnp.Seats[1].Rig != "mnp-fleet-claude" {
		t.Fatalf("mnpserver2 seats = %+v, want only its rig's seats", mnp.Seats)
	}
	if len(tsim.Seats) != 2 || tsim.Seats[0].Rig != "tsim-fleet-claude" || tsim.Seats[1].Rig != "tsim-fleet-claude" {
		t.Fatalf("tsim-v2 seats = %+v, want only tsim-fleet-claude", tsim.Seats)
	}
	if len(mnp.RigWarnings) != 1 || mnp.RigWarnings[0] != "ghost" {
		t.Fatalf("mnpserver2 warnings = %v, want [ghost]", mnp.RigWarnings)
	}
	if len(tsim.RigWarnings) != 0 {
		t.Fatalf("tsim-v2 warnings = %v, want none", tsim.RigWarnings)
	}

	// One named warning for the missing rig, and nothing about the healthy one.
	m.noteProjects(map[string]string{}, result)
	var rigLogs []string
	for _, line := range logs {
		if strings.Contains(line, "rig") {
			rigLogs = append(rigLogs, line)
		}
	}
	if len(rigLogs) != 1 || !strings.Contains(rigLogs[0], "ghost has no running seats") {
		t.Fatalf("logs = %v, want one named warning for ghost", logs)
	}

	// Each project plans a dispatch from its own builders, in listed order.
	mnpRow, _ := st.ProjectByKey(ctx, "mnpserver2")
	mnpPlan := decide.PlanTick(BuildSnapshot(*mnpRow, mnp, nil, nil), result.At)
	if len(mnpPlan.Intents) != 1 || mnpPlan.Intents[0].Seat != "dev-build-2@mnp-fleet-claude" {
		t.Fatalf("mnpserver2 plan = %+v, want dispatch to its first rig", mnpPlan.Intents)
	}
	tsimRow, _ := st.ProjectByKey(ctx, "tsim-v2")
	tsimPlan := decide.PlanTick(BuildSnapshot(*tsimRow, tsim, nil, nil), result.At)
	if len(tsimPlan.Intents) != 1 || tsimPlan.Intents[0].Seat != "dev-build-2@tsim-fleet-claude" {
		t.Fatalf("tsim-v2 plan = %+v, want dispatch to tsim-fleet-claude", tsimPlan.Intents)
	}
}

// TestSeatsFollowRigOrderUnderOneCap: two rigs, one free builder each, fill in
// the order the file lists them; concurrency_cap still bounds the project.
func TestSeatsFollowRigOrderUnderOneCap(t *testing.T) {
	at := time.Now()
	projectRow := store.ProjectRow{ID: 1, Key: "app", ForgeKind: "github", Enabled: true, ConcurrencyCap: 2}
	reads := ProjectResult{
		Items: []forge.Item{
			{Number: 1, Kind: forge.KindTask, State: forge.StateOpen, Title: "one"},
			{Number: 2, Kind: forge.KindTask, State: forge.StateOpen, Title: "two"},
		},
		Seats: []rig.Seat{
			{Rig: "rig-b", Session: "dev-build-1@rig-b", LogicalID: "dev.build-1", Lifecycle: "running"},
			{Rig: "rig-a", Session: "dev-build-1@rig-a", LogicalID: "dev.build-1", Lifecycle: "running"},
		},
	}
	ordered := ProjectResult{Items: reads.Items, Seats: filterSeats(reads.Seats, []string{"rig-a", "rig-b"})}
	plan := decide.PlanTick(BuildSnapshot(projectRow, ordered, nil, nil), at)
	if len(plan.Intents) != 2 {
		t.Fatalf("intents = %+v, want two", plan.Intents)
	}
	if plan.Intents[0].Seat != "dev-build-1@rig-a" || plan.Intents[1].Seat != "dev-build-1@rig-b" {
		t.Fatalf("seats = %s, %s; want the listed rig order", plan.Intents[0].Seat, plan.Intents[1].Seat)
	}
	projectRow.ConcurrencyCap = 1
	plan = decide.PlanTick(BuildSnapshot(projectRow, ordered, nil, nil), at)
	if len(plan.Intents) != 1 || plan.Intents[0].Seat != "dev-build-1@rig-a" {
		t.Fatalf("intents with cap 1 = %+v, want one dispatch to the first rig", plan.Intents)
	}
}

// TestRigFilteringPureHelpers pins the row filter on one rig's destinations.
func TestRigFilteringPureHelpers(t *testing.T) {
	rows := []rig.QueueRow{
		{ID: "a", Destination: "dev-build-1@rig-a"},
		{ID: "b", Destination: "dev-build-1@rig-b"},
		{ID: "c", Destination: "dev-review@rig-c"},
		{ID: "d", Destination: "plain"},
	}
	got := filterRows(rows, []string{"rig-a", "rig-b"})
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
		t.Fatalf("rows = %+v, want rig-a and rig-b only", got)
	}
	if len(missingRigs(nil, nil)) != 0 {
		t.Fatal("no configured rigs must not warn")
	}
}
