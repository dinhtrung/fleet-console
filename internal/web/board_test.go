package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gofiber/fiber/v3"
)

func boardApp() *fiber.App {
	board := fstest.MapFS{
		"index.html":    {Data: []byte("<!doctype html><title>board</title>")},
		"assets/app.js": {Data: []byte("console.log('board')")},
	}
	app := fiber.New()
	app.Use(boardHandler(board, "test-token"))
	return app
}

func TestBoardServesIndex(t *testing.T) {
	resp, err := boardApp().Test(httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestBoardServesAssetWithContentType(t *testing.T) {
	resp, err := boardApp().Test(httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Fatalf("content type = %q, want javascript", ct)
	}
}

func TestBoardFallsBackToIndexForClientRoutes(t *testing.T) {
	resp, err := boardApp().Test(httptest.NewRequest(http.MethodGet, "/projects/one/tasks/2", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestBoardLeavesUnknownAPINotFound(t *testing.T) {
	resp, err := boardApp().Test(httptest.NewRequest(http.MethodGet, "/api/nope", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
