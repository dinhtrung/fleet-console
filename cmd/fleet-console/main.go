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
	"path/filepath"
	"strings"
	"syscall"
	"time"

	// Embedded so a release binary keeps time-zone data on a bare box.
	_ "time/tzdata"

	"github.com/dinhtrung/fleet-console/internal/act"
	"github.com/dinhtrung/fleet-console/internal/config"
	"github.com/dinhtrung/fleet-console/internal/dblock"
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
		if errors.Is(err, dblock.ErrHeld) {
			// 3 is distinct from a config or flag error (1) so a supervisor can
			// tell "another instance owns this database" from a broken config.
			os.Exit(3)
		}
		os.Exit(1)
	}
}

// runTick reads the forge/rig/git, writes only the mirror caches, and prints the
// decide plan. It performs no forge, rig or git writes.
func projectSources(ctx context.Context, db *store.Store, runner forge.Runner) ([]mirror.Project, error) {
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
			source = forge.NewGitHub(project.ForgeProject, runner, budget)
		case forge.KindGitLab:
			source = forge.NewGitLab(project.ForgeProject, runner, budget)
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

// worktreeRoot is the configured worktree root for a project, else the repo's
// parent (the same layout the old dispatcher used).
func worktreeRoot(eff *config.Effective, project store.ProjectRow, source mirror.Project) string {
	for _, configured := range eff.Projects {
		if configured.Key == project.Key && configured.Paths.Worktrees != "" {
			return configured.Paths.Worktrees
		}
	}
	if source.RepoPath != "" {
		return filepath.Dir(source.RepoPath)
	}
	return ""
}

func runTick(ctx context.Context, db *store.Store, out io.Writer, runner forge.Runner, rigRunner rig.ExecRunner) error {
	projects, err := db.Projects(ctx)
	if err != nil {
		return err
	}
	adapter := rig.NewAdapter(rigRunner)
	mirrorer := &mirror.Mirror{Store: db, Rig: adapter}
	specs, err := projectSources(ctx, db, runner)
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
	applyFlag := flags.Bool("apply", false, "apply the plan through the act loop in the server (writes worktrees, rows and merges)")
	importState := flags.String("import-dispatch-state", "", "import the old dispatcher state JSON and exit")
	projectKey := flags.String("project-key", "fleet-console", "project key for --import-dispatch-state")
	flags.String("addr", "", "listen address (overrides the config)")
	flags.String("db", "", "SQLite database path (overrides the config)")
	configPath := flags.String("config", "", "config.yaml path (overrides FLEET_CONSOLE_CONFIG)")
	printConfig := flags.Bool("print-config", false, "print the effective config with secrets redacted and exit")
	applyConfig := flags.Bool("apply-config", false, "create or refresh the projects from the config; projects absent from the file are disabled")
	prune := flags.Bool("prune", false, "delete projects absent from the config and exit; refused while a live dispatch exists")
	syncOnHold := flags.Bool("sync-onhold", false, "with --apply-config, copy each project's onhold from the config")
	flags.String("tick-interval", "", "mirror interval (overrides the config)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	explicit := map[string]string{}
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "addr":
			explicit["listen"] = f.Value.String()
		case "db":
			explicit["database"] = f.Value.String()
		case "tick-interval":
			explicit["tick_interval"] = f.Value.String()
		}
	})

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

	eff, err := config.Load(config.Options{FlagPath: *configPath, Flags: explicit, Env: os.Getenv})
	if err != nil {
		return err
	}
	for _, warning := range eff.Warnings {
		fmt.Fprintf(stderr, "fleet-console: config: %s\n", warning)
	}
	if *printConfig {
		printed, err := eff.Print()
		if err != nil {
			return err
		}
		_, err = stdout.Write(printed)
		return err
	}
	rigRunner := rig.ExecRunner{Path: eff.RigBinary}
	forgeRunner := forge.CLIRunner{Binaries: map[string]string{"gh": eff.GhBinary, "glab": eff.GlabBinary}}
	probe := rig.NewProbe(rigRunner, log.New(stderr, "", 0).Printf)

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

	if *prune {
		if eff.ConfigPath == "" {
			return errors.New("--prune requires a config file")
		}
		db, err := store.Open(context.Background(), eff.Database)
		if err != nil {
			return err
		}
		defer db.Close()
		pruned, err := pruneProjects(context.Background(), db, eff.Projects)
		if err != nil {
			return err
		}
		for _, key := range pruned {
			fmt.Fprintf(stdout, "pruned %s\n", key)
		}
		return nil
	}

	if *applyConfig {
		db, err := store.Open(context.Background(), eff.Database)
		if err != nil {
			return err
		}
		defer db.Close()
		applied, err := applyProjects(context.Background(), db, eff.Projects, *syncOnHold, true)
		if err != nil {
			return err
		}
		for _, project := range applied {
			fmt.Fprintf(stdout, "%s %s\n", project.Result, project.Key)
		}
		return nil
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if *migrate {
		db, err := store.Open(ctx, eff.Database)
		if err != nil {
			return err
		}
		defer db.Close()
		fmt.Fprintln(stdout, "database migrated")
		return nil
	}

	if *tick {
		db, err := store.Open(ctx, eff.Database)
		if err != nil {
			return err
		}
		defer db.Close()
		return runTick(ctx, db, stdout, forgeRunner, rigRunner)
	}

	if *importState != "" {
		db, err := store.Open(ctx, eff.Database)
		if err != nil {
			return err
		}
		defer db.Close()
		data, err := os.ReadFile(*importState)
		if err != nil {
			return err
		}
		state, err := store.ParseDispatcherState(data)
		if err != nil {
			return err
		}
		project, err := db.ProjectByKey(ctx, *projectKey)
		if err != nil {
			return err
		}
		if project == nil {
			return fmt.Errorf("import: project %q is not registered; add it first", *projectKey)
		}
		inserted, err := db.ImportDispatches(ctx, project.ID, state)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "imported %d dispatch row(s) into %s\n", inserted, project.Key)
		return nil
	}

	apply := *applyFlag || eff.Act
	if apply {
		// D1: exactly one process ticks a database. Acquire before the probe,
		// token check or any write so a second instance exits before doing work.
		owner, err := dblock.Acquire(eff.Database)
		if err != nil {
			return err
		}
		defer owner.Release()
	}

	token := eff.Token
	if token == "" {
		return errors.New("token is required (FLEET_CONSOLE_TOKEN, token: or token_file:)")
	}

	// Host tools are declared, not bundled. A missing one stops startup and
	// names itself; nothing can be dispatched without it. Configured binary
	// paths satisfy the check even when they are not named on PATH.
	missing := hostcheck.MissingFor(os.Getenv("PATH"), hostcheck.Requirements{
		Rig:   []string{eff.RigBinary},
		Forge: []string{eff.GhBinary, eff.GlabBinary},
	})
	if len(missing) > 0 {
		return fmt.Errorf("missing host tools on PATH: %s; the act loop stays off", strings.Join(missing, ", "))
	}

	db, err := store.Open(ctx, eff.Database)
	if err != nil {
		return err
	}
	defer db.Close()

	if eff.ConfigPath != "" {
		applied, err := applyProjects(ctx, db, eff.Projects, false, true)
		if err != nil {
			return err
		}
		for _, project := range applied {
			fmt.Fprintf(stderr, "fleet-console: config: %s %s\n", project.Result, project.Key)
		}
	}

	// The compat check runs at boot, as runtime-compat requires. A failure is
	// visible at /api/compat and in the log; the API still serves.
	report := probe.Run(ctx)
	if !report.OK {
		fmt.Fprintf(stderr, "fleet-console: compat: %s\n", report.Error)
	} else {
		fmt.Fprintf(stderr, "fleet-console: compat: rig %s at or above floor %s\n", report.Version, report.Floor)
	}
	// The act loop's first check. A failed probe is a hard refusal with one named
	// reason; the mirror and the board keep serving and no dispatch or merge runs.
	gate := act.NewGate(report)
	gateErr := gate.Check()
	if gateErr != nil {
		fmt.Fprintf(stderr, "fleet-console: %v\n", gateErr)
	}
	if apply && gateErr != nil {
		if _, err := db.AppendEvent(ctx, store.Event{
			Actor: "act", Kind: "tick.blocked", Subject: "startup", Detail: gateErr.Error(),
		}); err != nil {
			fmt.Fprintf(stderr, "fleet-console: record blocked act: %v\n", err)
		}
	}

	// The mirror runs in the server: read-only and jittered. With apply, the same
	// tick hands the plan to the act loop after the mirror has read every project.
	jitter := 5 * time.Second
	if eff.TickInterval < jitter {
		jitter = 0
	}
	var specs []mirror.Project
	sources := func(ctx context.Context) ([]mirror.Project, error) {
		var err error
		specs, err = projectSources(ctx, db, forgeRunner)
		return specs, err
	}
	specByKey := map[string]mirror.Project{}
	go (&mirror.Mirror{
		Store: db, Rig: rig.NewAdapter(rigRunner),
		Logf: func(format string, args ...any) {
			fmt.Fprintf(stderr, "mirror: %s\n", fmt.Sprintf(format, args...))
		},
	}).Loop(
		ctx,
		sources,
		eff.TickInterval, jitter,
		func(read mirror.TickResult) {
			rows, err := db.Projects(ctx)
			if err != nil {
				return
			}
			for _, source := range specs {
				specByKey[source.Key] = source
			}
			for _, project := range rows {
				dispatches, err1 := db.DispatchesForProject(ctx, project.ID)
				verdicts, err2 := db.VerdictsForProject(ctx, project.ID)
				if err1 != nil || err2 != nil {
					continue
				}
				projectRead := read.Projects[project.Key]
				plan := decide.PlanTick(mirror.BuildSnapshot(project, projectRead, dispatches, verdicts), read.At)
				fmt.Fprintf(stderr, "mirror: %s status=%s intents=%d\n", project.Key, plan.Status, len(plan.Intents))
				if !apply || gateErr != nil {
					continue
				}
				source, ok := specByKey[project.Key]
				if !ok || source.Forge == nil {
					continue
				}
				gitRunner := act.ExecGit{RepoPath: source.RepoPath, DefaultBranch: project.DefaultBranch}
				report, err := act.RunTick(ctx, act.TickInput{
					Store: db, Project: project, Forge: source.Forge,
					Rig: rig.NewAdapter(rigRunner), Git: gitRunner, Gate: gate, Apply: true,
					Items: projectRead.Items, CRs: projectRead.CRs, Rows: projectRead.Rows,
					Plan: plan, WorktreeRoot: worktreeRoot(eff, project, source),
				})
				if err != nil {
					fmt.Fprintf(stderr, "act: %s: %v\n", project.Key, err)
					continue
				}
				fmt.Fprintf(stderr, "act: %s dispatches=%d verdicts=%d merges=%d skipped=%d\n",
					project.Key, len(report.Dispatches), len(report.Verdicts), len(report.Merges), len(report.Skipped))
			}
		},
	)

	app, err := web.New(web.Config{
		Token:  token,
		Compat: func(context.Context) rig.Report { return report },
		Store:  db,
		Rig:    rig.NewAdapter(rigRunner),
	})
	if err != nil {
		return err
	}

	listenErr := make(chan error, 1)
	go func() {
		listenErr <- app.Listen(eff.Listen, fiber.ListenConfig{DisableStartupMessage: true})
	}()
	fmt.Fprintf(stderr, "fleet-console: listening on %s\n", eff.Listen)

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return app.ShutdownWithContext(shutdownCtx)
	case err := <-listenErr:
		return err
	}
}

type appliedProject struct {
	Key    string
	Result store.UpsertResult
}

// applyProjects refreshes the project registry from config.yaml. Projects absent
// from the file are disabled, never deleted, and an onhold project is not
// resumed without syncOnHold.
func applyProjects(ctx context.Context, db *store.Store, projects []config.Project, syncOnHold, disableAbsent bool) ([]appliedProject, error) {
	applied := make([]appliedProject, 0, len(projects))
	for _, project := range projects {
		name := project.Name
		if name == "" {
			name = project.Key
		}
		labels := []byte("{}")
		if project.Labels != nil {
			encoded, err := json.Marshal(project.Labels)
			if err != nil {
				return applied, fmt.Errorf("config: project %s labels: %w", project.Key, err)
			}
			labels = encoded
		}
		onhold := project.OnHold != nil && *project.OnHold
		result, _, err := db.UpsertProject(ctx, store.UpsertInput{
			Project: store.ProjectInput{
				Key: project.Key, Name: name, ForgeKind: project.Forge, ForgeProject: project.Repo,
				DefaultBranch: project.DefaultBranch, Rig: project.Rig, RigTemplate: project.RigTemplate,
				ConcurrencyCap: project.ConcurrencyCap, Enabled: true, OnHold: onhold, GateCmd: project.GateCmd,
			},
			LabelsJSON: string(labels), RepoKey: "main",
			RepoPath: project.Paths.Main, WorktreeRoot: project.Paths.Worktrees,
			SyncOnHold: syncOnHold,
		})
		if err != nil {
			return applied, err
		}
		applied = append(applied, appliedProject{Key: project.Key, Result: result})
	}
	if disableAbsent {
		keep := make(map[string]bool, len(projects))
		for _, project := range projects {
			keep[project.Key] = true
		}
		rows, err := db.Projects(ctx)
		if err != nil {
			return applied, err
		}
		for _, row := range rows {
			if keep[row.Key] || !row.Enabled {
				continue
			}
			changed, err := db.SetProjectEnabled(ctx, row.ID, false)
			if err != nil {
				return applied, err
			}
			if changed {
				applied = append(applied, appliedProject{Key: row.Key, Result: store.UpsertResult("disabled")})
			}
		}
	}
	return applied, nil
}

// pruneProjects deletes every project absent from the config. It refuses
// before deleting anything when one of them has a live dispatch.
func pruneProjects(ctx context.Context, db *store.Store, projects []config.Project) ([]string, error) {
	keep := make(map[string]bool, len(projects))
	for _, project := range projects {
		keep[project.Key] = true
	}
	rows, err := db.Projects(ctx)
	if err != nil {
		return nil, err
	}
	var absent []store.ProjectRow
	for _, row := range rows {
		if !keep[row.Key] {
			absent = append(absent, row)
		}
	}
	for _, row := range absent {
		id, live, err := db.LiveDispatch(ctx, row.ID)
		if err != nil {
			return nil, err
		}
		if live {
			return nil, fmt.Errorf("prune: project %s has a live dispatch %d", row.Key, id)
		}
	}
	pruned := make([]string, 0, len(absent))
	for _, row := range absent {
		if err := db.DeleteProject(ctx, row.ID); err != nil {
			return pruned, err
		}
		pruned = append(pruned, row.Key)
	}
	return pruned, nil
}
