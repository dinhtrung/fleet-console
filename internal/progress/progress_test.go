package progress

import (
	"context"
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

// quietRead is a board with nothing on it: every issue closed, no epic open,
// nothing in flight, pending or on hold, and no decision waiting.
func quietRead() mirror.TickResult {
	return mirror.TickResult{
		At: fixedNow,
		Projects: map[string]mirror.ProjectResult{
			"fleet-console": {
				Items: []forge.Item{
					{Number: 64, Kind: forge.KindEpic, State: forge.StateClosed, Title: "done epic"},
					{Number: 65, Kind: forge.KindTask, State: forge.StateClosed, Title: "done task", Parent: intPtr(64)},
				},
				Seats: testSeats(),
			},
		},
	}
}

func buildQuiet() Snapshot {
	return Build(quietRead(), nil, testProject(), testConfig(), Roster{Plan: &decide.Plan{}})
}

// TestQuietWhenEverythingIsZero is the base case: an empty board is quiet.
func TestQuietWhenEverythingIsZero(t *testing.T) {
	snapshot := buildQuiet()
	if !snapshot.Quiet() {
		t.Fatalf("Quiet() = false on an empty board; counts = %+v", snapshot.Counts)
	}
	if snapshot.Counts != (Counts{}) {
		t.Fatalf("counts = %+v, want all zero", snapshot.Counts)
	}
}

// TestNotQuietForEachNonZeroCount is five separate cases, not one row with all
// five set: a predicate that forgets one bucket must fail on that bucket alone.
func TestNotQuietForEachNonZeroCount(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*mirror.TickResult, *[]store.DispatchRow)
	}{
		{
			name: "one open epic",
			mutate: func(read *mirror.TickResult, _ *[]store.DispatchRow) {
				// openEpics counts an epic only when it has an open child
				// (progress.go:484). The child here is itself an epic: openBuckets
				// skips epics, so this leaves Pending, OnHold and InFlight at zero
				// and makes the Epics term alone decide the predicate.
				projectRead := read.Projects["fleet-console"]
				projectRead.Items = append(projectRead.Items, forge.Item{
					Number: 70, Kind: forge.KindEpic, State: forge.StateOpen, Title: "open epic",
				}, forge.Item{
					Number: 74, Kind: forge.KindEpic, State: forge.StateOpen,
					Title: "nested open epic", Parent: intPtr(70),
				})
				read.Projects["fleet-console"] = projectRead
			},
		},
		{
			name: "one in flight",
			mutate: func(read *mirror.TickResult, rows *[]store.DispatchRow) {
				projectRead := read.Projects["fleet-console"]
				projectRead.Items = append(projectRead.Items, forge.Item{
					Number: 71, Kind: forge.KindTask, State: forge.StateOpen, Title: "in flight",
				})
				read.Projects["fleet-console"] = projectRead
				*rows = []store.DispatchRow{{
					ID: 1, ProjectID: 1, ItemNumber: 71, Seat: "dev-build-1@fleet-console",
					Branch: "feature/71", CreatedAt: "2026-10-30T11:33:00Z", State: "active",
				}}
			},
		},
		{
			name: "one pending",
			mutate: func(read *mirror.TickResult, _ *[]store.DispatchRow) {
				projectRead := read.Projects["fleet-console"]
				projectRead.Items = append(projectRead.Items, forge.Item{
					Number: 72, Kind: forge.KindTask, State: forge.StateOpen, Title: "pending work",
				})
				read.Projects["fleet-console"] = projectRead
			},
		},
		{
			name: "one on hold",
			mutate: func(read *mirror.TickResult, _ *[]store.DispatchRow) {
				projectRead := read.Projects["fleet-console"]
				projectRead.Items = append(projectRead.Items, forge.Item{
					Number: 73, Kind: forge.KindTask, State: forge.StateOpen, Title: "held work",
					Labels: []string{"onhold"},
				})
				read.Projects["fleet-console"] = projectRead
			},
		},
		{
			name: "one open decision",
			mutate: func(read *mirror.TickResult, _ *[]store.DispatchRow) {
				projectRead := read.Projects["fleet-console"]
				projectRead.Rows = []rig.QueueRow{{
					ID: "qitem-1", State: "pending", Destination: "human@kernel",
					Summary: "#63 accept the parked change request?", Body: "fleet-console #63: no verdict",
				}}
				read.Projects["fleet-console"] = projectRead
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			read := quietRead()
			var rows []store.DispatchRow
			tc.mutate(&read, &rows)
			snapshot := Build(read, rows, testProject(), testConfig(), Roster{Plan: &decide.Plan{}})
			if snapshot.Quiet() {
				t.Fatalf("Quiet() = true with %s; counts = %+v", tc.name, snapshot.Counts)
			}
		})
	}
}

// TestNotQuietWhenAListIsTruncated pins the reason Quiet reads Counts and not
// the slices. Truncation happens in Render (entryLimit, render.go:15), not in
// Build: the snapshot keeps the full slice and the count agrees with it. The
// message prints only five pending lines; the board still has forty.
func TestNotQuietWhenAListIsTruncated(t *testing.T) {
	read := quietRead()
	projectRead := read.Projects["fleet-console"]
	for i := range 40 {
		projectRead.Items = append(projectRead.Items, forge.Item{
			Number: 100 + i, Kind: forge.KindTask, State: forge.StateOpen,
			Title: "pending work", Labels: []string{"status::ongoing"},
		})
	}
	read.Projects["fleet-console"] = projectRead

	snapshot := Build(read, nil, testProject(), testConfig(), Roster{Plan: &decide.Plan{}})
	if snapshot.Counts.Pending != 40 {
		t.Fatalf("Counts.Pending = %d, want 40", snapshot.Counts.Pending)
	}
	body := Render(snapshot, fixedNow)
	// The pending bucket prints entryLimit lines and then "+35 more".
	if printed := strings.Count(body, "  #1"); printed != 5 {
		t.Fatalf("pending lines printed = %d, want 5 (entryLimit)", printed)
	}
	if !strings.Contains(body, "+35 more") {
		t.Fatalf("body has no truncation marker; the case would not prove anything:\n%s", body)
	}
	if snapshot.Quiet() {
		t.Fatal("Quiet() = true although the count is 40 and only the rendered list is truncated")
	}
}

// TestProductionShapedDecisionRowsAreNotQuiet pins issue #143 end to end. It
// drives the production path — real `rig queue list` wire bytes through
// rig.ParseQueueList, then the real mirror through filterRows — rather than
// handing Build a hand-made row that skipped both. That shortcut is what let
// the original test pass while production stayed broken.
//
// The shape is the one this box actually produces: a `blocked` row whose
// destination is an agent seat and whose blocker is `operator-human@kernel`.
// Counted by destination alone it is invisible, and the board served two such
// decisions while the snapshot read quiet.
func TestProductionShapedDecisionRowsAreNotQuiet(t *testing.T) {
	// Exactly the fields `rig queue list -A -a --json` emits for a parked row,
	// including the required pickup block.
	const wire = `[
	  {"pickup":{"state":"parked","evidence":"awaits a person"},
	   "qitemId":"qitem-20261104014517-6e808968eec861e8",
	   "tsCreated":"2026-11-04T01:45:17.000Z","sourceSession":"hermes-orchestrator",
	   "destinationSession":"operator-agent@kernel","state":"blocked","priority":"routine",
	   "blockedOn":"operator-human@kernel",
	   "summary":"Row S3 needs a builder","body":"#38 needs a decision"},
	  {"pickup":{"state":"parked","evidence":"awaits a person"},
	   "qitemId":"qitem-20261104012716-36bbd84f",
	   "tsCreated":"2026-11-04T01:27:16.000Z","sourceSession":"hermes-orchestrator",
	   "destinationSession":"advisor-lead@kernel","state":"blocked","priority":"routine",
	   "blockedOn":"operator-human@kernel",
	   "summary":"Release gate","body":"do not tag android-v1.2.0"}
	]`

	rows, err := rig.ParseQueueList([]byte(wire), nil)
	if err != nil {
		t.Fatalf("ParseQueueList: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("decoded rows = %d, want 2", len(rows))
	}
	for _, row := range rows {
		if !rig.HumanDecision(row.BlockedOn) {
			t.Fatalf("row %s: BlockedOn = %q, not decoded as a human blocker", row.ID, row.BlockedOn)
		}
	}

	// The rows are addressed to another rig, so the mirror's filter must keep
	// them: a person's obligation does not belong to the project's rig.
	runner := queueRunner(func(_ context.Context, args ...string) ([]byte, error) {
		switch {
		case len(args) > 0 && args[0] == "queue":
			return []byte(wire), nil
		case len(args) > 0 && args[0] == "ps":
			return []byte("[]"), nil
		default:
			return []byte("[]"), nil
		}
	})
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "fleet.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	projectID, err := st.CreateProject(context.Background(), store.ProjectInput{
		Key: "fleet-console", Name: "fleet-console", ForgeKind: "github",
		ForgeProject: "dinhtrung/fleet-console", DefaultBranch: "main",
		Rigs: []string{"fleet-console"}, ConcurrencyCap: 2, Enabled: true,
	})
	if err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	m := &mirror.Mirror{Store: st, Rig: rig.NewAdapter(runner)}
	ctx := context.Background()
	read, err := m.Tick(ctx, []mirror.Project{{
		ID: projectID, Key: "fleet-console",
		Forge: forge.NewFake(forge.KindGitHub), Rigs: []string{"fleet-console"},
	}})
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	kept := read.Projects["fleet-console"].Rows
	if len(kept) != 2 {
		t.Fatalf("filterRows kept %d rows, want 2: an @kernel decision row was dropped", len(kept))
	}

	snapshot := Build(read, nil, testProject(), testConfig(), Roster{Plan: &decide.Plan{}})
	if snapshot.Counts.OpenDecisions != 2 {
		t.Fatalf("Counts.OpenDecisions = %d, want 2", snapshot.Counts.OpenDecisions)
	}
	if snapshot.Quiet() {
		t.Fatalf("Quiet() = true with 2 decisions waiting; counts = %+v", snapshot.Counts)
	}
}

// queueRunner answers the rig CLI for this test: the queue read returns the
// recorded wire bytes, everything else is empty.
type queueRunner func(context.Context, ...string) ([]byte, error)

func (f queueRunner) Run(ctx context.Context, args ...string) ([]byte, error) { return f(ctx, args...) }
