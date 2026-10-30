package web

import (
	"io/fs"
	"mime"
	"path"
	"strings"

	"github.com/gofiber/fiber/v3"
)

// boardHandler serves an embedded board and falls back to index.html for
// client-side routes. Unknown /api paths stay JSON 404s.
//
// The board has no sign-in screen. It is served on a loopback listener to the
// operator's own browser, so a request that arrives over the loopback interface
// is granted the board session here. The token keeps its job for every other
// caller: a non-loopback request gets no cookie and must POST /api/session or
// use the /login form.
func boardHandler(board fs.FS, token string) fiber.Handler {
	return func(c fiber.Ctx) error {
		if strings.HasPrefix(c.Path(), "/api/") {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "not found"})
		}
		if token != "" && !isBoardCookie(c, token) && isLoopback(c.IP()) {
			issueBoardCookie(c, token)
		}
		name := strings.TrimPrefix(path.Clean("/"+c.Path()), "/")
		if name == "" {
			name = "index.html"
		}
		if !fs.ValidPath(name) {
			return c.Status(fiber.StatusNotFound).SendString("not found")
		}
		if _, err := fs.Stat(board, name); err != nil {
			name = "index.html"
		}
		body, err := fs.ReadFile(board, name)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).SendString("board is not embedded in this build")
		}
		if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
			c.Set(fiber.HeaderContentType, contentType)
		}
		return c.Send(body)
	}
}
