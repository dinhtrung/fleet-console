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
func boardHandler(board fs.FS) fiber.Handler {
	return func(c fiber.Ctx) error {
		if strings.HasPrefix(c.Path(), "/api/") {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "not found"})
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
