package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/store"
	"github.com/gofiber/fiber/v3"
)

const testTick = 15 * time.Second

// The header must say a never-read mirror is never read, not "0s": a project
// the loop has not reached yet is not a project it just looked at (#394).
func TestMirrorReadSaysNeverInWords(t *testing.T) {
	now := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		at     *time.Time
		source string
	}{
		{"nil stamp", nil, "forge"},
		{"zero stamp", new(time.Time), "rig"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := mirrorReadAt(now, tc.at, testTick, 0, tc.source)
			if want := "last " + tc.source + " read never"; got.Label != want {
				t.Fatalf("label = %q, want %q", got.Label, want)
			}
			if got.State != "never" {
				t.Fatalf("state = %q, want never", got.State)
			}
			if got.At != "" {
				t.Fatalf("at = %q, want empty", got.At)
			}
		})
	}
}

// Age wording is shared with the item age column, so it is pinned here: seconds
// under a minute, whole minutes under an hour, h:mm under a day, days beyond.
func TestAgeBetweenFormatting(t *testing.T) {
	now := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		age  time.Duration
		want string
	}{
		{0, "0s"},
		{9 * time.Second, "9s"},
		{59 * time.Second, "59s"},
		{time.Minute, "1m"},
		{4*time.Minute + 30*time.Second, "4m"},
		{time.Hour, "1h00m"},
		{3*time.Hour + 7*time.Minute, "3h07m"},
		{26 * time.Hour, "1d"},
		{72 * time.Hour, "3d"},
	} {
		if got := ageBetween(now, now.Add(-tc.age)); got != tc.want {
			t.Fatalf("ageBetween(%s) = %q, want %q", tc.age, got, tc.want)
		}
	}
	// A stamp in the future is not a negative age; it reads as 0s.
	if got := ageBetween(now, now.Add(time.Minute)); got != "0s" {
		t.Fatalf("future stamp = %q, want 0s", got)
	}
}

// Two ticks is where a read stops being fresh; five is where it is stalled.
// The boundary instants themselves belong to the calmer side.
func TestMirrorReadThresholds(t *testing.T) {
	now := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		age    time.Duration
		state  string
		suffix string
	}{
		{0, "ok", ""},
		{testTick, "ok", ""},
		{2 * testTick, "ok", ""},
		{2*testTick + time.Second, "quiet", " · quiet"},
		{5 * testTick, "quiet", " · quiet"},
		{5*testTick + time.Second, "stalled", " · stalled"},
	} {
		at := now.Add(-tc.age)
		got := mirrorReadAt(now, &at, testTick, 0, "forge")
		if got.State != tc.state {
			t.Fatalf("age %s: state = %q, want %q", tc.age, got.State, tc.state)
		}
		want := "last forge read " + ageBetween(now, at) + tc.suffix
		if got.Label != want {
			t.Fatalf("age %s: label = %q, want %q", tc.age, got.Label, want)
		}
	}
	// A caller that never learned the cadence still measures against the real
	// default rather than calling every read stalled.
	at := now.Add(-40 * time.Second)
	if got := mirrorReadAt(now, &at, 0, 0, "rig").State; got != "quiet" {
		t.Fatalf("zero tick state = %q, want quiet at the default cadence", got)
	}
}

// A quiet box backs the poll off to idle_poll_max, so a stamp a whole window
// old is healthy: the floors rise to the cadence's own cap (#408). With
// tick 1m and a 10m cap, nine minutes is fresh, eleven is quiet and twenty-one
// is stalled — against tick_interval alone those would read quiet/quiet/stalled.
func TestMirrorReadThresholdsMaxWaitAware(t *testing.T) {
	now := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	const (
		tick    = time.Minute
		maxWait = 10 * time.Minute
	)

	if quiet, stalled := mirrorWindows(tick, maxWait); quiet != 10*time.Minute || stalled != 20*time.Minute {
		t.Fatalf("windows = %s/%s, want 10m/20m at tick 1m and a 10m cap", quiet, stalled)
	}
	for _, tc := range []struct {
		age   time.Duration
		state string
	}{
		{9 * time.Minute, "ok"},
		{11 * time.Minute, "quiet"},
		{21 * time.Minute, "stalled"},
	} {
		at := now.Add(-tc.age)
		if got := mirrorReadAt(now, &at, tick, maxWait, "forge").State; got != tc.state {
			t.Fatalf("age %s with a 10m cap: state = %q, want %q", tc.age, got, tc.state)
		}
	}

	// A box with no lifecycle block has maxWait == tick, so the spec's plain
	// 2x/5x comes back out and the pinned scenario (60s tick, 10m old read) still
	// reads stalled.
	if quiet, stalled := mirrorWindows(time.Minute, time.Minute); quiet != 2*time.Minute || stalled != 5*time.Minute {
		t.Fatalf("block-less windows = %s/%s, want the spec's 2m/5m", quiet, stalled)
	}
	at := now.Add(-10 * time.Minute)
	if got := mirrorReadAt(now, &at, time.Minute, time.Minute, "forge").State; got != "stalled" {
		t.Fatalf("block-less 10m read = %q, want stalled", got)
	}
}

type mirrorReadJSON struct {
	Label string `json:"label"`
	State string `json:"state"`
	At    string `json:"at"`
}

// The board serves the phrase itself, so a client cannot invent its own
// thresholds; the JSON is the contract (#394).
func TestBoardServesMirrorReadAges(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fleet.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	read, err := st.CreateProject(ctx, store.ProjectInput{
		Key: "read", Name: "read", ForgeKind: "github", ForgeProject: "acme/read",
		Rig: "read", ConcurrencyCap: 2, Enabled: true,
	})
	if err != nil {
		t.Fatalf("project read: %v", err)
	}
	if err := st.SetForgeStamp(ctx, read, time.Now().UTC()); err != nil {
		t.Fatalf("forge stamp: %v", err)
	}
	if _, err := st.CreateProject(ctx, store.ProjectInput{
		Key: "never", Name: "never", ForgeKind: "github", ForgeProject: "acme/never",
		Rig: "never", ConcurrencyCap: 2, Enabled: true,
	}); err != nil {
		t.Fatalf("project never: %v", err)
	}

	app, err := New(Config{Token: "test-token", Store: st, ConfigPath: "/tmp/fake-config.yaml", TickInterval: testTick})
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/board", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer test-token")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	var board struct {
		Projects []struct {
			Key   string         `json:"key"`
			Forge mirrorReadJSON `json:"forge_read"`
			Rig   mirrorReadJSON `json:"rig_read"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(body, &board); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	byKey := make(map[string]int, len(board.Projects))
	for i, project := range board.Projects {
		byKey[project.Key] = i
	}
	fresh, ok := byKey["read"]
	if !ok {
		t.Fatalf("project read missing from %s", body)
	}
	if got := board.Projects[fresh].Forge.State; got != "ok" {
		t.Fatalf("fresh forge read state = %q, want ok", got)
	}
	if got := board.Projects[fresh].Forge.Label; len(got) <= len("last forge read ") || got[:len("last forge read ")] != "last forge read " {
		t.Fatalf("fresh forge read label = %q, want it to name the read and its age", got)
	}
	// No rig stamp was written, so that half of the same header is a never.
	if got := board.Projects[fresh].Rig.State; got != "never" {
		t.Fatalf("fresh rig read state = %q, want never", got)
	}
	if got := board.Projects[fresh].Rig.Label; got != "last rig read never" {
		t.Fatalf("fresh rig read label = %q", got)
	}
	never, ok := byKey["never"]
	if !ok {
		t.Fatalf("project never missing from %s", body)
	}
	if got := board.Projects[never].Forge; got.State != "never" || got.Label != "last forge read never" || got.At != "" {
		t.Fatalf("never project forge read = %+v", got)
	}
	if got := board.Projects[never].Rig; got.State != "never" || got.Label != "last rig read never" {
		t.Fatalf("never project rig read = %+v", got)
	}
}
