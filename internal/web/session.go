package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net"
	"strings"

	"github.com/gofiber/fiber/v3"
)

// The board session is only ever issued after the caller proves the API token.
// It used to be attached to every non-API request, which handed a working
// credential to any visitor: that was the auth bypass. A board session is a
// token-derived value with an expiry; rotating the token invalidates it.
const boardCookieName = "fleet_session"

func boardCookieValue(token string) string {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte("fleet-console-board"))
	return hex.EncodeToString(mac.Sum(nil))
}

// issueBoardCookie grants the board session. Call it only after checking the token.
func issueBoardCookie(c fiber.Ctx, token string) {
	c.Cookie(&fiber.Cookie{
		Name:     boardCookieName,
		Value:    boardCookieValue(token),
		Path:     "/",
		HTTPOnly: true,
		Secure:   c.Protocol() == "https",
		SameSite: fiber.CookieSameSiteStrictMode,
		MaxAge:   12 * 60 * 60,
	})
}

func clearBoardCookie(c fiber.Ctx) {
	c.Cookie(&fiber.Cookie{
		Name:     boardCookieName,
		Value:    "",
		Path:     "/",
		HTTPOnly: true,
		Secure:   c.Protocol() == "https",
		SameSite: fiber.CookieSameSiteStrictMode,
		MaxAge:   -1,
	})
}

// isLoopback reports whether the request arrived over the loopback interface.
func isLoopback(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.IsLoopback()
}
func isBoardCookie(c fiber.Ctx, token string) bool {
	got := c.Cookies(boardCookieName)
	return got != "" && hmac.Equal([]byte(got), []byte(boardCookieValue(token)))
}

func tokenMatches(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

const loginPageHTML = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>fleet-console</title>
<meta name="viewport" content="width=device-width, initial-scale=1"></head>
<body>
<main style="max-width:24rem;margin:12vh auto;font:14px/1.5 system-ui,sans-serif">
<h1 style="font-size:1.1rem">fleet-console</h1>
<p>The operator token is required.</p>
<form method="post" action="/login">
  <label for="token">Operator token</label>
  <input id="token" name="token" type="password" autocomplete="current-password" required autofocus
         style="display:block;width:100%;padding:.5rem;margin:.5rem 0">
  <button type="submit" style="padding:.5rem .9rem">Sign in</button>
</form>
%ERROR%
</main>
</body>
</html>
`

// loginPage is the only unauthenticated page that can mint a session.
func loginPage(c fiber.Ctx) error {
	c.Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)
	return c.SendString(loginPageHTML)
}

// loginSubmit validates the form token and issues the board session.
func loginSubmit(token string) fiber.Handler {
	return func(c fiber.Ctx) error {
		if !tokenMatches(c.FormValue("token"), token) {
			c.Status(fiber.StatusUnauthorized)
			c.Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)
			page := strings.Replace(loginPageHTML, "%ERROR%",
				`<p style="color:#903078">That token is not the operator token.</p>`, 1)
			return c.SendString(page)
		}
		issueBoardCookie(c, token)
		return c.Redirect().Status(fiber.StatusSeeOther).To("/")
	}
}

// sessionCreate is the JSON form of the login for the board SPA.
func sessionCreate(token string) fiber.Handler {
	return func(c fiber.Ctx) error {
		var body struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(c.Body(), &body); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "body must be JSON"})
		}
		if !tokenMatches(body.Token, token) {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
		}
		issueBoardCookie(c, token)
		return c.JSON(fiber.Map{"status": "ok"})
	}
}

// sessionDelete clears the board session.
func sessionDelete(c fiber.Ctx) error {
	clearBoardCookie(c)
	return c.JSON(fiber.Map{"status": "ok"})
}

func logout(c fiber.Ctx) error {
	clearBoardCookie(c)
	return c.Redirect().Status(fiber.StatusSeeOther).To("/login")
}
