// Command fleet-console is the control plane: one static binary serving the
// API and the embedded board.
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
	"strings"
	"syscall"
	"time"

	// Embedded so a release binary keeps time-zone data on a bare box.
	_ "time/tzdata"

	"github.com/dinhtrung/fleet-console/internal/act"
	"github.com/dinhtrung/fleet-console/internal/decide"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/git"
	"github.com/dinhtrung/fleet-console/internal/hostcheck"
	"github.com/dinhtrung/fleet-console/internal/mirror"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
	"github.com/dinhtrung/fleet-console/internal/web"
	"github.com/gofiber/fiber/v3"
)

// Set by the release build.
var (
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
)

type buildInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
	RigFloor  string `json:"rig_floor"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "fleet-console:", err)
		os.Exit(1)
	}
}

// runTick reads the forge/rig/git, writes only the mirror caches, and prints the
// decide plan. It performs no forge, rig or git writes.
func projectSources(ctx context.Context, db *store.Store) ([]mirror.Project, error) {
	projects, err := db.Projects(ctx)
	if err != nil {
		return nil, err
	}
	specs := make([]mirror.Project, 0, len(projects))
	for _, project := range projects {
		repoPath, err := db.RepoPath(ctx, project.ID)
		if err != nil {
			return nil, err
		}
		budget := &forge.Budget{Max: 400}
		var source forge.Forge
		switch forge.Kind(project.ForgeKind) {
		case forge.KindGitHub:
			source = forge.NewGitHub(project.ForgeProject, forge.CLIRunner{}, budget)
		case forge.KindGitLab:
			source = forge.NewGitLab(project.ForgeProject, forge.CLIRunner{}, budget)
		default:
			return nil, fmt.Errorf("tick: project %s has unknown forge %q", project.Key, project.ForgeKind)
		}
		spec := mirror.Project{ID: project.ID, Key: project.Key, Forge: source, Rig: project.Rig}
		if repoPath != "" {
			spec.Git = git.ExecRunner{RepoPath: repoPath}
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

func runTick(ctx context.Context, db *store.Store, out io.Writer) error {
	projects, err := db.Projects(ctx)
	if err != nil {
		return err
	}
	adapter := rig.NewAdapter(rig.ExecRunner{})
	mirrorer := &mirror.Mirror{Store: db, Rig: adapter}
	specs, err := projectSources(ctx, db)
	if err != nil {
		return err
	}
	read, err := mirrorer.Tick(ctx, specs)
	if err != nil {
		return err
	}
	type tickOutput struct {
		At       string                 `json:"at"`
		RigStale bool                   `json:"rig_stale"`
		RigError string                 `json:"rig_error,omitempty"`
		Projects map[string]decide.Plan `json:"projects"`
	}
	output := tickOutput{
		At: read.At.UTC().Format(time.RFC3339), RigStale: read.RigStale, RigError: read.RigError,
		Projects: make(map[string]decide.Plan, len(projects)),
	}
	for _, project := range projects {
		dispatches, err := db.DispatchesForProject(ctx, project.ID)
		if err != nil {
			return err
		}
		verdicts, err := db.VerdictsForProject(ctx, project.ID)
		if err != nil {
			return err
		}
		snapshot := mirror.BuildSnapshot(project, read.Projects[project.Key], dispatches, verdicts)
		output.Projects[project.Key] = decide.PlanTick(snapshot, read.At)
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(output)
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("fleet-console", flag.ContinueOnError)
	flags.SetOutput(stderr)
	showVersion := flags.Bool("version", false, "print build information and exit")
	asJSON := flags.Bool("json", false, "with --version, print JSON")
	compat := flags.Bool("compat", false, "probe the rig CLI, print the compat report and exit")
	migrate := flags.Bool("migrate", false, "open the database, apply migrations and exit")
	tick := flags.Bool("tick", false, "run one mirror + decide pass in dry-run and print the plan")
	addr := flags.String("addr", "127.0.0.1:8787", "listen address")
	dbPath := flags.String("db", "fleet-console.db", "SQLite database path")
	if err := flags.Parse(args); err != nil {
		return err
	}

	if *asJSON && !*showVersion {
		return errors.New("--json requires --version")
	}
	if *showVersion {
		info := buildInfo{Version: version, Commit: commit, BuildTime: buildTime, RigFloor: rig.Floor}
		if *asJSON {
			encoder := json.NewEncoder(stdout)
			encoder.SetIndent("", "  ")
			return encoder.Encode(info)
		}
		fmt.Fprintf(stdout, "fleet-console %s (%s) built %s; rig floor %s\n",
			info.Version, info.Commit, info.BuildTime, info.RigFloor)
		return nil
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

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if *migrate {
		db, err := store.Open(ctx, *dbPath)
		if err != nil {
			return err
		}
		defer db.Close()
		fmt.Fprintln(stdout, "database migrated")
		return nil
	}

	if *tick {
		db, err := store.Open(ctx, *dbPath)
		if err != nil {
			return err
		}
		defer db.Close()
		return runTick(ctx, db, stdout)
	}

	token := os.Getenv("FLEET_CONSOLE_TOKEN")
	if token == "" {
		return errors.New("FLEET_CONSOLE_TOKEN is required")
	}

	// Host tools are declared, not bundled. A missing one stops startup and
	// names itself; nothing can be dispatched without it.
	if missing := hostcheck.Missing(os.Getenv("PATH")); len(missing) > 0 {
		return fmt.Errorf("missing host tools on PATH: %s; the act loop stays off", strings.Join(missing, ", "))
	}

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
	// The act loop's first check. A failed probe is a hard refusal with one named
	// reason; the mirror and the board keep serving. The loop itself lands in
	// task 5.x and must call this same gate before it writes.
	if err := act.NewGate(report).Check(); err != nil {
		fmt.Fprintf(stderr, "fleet-console: %v\n", err)
	}

	// The mirror runs in the server: read-only, jittered, and dry-run only for
	// decide until the act loop lands.
	go (&mirror.Mirror{Store: db, Rig: rig.NewAdapter(rig.ExecRunner{})}).Loop(
		ctx,
		func(ctx context.Context) ([]mirror.Project, error) { return projectSources(ctx, db) },
		15*time.Second, 5*time.Second,
		func(read mirror.TickResult) {
			rows, err := db.Projects(ctx)
			if err != nil {
				return
			}
			for _, project := range rows {
				dispatches, err1 := db.DispatchesForProject(ctx, project.ID)
				verdicts, err2 := db.VerdictsForProject(ctx, project.ID)
				if err1 != nil || err2 != nil {
					continue
				}
				plan := decide.PlanTick(mirror.BuildSnapshot(project, read.Projects[project.Key], dispatches, verdicts), read.At)
				fmt.Fprintf(stderr, "mirror: %s status=%s intents=%d\n", project.Key, plan.Status, len(plan.Intents))
			}
		},
	)

	app, err := web.New(web.Config{
		Token:  token,
		Compat: func(context.Context) rig.Report { return report },
		Store:  db,
		Rig:    rig.NewAdapter(rig.ExecRunner{}),
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
