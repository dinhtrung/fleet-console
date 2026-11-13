// Package web is the Fiber v3 HTTP surface: health, compat, bearer auth,
// Idempotency-Key replay and graceful shutdown. Handlers stay thin; the
// mirror, decide and act loops live outside this package.
package web

import (
	"context"
	"errors"
	"time"

	"github.com/dinhtrung/fleet-console/internal/configfile"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/mirror"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
	"github.com/dinhtrung/fleet-console/internal/webui"
	"github.com/gofiber/fiber/v3"
)

// Config wires the app. Token is required; the server refuses to start without
// it rather than accepting an empty bearer.
type Config struct {
	Token      string
	ConfigPath string
	Compat     func(ctx context.Context) rig.Report
	Store      *store.Store
	Rig        *rig.Adapter
	// TickInterval is the mirror's poll cadence. The board judges how old a
	// mirror read is against it: quiet past two ticks, stalled past five
	// (#394). Zero takes the config default.
	TickInterval time.Duration
	// MaxWait is the longest the poll may legitimately sleep between reads —
	// pollCadence(eff).Max, the lifecycle block's idle_poll_max when it is
	// written. The board's quiet/stalled floors are raised to it, so a healthy
	// idle box is not painted stalled (#408). Zero means "no backoff": the
	// floors fall back to the plain 2x/5x of TickInterval.
	MaxWait time.Duration
	// ForgeFor is the write path a board verdict needs. Nil answers 503 on the
	// verdict route only; every read still serves.
	ForgeFor func(ctx context.Context, project store.ProjectRow) (forge.Forge, error)
	// WriteProject is the config writer, defaulting to configfile.UpsertProject.
	// A test installs a wrapper here to hand-edit the file between the request's
	// read and its write, which is the stale-read case.
	WriteProject func(path string, in configfile.Project, expected *[32]byte) (configfile.Outcome, error)
	// UpdateProject is the config editor the edit route uses, defaulting to
	// configfile.UpdateProject — an edit replaces its entry in place; routing
	// it through UpsertProject would insert a duplicate key. A test installs
	// the same wrapper WriteProject takes, for the stale-read case.
	UpdateProject func(path string, in configfile.Project, expected *[32]byte) (configfile.Outcome, error)
	// RemoveProject is the config-file remover, defaulting to
	// configfile.RemoveProject. The delete's failure test installs a wrapper
	// that refuses the file step after the pause, which is design D1's
	// half-finished state, without permissions games.
	RemoveProject func(path, key string, expected *[32]byte) (configfile.Outcome, error)
	// WriteSettings is the settings writer, defaulting to configfile.UpdateSettings.
	// A test installs a wrapper here to hand-edit the file between the request's
	// read and its write, which is the stale-read case.
	WriteSettings func(path string, values map[string]string, expected *[32]byte) (configfile.Outcome, error)
	// SetupExec runs one setup step's external command and returns its combined
	// output. Nil means the setup endpoint answers 500: a test supplies a fake,
	// the binary supplies the real runner.
	SetupExec func(ctx context.Context, name string, args ...string) (string, error)
	// Ready is the readiness surface's inputs (#393): its tick interval and the
	// cached queue read. Nil answers readiness with the store facts alone; a
	// process that wires it passes /api/ready every fact the design names.
	Ready *Readiness
	// PreflightForge is the forge_write check's CLI edge. Nil means the check's
	// own exec default, which runs the resolved gh/glab binary.
	PreflightForge func(ctx context.Context, binary string, args ...string) (stdout, stderr string, err error)
	// WebhookSecret is the shared secret a forge signs deliveries with. Empty leaves
	// the receiver off: the route answers 404 and reads no body (design D3).
	WebhookSecret string
	// Waker is the mirror loop's wake channel. Nil means no loop is running in this
	// process (the one-shot commands); a verified delivery still answers 202.
	Waker *mirror.Waker
}

// New builds the HTTP app. /api/health is open; every other /api route needs
// the bearer token.
func New(cfg Config) (*fiber.App, error) {
	if cfg.Token == "" {
		return nil, errors.New("web: bearer token is empty")
	}
	// ConfigPath is optional: a box with no config file still serves its board,
	// and only the write route needs a file to write to. It answers 500 there.
	compat := cfg.Compat
	if compat == nil {
		compat = func(context.Context) rig.Report {
			return rig.Report{Floor: rig.Floor, Status: rig.StatusBlocked, Error: "compat probe not configured"}
		}
	}

	app := fiber.New(fiber.Config{
		AppName:      "fleet-console",
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 10 * time.Minute,
		IdleTimeout:  60 * time.Second,
	})
	hub := NewHub()
	fleet := &Fleet{Store: cfg.Store, Rig: cfg.Rig, Hub: hub, Compat: compat, ForgeFor: cfg.ForgeFor, Waker: cfg.Waker, Tick: cfg.TickInterval, MaxWait: cfg.MaxWait}

	app.Use(idempotency())
	// Liveness is a constant on purpose (design D1): this route answers "the
	// process is up and serving", and a restarter wired to it must never see a
	// slow forge read as a crash. It reads no dependency — not the store, not the
	// rig, not the forge. Readiness is /api/ready.
	app.Get("/api/health", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})
	// Readiness sits outside the bearer group like health: a supervisor or a curl
	// probe has no board token. It is a read-only view of facts the operator's own
	// config already names (project keys, ages), on the same listener.
	app.Get("/api/ready", readyHandler(cfg.Store, cfg.Ready))
	app.Get("/login", loginPage)
	app.Post("/login", loginSubmit(cfg.Token))
	app.Post("/logout", logout)
	app.Post("/api/session", sessionCreate(cfg.Token))
	app.Delete("/api/session", sessionDelete)
	// The forge receiver sits on the app, not the bearer group: a forge cannot
	// present the board's token, so the HMAC is the whole credential. Registering it
	// here also means bearerAuth never runs for it.
	app.Post("/api/hooks/forge", ForgeHook{Secret: cfg.WebhookSecret, Waker: cfg.Waker}.Handle)

	api := app.Group("/api", bearerAuth(cfg.Token))
	api.Get("/compat", func(c fiber.Ctx) error {
		return c.JSON(compat(c.Context()))
	})
	api.Get("/board", fleet.Board)
	api.Get("/items/:number", fleet.Item)
	// The operator's approve/reject: the same note shape a reviewer seat posts,
	// so one gate reads both and the newest verdict wins.
	api.Post("/items/:number/verdict", fleet.ItemVerdict)
	api.Get("/decisions", fleet.Decisions)
	api.Post("/decisions/:id/answer", fleet.AnswerDecision)
	api.Get("/seats", fleet.Seats)
	api.Get("/seats/:session/tail", fleet.SeatTail)
	api.Get("/events", fleet.Events)
	api.Get("/events/:id", fleet.Event)
	api.Get("/stream", hub.Stream)
	// Projects routes
	RegisterProjectsRoutes(api, cfg, cfg.ConfigPath)
	RegisterSettingsRoutes(api, cfg, cfg.ConfigPath)
	RegisterCultureRoutes(api, cfg, cfg.ConfigPath)
	RegisterPreflightRoutes(api, cfg, cfg.ConfigPath)
	if board, ok := webui.FS(); ok {
		app.Use(boardHandler(board, cfg.Token))
	}
	return app, nil
}
