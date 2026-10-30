package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/store"
	"github.com/dinhtrung/fleet-console/internal/web"
	"github.com/gofiber/fiber/v3"
)

const roundTripConfig = `# the operator's header
listen: 127.0.0.1:8787

projects:
  - key: existing
    name: Existing
    forge: github
    repo: org/existing
    rigs: [rig-a]
    concurrency_cap: 1
`

// TestBoardWriteSurvivesPrintAndApply: a file the board wrote is accepted by
// the loader, printed by --print-config, and unchanged by --apply-config twice.
func TestBoardWriteSurvivesPrintAndApply(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	database := filepath.Join(dir, "fleet.db")
	if err := os.WriteFile(configPath, []byte(roundTripConfig), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	db, err := store.Open(context.Background(), database)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	app, err := web.New(web.Config{Token: "test-token", Store: db, ConfigPath: configPath})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	body := `{"key":"new-proj","name":"New project","forge":"github","repo":"org/new",
	 "default_branch":"main","rigs":["rig-b"],"concurrency_cap":2,
	 "paths":{"main":"./checkouts/new","worktrees":"./wt"}}`
	request := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(body))
	request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	request.Header.Set(fiber.HeaderAuthorization, "Bearer test-token")
	request.Header.Set("Idempotency-Key", "round-trip")
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if response.StatusCode != http.StatusCreated {
		payload, _ := io.ReadAll(response.Body)
		t.Fatalf("status = %d, want 201 (%s)", response.StatusCode, payload)
	}

	var printed bytes.Buffer
	if err := run([]string{"--config", configPath, "--print-config"}, &printed, io.Discard); err != nil {
		t.Fatalf("print-config: %v", err)
	}
	if !strings.Contains(printed.String(), "key: new-proj") {
		t.Fatalf("--print-config does not name the created project:\n%s", printed.String())
	}

	apply := func() string {
		var out bytes.Buffer
		if err := run([]string{"--config", configPath, "--db", database, "--apply-config"}, &out, io.Discard); err != nil {
			t.Fatalf("apply-config: %v", err)
		}
		return out.String()
	}
	// The board's write is what the first apply meets; the pre-existing project
	// is created on the same run. Two further runs must both say unchanged.
	apply()
	first := apply()
	if !strings.Contains(first, "unchanged new-proj") || !strings.Contains(first, "unchanged existing") {
		t.Fatalf("apply = %q, want both projects unchanged", first)
	}
	second := apply()
	if first != second {
		t.Fatalf("second apply changed something:\nfirst:\n%s\nsecond:\n%s", first, second)
	}

	// The store now holds both projects and the created one's paths.
	row, err := db.ProjectByKey(context.Background(), "new-proj")
	if err != nil || row == nil {
		t.Fatalf("store row = %+v (%v)", row, err)
	}
	path, err := db.RepoPath(context.Background(), row.ID)
	if err != nil || filepath.Base(path) != "new" {
		t.Fatalf("repo path = %q (%v), want the resolved paths.main", path, err)
	}

	// And a second POST for the same key is a conflict, not a second entry.
	again := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(body))
	again.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	again.Header.Set(fiber.HeaderAuthorization, "Bearer test-token")
	again.Header.Set("Idempotency-Key", "round-trip-2")
	conflict, err := app.Test(again)
	if err != nil {
		t.Fatalf("post again: %v", err)
	}
	if conflict.StatusCode != http.StatusConflict {
		payload, _ := io.ReadAll(conflict.Body)
		t.Fatalf("second post status = %d, want 409 (%s)", conflict.StatusCode, payload)
	}
	var count int
	var listed struct {
		Projects []struct {
			Key string `json:"key"`
		} `json:"projects"`
	}
	listRequest := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	listRequest.Header.Set(fiber.HeaderAuthorization, "Bearer test-token")
	listResponse, err := app.Test(listRequest)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if err := json.NewDecoder(listResponse.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	for _, project := range listed.Projects {
		if project.Key == "new-proj" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("GET /api/projects has %d new-proj entries, want 1", count)
	}
}
