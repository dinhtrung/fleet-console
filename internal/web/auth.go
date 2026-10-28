package web

import (
	"crypto/subtle"
	"strings"

	"github.com/gofiber/fiber/v3"
)

const bearerPrefix = "Bearer "

// bearerAuth rejects anything without the exact configured bearer token.
func bearerAuth(token string) fiber.Handler {
	want := []byte(token)
	return func(c fiber.Ctx) error {
		header := c.Get(fiber.HeaderAuthorization)
		if len(header) <= len(bearerPrefix) || !strings.EqualFold(header[:len(bearerPrefix)], bearerPrefix) {
			return unauthorized(c)
		}
		got := []byte(header[len(bearerPrefix):])
		if subtle.ConstantTimeCompare(got, want) != 1 {
			return unauthorized(c)
		}
		return c.Next()
	}
}

func unauthorized(c fiber.Ctx) error {
	return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
}
