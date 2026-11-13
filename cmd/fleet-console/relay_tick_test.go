package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/config"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// relayTickConfig is a project whose config block names no verdict relay: the
// relay mode lives only on the project row, which is the board's half (#375).
// The tick must still plan the relay (the #397 bug: the row never reached the
// decide input).
const relayTickConfig = `listen: 127.0.0.1:0
database: %DATABASE%
projects:
  - key: app
    name: app
    forge: github
    repo: o/r
    rigs: [rig]
    concurrency_cap: 2
`

const relayTickHead = "aaaa1111111111111111111111111111111a"

// ghFixture answers the GitHub reads one open item and one open change
// request cost, so a real runTick can plan from them.
type ghFixture struct{}

func (ghFixture) Run(_ context.Context, argv ...string) (string, string, error) {
	path := ""
	for _, arg := range argv {
		if strings.HasPrefix(arg, "repos/") {
			path = arg
			break
		}
	}
	switch {
	case strings.HasPrefix(path, "repos/o/r/issues?"):
		return `[{"number":5,"title":"Five","state":"open","labels":[],"html_url":"u"}]`, "", nil
	case strings.HasSuffix(path, "/sub_issues"), strings.HasSuffix(path, "/dependencies/blocked_by"),
		strings.HasSuffix(path, "/files?per_page=100"):
		return `[]`, "", nil
	case strings.HasPrefix(path, "repos/o/r/pulls?"):
		return `[{"number":20,"state":"open","mergable":true,"mergeable":true,"mergeable_state":"clean",
			"head":{"sha":"` + relayTickHead + `","ref":"feature/5-five"},"body":"Closes #5","html_url":"u"}]`, "", nil
	case strings.Contains(path, "/check-runs"):
		return `{"check_runs":[{"name":"test","status":"completed","conclusion":"success"}]}`, "", nil
	case strings.HasSuffix(path, "/status"):
		return `{"statuses":[]}`, "", nil
	default:
		return "", "unexpected gh api " + path, os.ErrNotExist
	}
}

// rigFixture answers the two reads the mirror makes, with one reviewer row that
// ended without writing a verdict and names the change request's head.
type rigFixture struct{}

func (rigFixture) Run(_ context.Context, args ...string) ([]byte, error) {
	joined := strings.Join(args, " ")
	switch {
	case strings.HasPrefix(joined, "queue list"):
		return []byte(`[{"qitemId":"q-1","sourceSession":"dev-builder@rig",
			"destinationSession":"dev-review@rig","state":"done",
			"pickup":{"state":"done","evidence":"closed"},
			"body":"review #5 change request #20 head ` + relayTickHead + `"}]`), nil
	case strings.HasPrefix(joined, "ps "):
		return []byte(`[{"nodeId":"n1","rigId":"r1","rigName":"rig",
			"canonicalSessionName":"dev-build@rig","logicalId":"dev.build",
			"lifecycleState":"running"}]`), nil
	default:
		return nil, os.ErrNotExist
	}
}

// TestTickPlansTheRelayFromTheProjectRow runs the real dry-run tick: config in a
// file, a store whose project row names relay_mode scribe, the real mirror and
// the real PlanTick. The plan's JSON must carry the relay intent, which is the
// exact path #397 found dead.
func TestTickPlansTheRelayFromTheProjectRow(t *testing.T) {
	dir := t.TempDir()
	database := filepath.Join(dir, "fleet.db")
	configPath := filepath.Join(dir, "config.yaml")
	body := strings.ReplaceAll(relayTickConfig, "%DATABASE%", database)
	if err := os.WriteFile(configPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	eff, err := config.Load(config.Options{
		FlagPath: configPath,
		Env:      func(string) string { return "" },
		Home:     dir, Cwd: dir,
	})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	ctx := context.Background()
	db, err := store.Open(ctx, database)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	if _, _, err := db.UpsertProject(ctx, store.UpsertInput{
		Project: store.ProjectInput{
			Key: "app", Name: "app", ForgeKind: "github", ForgeProject: "o/r",
			Rig: "rig", ConcurrencyCap: 2, Enabled: true,
			RelayMode: "scribe", RelaySeat: "scribe@rig",
		},
		SyncRelay: true,
	}); err != nil {
		t.Fatalf("project: %v", err)
	}

	var out, errOut bytes.Buffer
	if err := runTick(ctx, eff, db, &out, &errOut, ghFixture{}, rigFixture{}, mirrorTimeouts(eff)); err != nil {
		t.Fatalf("tick: %v\nstderr: %s", err, errOut.String())
	}
	var tick struct {
		Projects map[string]struct {
			Intents []struct {
				Kind string `json:"kind"`
				Item int    `json:"item"`
				Head string `json:"head_sha"`
			} `json:"intents"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(out.Bytes(), &tick); err != nil {
		t.Fatalf("decode tick %s: %v", out.String(), err)
	}
	plan := tick.Projects["app"]
	var relays []struct {
		Kind string `json:"kind"`
		Item int    `json:"item"`
		Head string `json:"head_sha"`
	}
	for _, intent := range plan.Intents {
		if intent.Kind == "relay_verdict" {
			relays = append(relays, intent)
		}
	}
	if len(relays) != 1 {
		t.Fatalf("relays = %+v, want one from the project row's mode\ntick: %s\nstderr: %s",
			relays, out.String(), errOut.String())
	}
	if relays[0].Item != 5 || relays[0].Head != relayTickHead {
		t.Fatalf("relay = %+v, want #5 at %s", relays[0], relayTickHead)
	}
}

// TestTickWithoutRelayModePlansNoRelay is the other half at the same seam: the
// same row with no mode plans no relay.
func TestTickWithoutRelayModePlansNoRelay(t *testing.T) {
	dir := t.TempDir()
	database := filepath.Join(dir, "fleet.db")
	configPath := filepath.Join(dir, "config.yaml")
	body := strings.ReplaceAll(relayTickConfig, "%DATABASE%", database)
	if err := os.WriteFile(configPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	eff, err := config.Load(config.Options{
		FlagPath: configPath,
		Env:      func(string) string { return "" },
		Home:     dir, Cwd: dir,
	})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	ctx := context.Background()
	db, err := store.Open(ctx, database)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	if _, _, err := db.UpsertProject(ctx, store.UpsertInput{
		Project: store.ProjectInput{
			Key: "app", Name: "app", ForgeKind: "github", ForgeProject: "o/r",
			Rig: "rig", ConcurrencyCap: 2, Enabled: true,
		},
	}); err != nil {
		t.Fatalf("project: %v", err)
	}

	var out, errOut bytes.Buffer
	if err := runTick(ctx, eff, db, &out, &errOut, ghFixture{}, rigFixture{}, mirrorTimeouts(eff)); err != nil {
		t.Fatalf("tick: %v\nstderr: %s", err, errOut.String())
	}
	var tick struct {
		Projects map[string]struct {
			Intents []struct {
				Kind string `json:"kind"`
			} `json:"intents"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(out.Bytes(), &tick); err != nil {
		t.Fatalf("decode tick %s: %v", out.String(), err)
	}
	for _, intent := range tick.Projects["app"].Intents {
		if intent.Kind == "relay_verdict" {
			t.Fatalf("intents = %+v, want none: the project never opted in", tick.Projects["app"].Intents)
		}
	}
}
