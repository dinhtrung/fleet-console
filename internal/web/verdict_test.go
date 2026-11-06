package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/store"
	"github.com/gofiber/fiber/v3"
)

// verdictFixture is one project with item 7 dispatched and a draft change
// request that closes it.
type verdictFixture struct {
	app  *fiber.App
	fake *forge.Fake
	st   *store.Store
}

func newVerdictFixture(t *testing.T, dispatch bool) verdictFixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fleet.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	projectID, err := st.CreateProject(ctx, store.ProjectInput{
		Key: "app", Name: "app", ForgeKind: "github", ForgeProject: "dinhtrung/fleet-console",
		Rig: "fleet-console", ConcurrencyCap: 2, Enabled: true,
	})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	at := time.Date(2026, 11, 4, 5, 0, 0, 0, time.UTC)
	if err := st.ReplaceItems(ctx, projectID, []store.ItemRecord{{
		Number: 7, Kind: "task", State: "open", Title: "Human verdict", Labels: "", BlockedByJSON: "[]",
		URL: "u", UpdatedAt: at,
	}}); err != nil {
		t.Fatalf("items: %v", err)
	}
	if dispatch {
		if _, err := st.CreateDispatch(ctx, store.NewDispatch{
			ProjectID: projectID, ItemNumber: 7, Attempt: 1, Seat: "dev-build-1@fleet-console",
			Branch: "feature/7-human-verdict", Worktree: "/tmp/wt", BaseSHA: "base", State: "active", CreatedAt: at,
		}); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
	}
	fake := forge.NewFake(forge.KindGitHub)
	fake.RequestsValue = []forge.ChangeRequest{{
		Number: 12, State: forge.StateOpen, HeadSHA: "abc1234", Draft: true, Mergeable: true,
		ClosesItem: 7, Checks: []forge.Check{{Name: "ci", Conclusion: "success"}},
	}}
	app, err := New(Config{
		Token: "test-token", Store: st, ConfigPath: "/tmp/fake-config.yaml",
		ForgeFor: func(context.Context, store.ProjectRow) (forge.Forge, error) { return fake, nil },
	})
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	return verdictFixture{app: app, fake: fake, st: st}
}

func postVerdict(t *testing.T, app *fiber.App, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/items/7/verdict", bytes.NewBufferString(body))
	req.Header.Set(fiber.HeaderAuthorization, "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "test-key-"+body)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// An approve must land as the exact note the merge gate reads, name the head sha,
// take the draft out of draft, and say the merge belongs to the next act tick.
func TestItemVerdictAcceptPostsGateShapedNote(t *testing.T) {
	f := newVerdictFixture(t, true)
	status, out := postVerdict(t, f.app, `{"verdict":"accept","reason":"read the diff, checks are green"}`)
	if status != http.StatusOK {
		t.Fatalf("status %d: %v", status, out)
	}
	if out["sha"] != "abc1234" || out["merge"] != "act-tick" || out["ready"] != true {
		t.Fatalf("answer %v", out)
	}
	var comment, ready bool
	for _, write := range f.fake.Writes {
		switch write.Op {
		case "comment":
			comment = true
			if write.Number != 12 {
				t.Fatalf("comment landed on %d, want the change request 12", write.Number)
			}
			first := strings.SplitN(write.Value, "\n", 2)[0]
			if first != "ACCEPT abc1234" {
				t.Fatalf("first line %q, want ACCEPT abc1234", first)
			}
			if !strings.Contains(write.Value, "read the diff") {
				t.Fatalf("reason missing from %q", write.Value)
			}
		case "ready":
			ready = true
		}
	}
	if !comment || !ready {
		t.Fatalf("writes %v: want a comment and a ready", f.fake.Writes)
	}
}

// A reject is the builder's work list, so an empty one is refused before any
// forge write: a REJECT with no reason parks the row with nothing to act on.
func TestItemVerdictRejectNeedsReason(t *testing.T) {
	f := newVerdictFixture(t, true)
	status, out := postVerdict(t, f.app, `{"verdict":"reject"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status %d: %v", status, out)
	}
	if len(f.fake.Writes) != 0 {
		t.Fatalf("writes after a refused reject: %v", f.fake.Writes)
	}
}

// A reject with a reason posts REJECT <sha> and must not mark anything ready.
func TestItemVerdictRejectPostsReason(t *testing.T) {
	f := newVerdictFixture(t, true)
	status, out := postVerdict(t, f.app, `{"verdict":"reject","reason":"the probe skips the fixture"}`)
	if status != http.StatusOK {
		t.Fatalf("status %d: %v", status, out)
	}
	if out["ready"] != false {
		t.Fatalf("ready on a reject: %v", out)
	}
	for _, write := range f.fake.Writes {
		if write.Op == "ready" {
			t.Fatalf("a reject marked the change request ready: %v", f.fake.Writes)
		}
		if write.Op == "comment" && !strings.HasPrefix(write.Value, "REJECT abc1234") {
			t.Fatalf("note %q", write.Value)
		}
	}
}

// An item whose change request is already merged has nothing to judge; answering
// 200 there would tell the operator a verdict landed that nothing will read.
func TestItemVerdictRefusesWhenNothingIsOpen(t *testing.T) {
	f := newVerdictFixture(t, true)
	f.fake.RequestsValue[0].Merged = true
	status, _ := postVerdict(t, f.app, `{"verdict":"accept"}`)
	if status != http.StatusConflict {
		t.Fatalf("status %d, want 409", status)
	}
}

// No dispatch row is the documented adoption gap: the note is still the record,
// but nothing will merge it, so the answer says manual instead of pretending.
func TestItemVerdictWithoutDispatchSaysManual(t *testing.T) {
	f := newVerdictFixture(t, false)
	status, out := postVerdict(t, f.app, `{"verdict":"accept"}`)
	if status != http.StatusOK {
		t.Fatalf("status %d: %v", status, out)
	}
	if out["merge"] != "manual" {
		t.Fatalf("merge %v, want manual", out["merge"])
	}
}
