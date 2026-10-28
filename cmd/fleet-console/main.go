// Command fleet-console is the control plane: one static binary serving the
// API and, later, the embedded board.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
	"github.com/dinhtrung/fleet-console/internal/web"
	"github.com/gofiber/fiber/v3"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "fleet-console:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("fleet-console", flag.ContinueOnError)
	flags.SetOutput(stderr)
	compat := flags.Bool("compat", false, "probe the rig CLI, print the compat report and exit")
	addr := flags.String("addr", "127.0.0.1:8787", "listen address")
	dbPath := flags.String("db", "fleet-console.db", "SQLite database path")
	if err := flags.Parse(args); err != nil {
		return err
	}

	probe := rig.NewProbe(rig.ExecRunner{}, log.New(stderr, "", 0).Printf)

	if *compat {
		report := probe.Run(context.Background())
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return err
		}
		if !report.OK {
			return fmt.Errorf("compat probe failed: %s", report.Error)
		}
		return nil
	}

	token := os.Getenv("FLEET_CONSOLE_TOKEN")
	if token == "" {
		return errors.New("FLEET_CONSOLE_TOKEN is required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := store.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	// The compat check runs at boot, as runtime-compat requires. A failure is
	// visible at /api/compat and in the log; the API still serves.
	report := probe.Run(ctx)
	if !report.OK {
		fmt.Fprintf(stderr, "fleet-console: compat: %s\n", report.Error)
	} else {
		fmt.Fprintf(stderr, "fleet-console: compat: rig %s at or above floor %s\n", report.Version, report.Floor)
	}

	app, err := web.New(web.Config{
		Token:  token,
		Compat: func(context.Context) rig.Report { return report },
	})
	if err != nil {
		return err
	}

	listenErr := make(chan error, 1)
	go func() {
		listenErr <- app.Listen(*addr, fiber.ListenConfig{DisableStartupMessage: true})
	}()
	fmt.Fprintf(stderr, "fleet-console: listening on %s\n", *addr)

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return app.ShutdownWithContext(shutdownCtx)
	case err := <-listenErr:
		return err
	}
}
