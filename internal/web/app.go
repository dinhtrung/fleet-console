// Package web is the Fiber v3 HTTP surface: health, compat, bearer auth,
// Idempotency-Key replay and graceful shutdown. Handlers stay thin; the
// mirror, decide and act loops live outside this package.
package web

import (
	"context"
	"errors"

	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/gofiber/fiber/v3"
)

// Config wires the app. Token is required; the server refuses to start without
// it rather than accepting an empty bearer.
type Config struct {
	Token  string
	Compat func(ctx context.Context) rig.Report
}

// New builds the HTTP app. /api/health is open; every other /api route needs
// the bearer token.
func New(cfg Config) (*fiber.App, error) {
	if cfg.Token == "" {
		return nil, errors.New("web: bearer token is empty")
	}
	compat := cfg.Compat
	if compat == nil {
		compat = func(context.Context) rig.Report {
			return rig.Report{Floor: rig.Floor, Error: "compat probe not configured"}
		}
	}

	app := fiber.New(fiber.Config{
		AppName: "fleet-console",
	})
	app.Use(idempotency())
	app.Get("/api/health", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})
	api := app.Group("/api", bearerAuth(cfg.Token))
	api.Get("/compat", func(c fiber.Ctx) error {
		return c.JSON(compat(c.Context()))
	})
	return app, nil
}
