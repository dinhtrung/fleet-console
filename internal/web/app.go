// Package web is the Fiber v3 HTTP surface: health, compat, bearer auth,
// Idempotency-Key replay and graceful shutdown. Handlers stay thin; the
// mirror, decide and act loops live outside this package.
package web

import (
	"context"
	"errors"
	"time"

	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
	"github.com/dinhtrung/fleet-console/internal/webui"
	"github.com/gofiber/fiber/v3"
)

// Config wires the app. Token is required; the server refuses to start without
// it rather than accepting an empty bearer.
type Config struct {
	Token  string
	Compat func(ctx context.Context) rig.Report
	Store  *store.Store
	Rig    *rig.Adapter
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
			return rig.Report{Floor: rig.Floor, Status: rig.StatusBlocked, Error: "compat probe not configured"}
		}
	}

	app := fiber.New(fiber.Config{
		AppName:     "fleet-console",
		ReadTimeout: 15 * time.Second,
		// The event stream is long-lived, so the write timeout is long rather
		// than absent; EventSource reconnects when a stream hits it.
		WriteTimeout: 10 * time.Minute,
		IdleTimeout:  60 * time.Second,
	})
	hub := NewHub()
	fleet := &Fleet{Store: cfg.Store, Rig: cfg.Rig, Hub: hub, Compat: compat}

	app.Use(idempotency())
	app.Get("/api/health", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})
	// A board session is minted only after the token is presented. The board page
	// itself is public; it must sign in to call /api.
	app.Get("/login", loginPage)
	app.Post("/login", loginSubmit(cfg.Token))
	app.Post("/logout", logout)
	app.Post("/api/session", sessionCreate(cfg.Token))
	app.Delete("/api/session", sessionDelete)
	api := app.Group("/api", bearerAuth(cfg.Token))
	api.Get("/compat", func(c fiber.Ctx) error {
		return c.JSON(compat(c.Context()))
	})
	api.Get("/board", fleet.Board)
	api.Get("/items/:number", fleet.Item)
	api.Get("/decisions", fleet.Decisions)
	api.Post("/decisions/:id/answer", fleet.AnswerDecision)
	api.Get("/seats", fleet.Seats)
	api.Get("/seats/:session/tail", fleet.SeatTail)
	api.Get("/events", fleet.Events)
	api.Get("/stream", hub.Stream)
	if board, ok := webui.FS(); ok {
		app.Use(boardHandler(board))
	}
	return app, nil
}
