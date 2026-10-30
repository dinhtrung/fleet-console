package web

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gofiber/fiber/v3"
)

func boardCookie(resp *http.Response) *http.Cookie {
	for _, cookie := range resp.Cookies() {
		if cookie.Name == boardCookieName {
			return cookie
		}
	}
	return nil
}

// TestBoardPageGrantsSessionOnlyOnLoopback replaces the old no-cookie-on-/ guard. The board has
// no sign-in screen, so the loopback request that loads it is granted the session — a real
// listener is used because only a real connection carries the client address.
func TestBoardPageGrantsSessionOnlyOnLoopback(t *testing.T) {
	board := fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>board</title>")}}
	app := fiber.New()
	app.Use(boardHandler(board, "test-token"))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = app.Listener(ln) }()
	t.Cleanup(func() { _ = app.Shutdown() })

	resp, err := http.Get("http://" + ln.Addr().String() + "/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	cookie := boardCookie(resp)
	if cookie == nil {
		t.Fatal("loopback GET / issued no board session")
	}
	if want := boardCookieValue("test-token"); cookie.Value != want {
		t.Fatalf("loopback session = %q, want %q", cookie.Value, want)
	}
}

// TestIsLoopback pins the address test the grant rests on: only a real loopback client is
// trusted, so a spoofed X-Forwarded-For cannot mint a session for a remote caller.
func TestIsLoopback(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"::ffff:127.0.0.1", true},
		{"192.0.2.1", false},
		{"10.0.0.5", false},
		{"", false},
		{"not-an-ip", false},
	}
	for _, tc := range cases {
		if got := isLoopback(tc.ip); got != tc.want {
			t.Errorf("isLoopback(%q) = %v, want %v", tc.ip, got, tc.want)
		}
	}
}

func TestSessionRequiresTheOperatorToken(t *testing.T) {
	app := testApp(t)

	req := httptest.NewRequest(http.MethodPost, "/api/session", strings.NewReader(`{"token":"wrong"}`))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token: status = %d, want 401", resp.StatusCode)
	}
	if cookie := boardCookie(resp); cookie != nil {
		t.Fatalf("wrong token issued a cookie %q", cookie.Value)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/session", strings.NewReader(`{"token":"`+testToken+`"}`))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	resp, err = app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("right token: status = %d, want 200", resp.StatusCode)
	}
	cookie := boardCookie(resp)
	if cookie == nil {
		t.Fatal("right token did not issue a board cookie")
	}
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie flags = %+v, want HttpOnly and SameSite=Strict", cookie)
	}

	// The session, and only the session, grants the API.
	req = httptest.NewRequest(http.MethodGet, "/api/compat", nil)
	req.AddCookie(&http.Cookie{Name: boardCookieName, Value: cookie.Value})
	resp, err = app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("session cookie: status = %d, want 200", resp.StatusCode)
	}
}

func TestForgedBoardCookieRejected(t *testing.T) {
	app := testApp(t)
	req := httptest.NewRequest(http.MethodGet, "/api/compat", nil)
	req.AddCookie(&http.Cookie{Name: boardCookieName, Value: "deadbeef"})
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("forged cookie: status = %d, want 401", resp.StatusCode)
	}
}

func TestLoginForm(t *testing.T) {
	app := testApp(t)

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `name="token"`) {
		t.Fatalf("GET /login = %d, body missing token field", resp.StatusCode)
	}

	form := url.Values{"token": {testToken}}.Encode()
	req = httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationForm)
	resp, err = app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /login = %d, want 303", resp.StatusCode)
	}
	if cookie := boardCookie(resp); cookie == nil {
		t.Fatal("login did not issue a board cookie")
	}
}

func TestLogoutClearsTheSession(t *testing.T) {
	app := testApp(t)
	req := httptest.NewRequest(http.MethodDelete, "/api/session", nil)
	req.AddCookie(&http.Cookie{Name: boardCookieName, Value: boardCookieValue(testToken)})
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE /api/session = %d, want 200", resp.StatusCode)
	}
	cookie := boardCookie(resp)
	if cookie == nil || cookie.Value != "" || cookie.MaxAge >= 0 {
		t.Fatalf("logout cookie = %+v, want a cleared cookie", cookie)
	}
}
