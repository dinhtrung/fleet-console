package progress

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/dinhtrung/fleet-console/internal/config"
	"github.com/dinhtrung/fleet-console/internal/decide"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/git"
	"github.com/dinhtrung/fleet-console/internal/mirror"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

var update = flag.Bool("update", false, "rewrite the golden report")

var bucketEntry = regexp.MustCompile(`(?m)^  #\d+ `)

var fixedNow = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

func intPtr(value int) *int { return &value }

func testProject() store.ProjectRow {
	return store.ProjectRow{
		ID: 1, Key: "fleet-console", Name: "fleet-console",
		ForgeKind: "github", ForgeProject: "dinhtrung/fleet-console",
		DefaultBranch: "main", Rig: "fleet-console", Rigs: []string{"fleet-console"},
		ConcurrencyCap: 2, Enabled: true,
	}
}

func testConfig() *config.Effective {
	project := config.Project{
		Key: "fleet-console", Name: "fleet-console", Forge: "github",
		Repo: "dinhtrung/fleet-console", DefaultBranch: "main",
		Rigs: []string{"fleet-console"}, ConcurrencyCap: 2,
	}
	project.Paths.Main = "/home/dinhtrung/develop/fleet-console"
	project.Paths.Worktrees = "/home/dinhtrung/develop"
	return &config.Effective{Act: true, Projects: []config.Project{project}}
}

func testSeats() []rig.Seat {
	return []rig.Seat{
		{Session: "dev-build-1@fleet-console", LogicalID: "dev.build-1", Rig: "fleet-console",
			Lifecycle: "running", LastActivity: "2026-10-30T11:59:00Z"},
		{Session: "dev-build-2@fleet-console", LogicalID: "dev.build-2", Rig: "fleet-console",
			Lifecycle: "running", LastActivity: "2026-10-30T11:58:00Z"},
		{Session: "dev-review@fleet-console", LogicalID: "dev.review", Rig: "fleet-console",
			Lifecycle: "running", LastActivity: "2026-10-30T11:57:00Z"},
	}
}

// baseRead is the design §2 shape: two items in flight, one epic, one idle
// reviewer.
func baseRead() mirror.TickResult {
	items := []forge.Item{
		{Number: 62, Kind: forge.KindTask, State: forge.StateOpen,
			Title: "backend: create a project from the board", Labels: []string{"status::ongoing"}, Parent: intPtr(64)},
		{Number: 63, Kind: forge.KindTask, State: forge.StateOpen,
			Title: "web: Projects surface and New project dialog", Labels: []string{"status::ongoing"}},
		{Number: 64, Kind: forge.KindEpic, State: forge.StateOpen,
			Title: "create a project from the board"},
		{Number: 65, Kind: forge.KindTask, State: forge.StateClosed,
			Title: "backend: the project row is written to config.yaml", Parent: intPtr(64)},
	}
	branches := []git.Branch{
		{Name: "feature/62-p2-create-a-project", Item: 62},
		{Name: "feature/63-p2-projects-surface", Item: 63},
	}
	return mirror.TickResult{
		At: fixedNow,
		Projects: map[string]mirror.ProjectResult{
			"fleet-console": {
				Items: items, Branches: branches, Seats: testSeats(),
			},
		},
	}
}

func baseRows() []store.DispatchRow {
	return []store.DispatchRow{
		{ID: 1, ProjectID: 1, ItemNumber: 62, Seat: "dev-build-1@fleet-console",
			Branch: "feature/62-p2-create-a-project", CreatedAt: "2026-10-30T11:33:00Z", State: "active"},
		{ID: 2, ProjectID: 1, ItemNumber: 63, Seat: "dev-build-2@fleet-console",
			Branch: "feature/63-p2-projects-surface", CreatedAt: "2026-10-30T11:19:00Z", State: "active"},
	}
}

func buildBase() Snapshot {
	return Build(baseRead(), baseRows(), testProject(), testConfig(), Roster{Plan: &decide.Plan{}})
}

func TestBuildProjectBlock(t *testing.T) {
	snapshot := buildBase()
	block := snapshot.Project
	checks := map[string][2]string{
		"key":      {block.Key, "fleet-console"},
		"name":     {block.Name, "fleet-console"},
		"forge":    {block.Forge, "github"},
		"repo":     {block.Repo, "dinhtrung/fleet-console"},
		"url":      {block.RepoURL, "https://github.com/dinhtrung/fleet-console"},
		"branch":   {block.DefaultBranch, "main"},
		"cap":      {string(rune('0' + block.Cap)), "2"},
		"onhold":   {yesNo(block.OnHold), "no"},
		"act":      {onOff(block.Act), "on"},
		"main":     {block.MainPath, "/home/dinhtrung/develop/fleet-console"},
		"worktree": {block.WorktreePath, "/home/dinhtrung/develop"},
	}
	for field, values := range checks {
		if values[0] != values[1] {
			t.Errorf("%s = %q, want %q", field, values[0], values[1])
		}
	}
	if len(block.Rigs) != 1 || block.Rigs[0] != "fleet-console" {
		t.Fatalf("rigs = %v, want fleet-console", block.Rigs)
	}
}

func TestBuildSeats(t *testing.T) {
	snapshot := buildBase()
	if len(snapshot.Rig.Seats) != 3 {
		t.Fatalf("seats = %d, want one line per roster seat", len(snapshot.Rig.Seats))
	}
	for index, want := range []string{"dev.build-1", "dev.build-2", "dev.review"} {
		if snapshot.Rig.Seats[index].LogicalID != want {
			t.Fatalf("seat %d = %s, want %s (roster order)", index, snapshot.Rig.Seats[index].LogicalID, want)
		}
	}
	if snapshot.Rig.Busy != 2 || snapshot.Rig.Total != 3 {
		t.Fatalf("busy = %d/%d, want 2/3", snapshot.Rig.Busy, snapshot.Rig.Total)
	}
	if snapshot.Rig.Seats[0].Item != 62 || snapshot.Rig.Seats[0].Age != "27m" {
		t.Fatalf("builder line = %+v, want #62 held 27m", snapshot.Rig.Seats[0])
	}
	if snapshot.Rig.Seats[2].IdleWhy != "idle — nothing to review" {
		t.Fatalf("reviewer = %q, want nothing to review", snapshot.Rig.Seats[2].IdleWhy)
	}
}

func TestRenderHasEveryLabel(t *testing.T) {
	body := Render(buildBase(), fixedNow)
	for _, want := range []string{
		"PROJECT", "key: fleet-console", "name: fleet-console", "forge: github",
		"repo: dinhtrung/fleet-console", "url: https://github.com/dinhtrung/fleet-console",
		"branch: main", "cap: 2", "onhold: no", "act: on",
		"paths: main /home/dinhtrung/develop/fleet-console",
		"worktrees /home/dinhtrung/develop",
		"RIG fleet-console · busy 2/3",
		"dev-build-1", "dev-build-2", "dev-review",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body is missing %q\n%s", want, body)
		}
	}
}

func TestGoldenReport(t *testing.T) {
	body := Render(buildBase(), fixedNow)
	path := filepath.Join("testdata", "report.golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if body != string(want) {
		t.Fatalf("report differs from golden\n--- got ---\n%s\n--- want ---\n%s", body, want)
	}
}

func TestBucketCapAndEmptyBuckets(t *testing.T) {
	snapshot := Snapshot{
		Project:  buildBase().Project,
		Rig:      buildBase().Rig,
		InFlight: items(9),
		Counts:   Counts{InFlight: 9},
	}
	body := Render(snapshot, fixedNow)
	if !strings.Contains(body, "▶ in progress (9)") {
		t.Fatalf("header lost the true count\n%s", body)
	}
	if !strings.Contains(body, "  +4 more") {
		t.Fatalf("missing +4 more\n%s", body)
	}
	if got := len(bucketEntry.FindAllString(body, -1)); got != entryLimit {
		t.Fatalf("rendered %d entries, want %d\n%s", got, entryLimit, body)
	}
	for _, want := range []string{"⏳ pending (0)", "⏸ on hold (0)"} {
		if !strings.Contains(body, want) {
			t.Fatalf("empty bucket %q is missing\n%s", want, body)
		}
	}
}

func TestLengthCap(t *testing.T) {
	base := buildBase()
	idle := base.Rig
	idle.Idle = true
	idle.Busy = 0
	idle.IdleWhy = "idle — nothing dispatchable: #1 blocked: #2"
	for _, seat := range idle.Seats {
		seat.IdleWhy = "idle — waiting for a dispatch"
	}
	snapshot := Snapshot{
		Project:  base.Project,
		Rig:      idle,
		InFlight: items(200),
		Pending:  items(200),
		Counts:   Counts{InFlight: 200, Pending: 200},
	}
	body := Render(snapshot, fixedNow)
	if got := utf8.RuneCountInString(body); got > maxBody {
		t.Fatalf("body = %d characters, want <= %d", got, maxBody)
	}
	for _, want := range []string{
		"PROJECT", "key: fleet-console", "worktrees /home/dinhtrung/develop",
		"RIG fleet-console · idle", "why: idle — nothing dispatchable: #1 blocked: #2",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("truncation lost %q\n%s", want, body)
		}
	}
}

func items(count int) []Item {
	out := make([]Item, 0, count)
	for number := 1; number <= count; number++ {
		out = append(out, Item{
			Number: number, Title: strings.Repeat("a long issue title ", 8),
			Seat: "dev-build-1@fleet-console", Branch: "feature/1-a-long-branch-name", Since: fixedNow.Add(-time.Hour),
		})
	}
	return out
}

// TestRosterInMessage pairs the two roster outcomes: a roster answers and the
// block carries busy N/M with every seat, or it does not and the block names
// the reason. Neither path is an error (the notifier's Send is task 2.x).
func TestRosterInMessage(t *testing.T) {
	withRoster := Render(buildBase(), fixedNow)
	if !strings.Contains(withRoster, "RIG fleet-console · busy 2/3") {
		t.Fatalf("a roster that answered lost busy N/M\n%s", withRoster)
	}
	if strings.Contains(withRoster, "roster unavailable") {
		t.Fatalf("a roster that answered reports itself unavailable\n%s", withRoster)
	}

	read := baseRead()
	projectRead := read.Projects["fleet-console"]
	projectRead.Seats = nil
	read.Projects["fleet-console"] = projectRead
	snapshot := Build(read, nil, testProject(), testConfig(), Roster{
		Err:  errors.New("rig ps --nodes: exit 1"),
		Plan: &decide.Plan{},
	})
	body := Render(snapshot, fixedNow)
	if !strings.Contains(body, "roster unavailable: rig ps --nodes: exit 1") {
		t.Fatalf("roster error is not a line\n%s", body)
	}
	if snapshot.Rig.RosterErr != "rig ps --nodes: exit 1" {
		t.Fatalf("roster error = %q", snapshot.Rig.RosterErr)
	}
}

func TestIdleReasons(t *testing.T) {
	openItem := forge.Item{Number: 70, Kind: forge.KindTask, State: forge.StateOpen, Title: "waiting work"}
	cases := []struct {
		name   string
		mutate func(*mirror.TickResult, *store.ProjectRow, *config.Effective, *Roster, *[]store.DispatchRow)
		want   string
	}{
		{
			name: "1 act off",
			mutate: func(_ *mirror.TickResult, _ *store.ProjectRow, eff *config.Effective, _ *Roster, _ *[]store.DispatchRow) {
				eff.Act = false
			},
			want: "idle — act is off: the board mirrors, nothing is dispatched",
		},
		{
			name: "2 project on hold",
			mutate: func(_ *mirror.TickResult, project *store.ProjectRow, _ *config.Effective, _ *Roster, _ *[]store.DispatchRow) {
				project.OnHold = true
			},
			want: "idle — project on hold",
		},
		{
			name: "3 quota pause",
			mutate: func(_ *mirror.TickResult, _ *store.ProjectRow, _ *config.Effective, roster *Roster, _ *[]store.DispatchRow) {
				roster.QuotaPause = "qitem-quota-1"
			},
			want: "idle — paused by a quota decision: qitem-quota-1",
		},
		{
			name: "4 open decisions",
			mutate: func(read *mirror.TickResult, _ *store.ProjectRow, _ *config.Effective, _ *Roster, _ *[]store.DispatchRow) {
				projectRead := read.Projects["fleet-console"]
				projectRead.Rows = []rig.QueueRow{{
					ID: "qitem-1", State: "pending", Destination: "human@kernel",
					Summary: "#63 accept the parked change request?", Body: "fleet-console #63: no verdict",
				}}
				read.Projects["fleet-console"] = projectRead
			},
			want: "idle — waiting on you: 1 open decisions (#63)",
		},
		{
			name: "5 no open work",
			mutate: func(read *mirror.TickResult, _ *store.ProjectRow, _ *config.Effective, _ *Roster, _ *[]store.DispatchRow) {
				projectRead := read.Projects["fleet-console"]
				projectRead.Items = []forge.Item{
					{Number: 64, Kind: forge.KindEpic, State: forge.StateClosed, Title: "done epic"},
				}
				read.Projects["fleet-console"] = projectRead
			},
			want: "idle — no open work: every issue is closed",
		},
		{
			name: "6 no seat running",
			mutate: func(read *mirror.TickResult, _ *store.ProjectRow, _ *config.Effective, _ *Roster, _ *[]store.DispatchRow) {
				projectRead := read.Projects["fleet-console"]
				for index := range projectRead.Seats {
					projectRead.Seats[index].Lifecycle = "down"
				}
				read.Projects["fleet-console"] = projectRead
			},
			want: "idle — no seat running: 3 seats reported down",
		},
		{
			name: "7 roster unavailable",
			mutate: func(_ *mirror.TickResult, _ *store.ProjectRow, _ *config.Effective, roster *Roster, _ *[]store.DispatchRow) {
				roster.Seats = []rig.Seat{}
				roster.Err = errors.New("rig ps: exit 1")
			},
			want: "idle — rig roster unavailable: rig ps: exit 1",
		},
		{
			name: "8 plan skip text",
			mutate: func(_ *mirror.TickResult, _ *store.ProjectRow, _ *config.Effective, roster *Roster, _ *[]store.DispatchRow) {
				roster.Plan = &decide.Plan{Skips: []decide.Skip{{Number: 70, Reason: decide.ReasonBlocked, Detail: "#69"}}}
			},
			want: "idle — nothing dispatchable: #70 blocked: #69",
		},
		{
			name: "8 plan skip text keeps the plan's detail",
			mutate: func(_ *mirror.TickResult, _ *store.ProjectRow, _ *config.Effective, roster *Roster, _ *[]store.DispatchRow) {
				roster.Plan = &decide.Plan{Skips: []decide.Skip{{Number: 71, Reason: decide.ReasonParentOnHold, Detail: "parent #64 carries onhold"}}}
			},
			want: "idle — nothing dispatchable: #71 parent_onhold: parent #64 carries onhold",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			read := mirror.TickResult{At: fixedNow, Projects: map[string]mirror.ProjectResult{
				"fleet-console": {Items: []forge.Item{openItem}, Seats: testSeats()},
			}}
			project := testProject()
			eff := testConfig()
			roster := Roster{Plan: &decide.Plan{}}
			var rows []store.DispatchRow
			tc.mutate(&read, &project, eff, &roster, &rows)
			snapshot := Build(read, rows, project, eff, roster)
			if !snapshot.Rig.Idle {
				t.Fatalf("rig is not idle: why=%q", snapshot.Rig.IdleWhy)
			}
			if snapshot.Rig.IdleWhy != tc.want {
				t.Fatalf("why = %q, want %q", snapshot.Rig.IdleWhy, tc.want)
			}
			if body := Render(snapshot, fixedNow); !strings.Contains(body, "  why: "+tc.want) {
				t.Fatalf("rendered body is missing the why line\n%s", body)
			}
		})
	}
}

func TestNoFreeBuilderIsNeverIdle(t *testing.T) {
	read := mirror.TickResult{At: fixedNow, Projects: map[string]mirror.ProjectResult{
		"fleet-console": {
			Items: []forge.Item{{Number: 70, Kind: forge.KindTask, State: forge.StateOpen, Title: "queued work"}},
			Seats: testSeats(),
		},
	}}
	roster := Roster{Plan: &decide.Plan{Skips: []decide.Skip{
		{Number: 70, Reason: decide.ReasonNoFreeBuilder},
	}}}
	snapshot := Build(read, nil, testProject(), testConfig(), roster)
	if snapshot.Rig.Idle {
		t.Fatalf("no_free_builder rendered as idle: %q", snapshot.Rig.IdleWhy)
	}
	body := Render(snapshot, fixedNow)
	if strings.Contains(body, " · idle") || strings.Contains(body, "  why:") {
		t.Fatalf("no_free_builder produced an idle report\n%s", body)
	}
}

func TestIdleSeatReasons(t *testing.T) {
	cases := []struct {
		name   string
		item   int
		seats  func() []rig.Seat
		items  []forge.Item
		reason string
	}{
		{
			name: "lifecycle not running",
			seats: func() []rig.Seat {
				return []rig.Seat{{Session: "dev-build-1@rig", LogicalID: "dev.build-1", Rig: "rig", Lifecycle: "down"}}
			},
			items:  []forge.Item{{Number: 70, Kind: forge.KindTask, State: forge.StateOpen, Title: "work"}},
			reason: "idle — seat down",
		},
		{
			name: "dead reviewer names the lifecycle, not the review",
			seats: func() []rig.Seat {
				return []rig.Seat{{Session: "dev-review@rig", LogicalID: "dev.review", Rig: "rig", Lifecycle: "down"}}
			},
			items:  []forge.Item{{Number: 70, Kind: forge.KindTask, State: forge.StateOpen, Title: "work"}},
			reason: "idle — seat down",
		},
		{
			name: "reviewer with nothing to review",
			seats: func() []rig.Seat {
				return []rig.Seat{{Session: "dev-review@rig", LogicalID: "dev.review", Rig: "rig", Lifecycle: "running"}}
			},
			items:  []forge.Item{{Number: 70, Kind: forge.KindTask, State: forge.StateOpen, Title: "work"}},
			reason: "idle — nothing to review",
		},
		{
			name: "builder with nothing pending",
			seats: func() []rig.Seat {
				return []rig.Seat{{Session: "dev-build-1@rig", LogicalID: "dev.build-1", Rig: "rig", Lifecycle: "running"}}
			},
			reason: "idle — nothing pending",
		},
		{
			name: "builder waiting for a dispatch",
			seats: func() []rig.Seat {
				return []rig.Seat{{Session: "dev-build-1@rig", LogicalID: "dev.build-1", Rig: "rig", Lifecycle: "running"}}
			},
			items:  []forge.Item{{Number: 70, Kind: forge.KindTask, State: forge.StateOpen, Title: "work"}},
			reason: "idle — waiting for a dispatch",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			project := testProject()
			project.Rigs = []string{"rig"}
			eff := testConfig()
			eff.Projects[0].Rigs = []string{"rig"}
			read := mirror.TickResult{At: fixedNow, Projects: map[string]mirror.ProjectResult{
				"fleet-console": {Items: tc.items, Seats: tc.seats()},
			}}
			snapshot := Build(read, nil, project, eff, Roster{Plan: &decide.Plan{}})
			if len(snapshot.Rig.Seats) != 1 {
				t.Fatalf("seats = %d, want 1", len(snapshot.Rig.Seats))
			}
			if got := snapshot.Rig.Seats[0].IdleWhy; got != tc.reason {
				t.Fatalf("seat reason = %q, want %q", got, tc.reason)
			}
		})
	}
}

func TestRepoURLRules(t *testing.T) {
	cases := []struct {
		name       string
		forge      string
		repo       string
		configured string
		want       string
	}{
		{name: "configured verbatim", forge: "gitlab", repo: "team/app", configured: "https://gitlab.horisen.org/team/app", want: "https://gitlab.horisen.org/team/app"},
		{name: "github derived", forge: "github", repo: "dinhtrung/fleet-console", want: "https://github.com/dinhtrung/fleet-console"},
		{name: "gitlab derived", forge: "gitlab", repo: "team/app", want: "https://gitlab.com/team/app"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			project := testProject()
			project.ForgeKind = tc.forge
			project.ForgeProject = tc.repo
			eff := testConfig()
			eff.Projects[0].Forge = tc.forge
			eff.Projects[0].Repo = tc.repo
			eff.Projects[0].RepoURL = tc.configured
			snapshot := Build(mirror.TickResult{At: fixedNow}, nil, project, eff, Roster{})
			if snapshot.Project.RepoURL != tc.want {
				t.Fatalf("url = %q, want %q", snapshot.Project.RepoURL, tc.want)
			}
		})
	}
}
