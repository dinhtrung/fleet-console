package web

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/store"
)

// setPaused flips the DB-owned pause the way the board's own route does.
func setPaused(t *testing.T, st *store.Store, onHold bool) {
	t.Helper()
	if _, err := st.SetProjectOnHold(context.Background(), 1, onHold); err != nil {
		t.Fatalf("set onhold=%v: %v", onHold, err)
	}
}

// A paused project answers 409 before the first forge call: no note, no ready
// mark, no wake. The same verdict after resume lands as usual — the note is
// posted and the loop is woken (pause-stops-forge, design D4).
func TestItemVerdictRefusedWhilePaused(t *testing.T) {
	f := newVerdictFixture(t, true)
	waker := f.waker

	setPaused(t, f.st, true)

	status, raw := postVerdictRaw(t, f.app, `{"verdict":"accept"}`)
	if status != http.StatusConflict {
		t.Fatalf("paused status = %d, want 409; body=%q", status, raw)
	}
	if raw != "project is paused; resume it to post a verdict" {
		t.Fatalf("paused body = %q, want the fixed refusal", raw)
	}
	if len(f.fake.Writes) != 0 {
		t.Fatalf("paused verdict wrote to the forge: %+v", f.fake.Writes)
	}
	if len(f.fake.NotesAsked) != 0 {
		t.Fatalf("paused verdict read notes: %v", f.fake.NotesAsked)
	}
	select {
	case <-waker.Channel():
		t.Fatal("paused verdict woke the loop")
	default:
	}

	// Resume and post the same verdict: the note lands and the loop wakes.
	setPaused(t, f.st, false)
	resumedStatus, out := postVerdict(t, f.app, `{"verdict":"accept"}`)
	if resumedStatus != http.StatusOK {
		t.Fatalf("resumed status = %d, want 200; out=%v", resumedStatus, out)
	}
	if len(f.fake.Writes) != 2 {
		t.Fatalf("resumed writes = %+v, want the comment and the ready mark", f.fake.Writes)
	}
	if out["ready"] != true {
		t.Fatalf("resumed out = %v, want ready true", out)
	}
	select {
	case <-waker.Channel():
	default:
		t.Fatal("resumed verdict did not wake the loop")
	}
	// The paused attempt left no audit record: nothing happened.
	events, err := f.st.RecentEvents(context.Background(), 5)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %+v, want only the resumed verdict's record", events)
	}
}

// The same guard applies when the project was never enabled: a disabled project
// is paused by definition (projectPaused treats them alike).
func TestItemVerdictRefusedOnDisabledProject(t *testing.T) {
	f := newVerdictFixture(t, true)
	if _, err := f.st.SetProjectEnabled(context.Background(), 1, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	status, _ := postVerdict(t, f.app, `{"verdict":"reject","reason":"no"}`)
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409", status)
	}
	if len(f.fake.Writes) != 0 {
		t.Fatalf("writes = %+v, want none", f.fake.Writes)
	}
}

// The read-age label says paused whatever the stamp says: a day-old stamp on a
// paused project is not "stalled", and the same stamp on an enabled project is.
func TestMirrorReadPausedSaysPausedNotStalled(t *testing.T) {
	now := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	day := now.Add(-24 * time.Hour)

	paused := mirrorReadAt(now, &day, testTick, 0, "forge", true)
	if paused.Label != "paused — no forge reads" {
		t.Fatalf("paused label = %q, want %q", paused.Label, "paused — no forge reads")
	}
	if paused.State != "paused" {
		t.Fatalf("paused state = %q, want paused", paused.State)
	}
	if paused.At != day.UTC().Format(time.RFC3339) {
		t.Fatalf("paused at = %q, want the stamp kept", paused.At)
	}
	enabled := mirrorReadAt(now, &day, testTick, 0, "forge", false)
	if enabled.State != "stalled" {
		t.Fatalf("enabled state = %q, want stalled at the same stamp", enabled.State)
	}
	// A paused project that has never read still says never, not paused.
	never := mirrorReadAt(now, nil, testTick, 0, "forge", true)
	if never.State != "never" || never.Label != "last forge read never" {
		t.Fatalf("never = %+v, want the never-read line", never)
	}
}

// The board payload carries the paused label through to the client, so the
// header a paused project renders is the server's words and not the client's.
func TestBoardPayloadSaysPausedForgeRead(t *testing.T) {
	f := newVerdictFixture(t, false)
	ctx := context.Background()
	if err := f.st.SetForgeStamp(ctx, 1, time.Now().Add(-24*time.Hour)); err != nil {
		t.Fatalf("forge stamp: %v", err)
	}
	if err := f.st.SetRigStamp(ctx, 1, time.Now().Add(-24*time.Hour)); err != nil {
		t.Fatalf("rig stamp: %v", err)
	}
	setPaused(t, f.st, true)

	resp, body := do(t, f.app, http.MethodGet, "/api/board", "test-token", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	var out struct {
		Projects []struct {
			Paused    bool       `json:"paused"`
			ForgeRead mirrorRead `json:"forge_read"`
			RigRead   mirrorRead `json:"rig_read"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(out.Projects) != 1 {
		t.Fatalf("projects = %d, want 1 (%s)", len(out.Projects), body)
	}
	project := out.Projects[0]
	if !project.Paused {
		t.Fatalf("project = %+v, want paused true", project)
	}
	if project.ForgeRead.State != "paused" || project.ForgeRead.Label != "paused — no forge reads" {
		t.Fatalf("forge_read = %+v, want the paused label", project.ForgeRead)
	}
	// Only the forge read goes quiet on a pause: the rig reads and the roster
	// stay fleet-wide (design D3), so the rig line keeps its age and its state.
	if project.RigRead.State == "paused" || project.RigRead.Label != "last rig read 1d · stalled" {
		t.Fatalf("rig_read = %+v, want the age-based line even while paused", project.RigRead)
	}
}

// The item drawer's project header carries the same two read ages as the board,
// so a paused project's rig line is the age-based one there too.
func TestItemPayloadSaysPausedForgeRead(t *testing.T) {
	f := newVerdictFixture(t, true)
	ctx := context.Background()
	if err := f.st.SetForgeStamp(ctx, 1, time.Now().Add(-24*time.Hour)); err != nil {
		t.Fatalf("forge stamp: %v", err)
	}
	if err := f.st.SetRigStamp(ctx, 1, time.Now().Add(-24*time.Hour)); err != nil {
		t.Fatalf("rig stamp: %v", err)
	}
	setPaused(t, f.st, true)

	resp, body := do(t, f.app, http.MethodGet, "/api/items/7", "test-token", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	var out struct {
		Project struct {
			ForgeRead mirrorRead `json:"forge_read"`
			RigRead   mirrorRead `json:"rig_read"`
		} `json:"project"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if out.Project.ForgeRead.State != "paused" {
		t.Fatalf("forge_read = %+v, want paused", out.Project.ForgeRead)
	}
	if out.Project.RigRead.State == "paused" || out.Project.RigRead.Label != "last rig read 1d · stalled" {
		t.Fatalf("rig_read = %+v, want the age-based line even while paused", out.Project.RigRead)
	}
}
