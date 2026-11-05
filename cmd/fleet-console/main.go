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
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	// Embedded so a release binary keeps time-zone data on a bare box.
	_ "time/tzdata"

	"github.com/dinhtrung/fleet-console/internal/act"
	"github.com/dinhtrung/fleet-console/internal/alert"
	"github.com/dinhtrung/fleet-console/internal/config"
	"github.com/dinhtrung/fleet-console/internal/dblock"
	"github.com/dinhtrung/fleet-console/internal/decide"
	"github.com/dinhtrung/fleet-console/internal/decisions"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/git"
	"github.com/dinhtrung/fleet-console/internal/hostcheck"
	"github.com/dinhtrung/fleet-console/internal/mirror"
	"github.com/dinhtrung/fleet-console/internal/progress"
	"github.com/dinhtrung/fleet-console/internal/registry"
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
		var code exitCode
		if errors.As(err, &code) {
			// --notify-report answers with the severity of its own failure:
			// 3 not configured, 1 transport. Its message already went to stderr.
			os.Exit(int(code))
		}
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
		// One tick reads items, change requests, their checks and the notes on each change
		// request; nine open pull requests is a normal afternoon. 400 ran out in the middle
		// of the merge loop and looked like a forge outage.
		budget := &forge.Budget{Max: 1500}
		var source forge.Forge
		switch forge.Kind(project.ForgeKind) {
		case forge.KindGitHub:
			source = forge.NewGitHub(project.ForgeProject, runner, budget)
		case forge.KindGitLab:
			source = forge.NewGitLab(project.ForgeProject, runner, budget)
		default:
			return nil, fmt.Errorf("tick: project %s has unknown forge %q", project.Key, project.ForgeKind)
		}
		spec := mirror.Project{ID: project.ID, Key: project.Key, Forge: source, Rigs: project.Rigs, RepoPath: repoPath}
		if repoPath != "" {
			spec.Git = git.ExecRunner{RepoPath: repoPath}
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

// mirrorTimeouts passes the resolved project_timeout (base + per_item*items,
// capped at ceiling) to both mirror constructions.
func mirrorTimeouts(eff *config.Effective) mirror.TimeoutSettings {
	return mirror.TimeoutSettings{
		Base:    eff.ProjectTimeout.Base,
		PerItem: eff.ProjectTimeout.PerItem,
		Ceiling: eff.ProjectTimeout.Ceiling,
	}
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

// lifecycleFacts is one tick's lifecycle input, taken from the reads the tick
// already performed: no second forge read, no second rig read. Ready is the
// plan's count before the concurrency limit, Fresh is the project's own forge
// read, and Managed is the project's effective `lifecycle:` value.
func lifecycleFacts(rows []store.ProjectRow, reads map[int64]mirror.ProjectResult, plans map[string]decide.Plan, eff *config.Effective) []act.LifecycleProjectFacts {
	facts := make([]act.LifecycleProjectFacts, 0, len(rows))
	for _, project := range rows {
		projectRead := reads[project.ID]
		facts = append(facts, act.LifecycleProjectFacts{
			Key:     project.Key,
			Rigs:    project.Rigs,
			Managed: eff.LifecycleOn(project.Key),
			Fresh:   !projectRead.ForgeStale && projectRead.ForgeError == "",
			Ready:   plans[project.Key].Ready,
			Rows:    projectRead.Rows,
			Seats:   projectRead.Seats,
		})
	}
	return facts
}

// pollCadence is the poll's backoff, or the fixed tick_interval when the
// operator never wrote the rig_lifecycle block. The spec's "an existing
// configuration is unchanged" scenario rests on this: a block-less box must keep
// polling at tick_interval, not at the block's 10m default.
//
// The block's own default idle_poll_max is 10m against a 15s tick_interval, so
// cadence-off and cadence-on-but-default differ by forty times. That gap is why
// Written exists rather than testing Enabled.
func pollCadence(eff *config.Effective) mirror.Cadence {
	if !eff.RigLifecycle.Written {
		// Max == Base makes the loop's wait constant without special-casing the
		// cadence type: Next still runs, and it always returns Base.
		return mirror.Cadence{Base: eff.TickInterval, Max: eff.TickInterval}
	}
	return mirror.Cadence{Base: eff.TickInterval, Max: eff.RigLifecycle.IdlePollMax}
}

// noteContextFor builds what a decision's note needs: the item's link from the
// read the tick already has, the board's deep link, and how many decisions are
// open on that item so the reply line can name the id when it must (design D3).
//
// It reads no forge and no store: the item URL is in the item the mirror read, and
// the open count is a walk over rows already in memory. A decision whose item the
// app cannot name gets an empty ItemURL, which makes Render omit the link rather
// than print a guess.
func noteContextFor(raised act.DecisionRaised, eff *config.Effective, projectRead mirror.ProjectResult) decisions.NoteContext {
	item := decisions.ItemNumber(raised.Item, raised.Text)
	nc := decisions.NoteContext{
		BoardURL: boardURLFrom(eff.Listen) + "/decisions#" + raised.QItemID,
	}
	if item > 0 {
		for _, read := range projectRead.Items {
			if read.Number == item && read.URL != "" {
				nc.ItemURL = read.URL
				break
			}
		}
	}
	// Count the rows waiting on a person for the same item, including this one.
	// The read was taken before the tick, so the row this decision just created is
	// not in it: seed it by identity and skip a pre-tick row carrying the same id,
	// so a row already parked counts once rather than twice. With two open,
	// "fleet: accept" would not say which question it answers.
	open := 0
	if item > 0 {
		open = 1
		for _, row := range projectRead.Rows {
			if row.ID == raised.QItemID {
				continue
			}
			if !rig.WaitsOnHuman(row.State, row.Destination, row.BlockedOn) {
				continue
			}
			if decisions.ItemNumber(0, row.Summary) == item {
				open++
			}
		}
	}
	nc.OpenOnItem = open
	return nc
}

// filterSeatsFor keeps a project's rig seats in the configured order, the same
// selection the mirror's own filter makes, for the re-plan after a launch.
func filterSeatsFor(seats []rig.Seat, rigs []string) []rig.Seat {
	if len(rigs) == 0 {
		return seats
	}
	order := make(map[string]int, len(rigs))
	for index, name := range rigs {
		order[name] = index
	}
	out := make([]rig.Seat, 0, len(seats))
	for _, seat := range seats {
		if _, ok := order[seat.Rig]; ok {
			out = append(out, seat)
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return order[out[a].Rig] < order[out[b].Rig] })
	return out
}

func runTick(ctx context.Context, db *store.Store, out, errOut io.Writer, runner forge.Runner, rigRunner rig.ExecRunner, timeouts mirror.TimeoutSettings) error {
	projects, err := db.Projects(ctx)
	if err != nil {
		return err
	}
	adapter := rig.NewAdapter(rigRunner)
	mirrorer := &mirror.Mirror{Store: db, Rig: adapter, ProjectTimeout: timeouts}
	specs, err := projectSources(ctx, db, runner)
	if err != nil {
		return err
	}
	read, err := mirrorer.Tick(ctx, specs)
	if err != nil {
		return err
	}
	type tickOutput struct {
		At          string                 `json:"at"`
		RigStale    bool                   `json:"rig_stale"`
		RigError    string                 `json:"rig_error,omitempty"`
		RigWarnings map[string][]string    `json:"rig_warnings,omitempty"`
		Projects    map[string]decide.Plan `json:"projects"`
	}
	output := tickOutput{
		At: read.At.UTC().Format(time.RFC3339), RigStale: read.RigStale, RigError: read.RigError,
		RigWarnings: make(map[string][]string, len(projects)),
		Projects:    make(map[string]decide.Plan, len(projects)),
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
		result := read.Projects[project.Key]
		snapshot := mirror.BuildSnapshot(project, result, dispatches, verdicts)
		output.Projects[project.Key] = decide.PlanTick(snapshot, read.At)
		// A ghost rig is a config error the operator must see from the same dry
		// run that plans the work: stderr for a human, the JSON field for a script.
		for _, rigName := range result.RigWarnings {
			fmt.Fprintf(errOut, "rig: %s: rig %s has no running seats\n", project.Key, rigName)
		}
		if len(result.RigWarnings) > 0 {
			output.RigWarnings[project.Key] = result.RigWarnings
		}
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
	notifyReportFlag := flags.Bool("notify-report", false, "render the progress report from the store and send it; no mirror, no dispatch")
	notifyProject := flags.String("notify-project", "fleet-console", "with --notify-report, the project to report on")
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
		pruned, err := registry.Prune(context.Background(), db, eff.Projects)
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
		applied, err := registry.Apply(context.Background(), db, eff.Projects, *syncOnHold, true)
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
		return runTick(ctx, db, stdout, stderr, forgeRunner, rigRunner, mirrorTimeouts(eff))
	}

	if *notifyReportFlag {
		// No mirror, no dispatch, no server: read what the store already holds,
		// render, send. The exit code is the report's result.
		code, err := runNotifyReport(ctx, notifyReportArgs{
			eff: eff, projectKey: *notifyProject, adapter: rig.NewAdapter(rigRunner),
			log: stderr, boardURL: boardURLFrom(eff.Listen),
		})
		if err != nil {
			return err
		}
		if code != 0 {
			return exitCode(code)
		}
		return nil
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
		applied, err := registry.Apply(ctx, db, eff.Projects, false, true)
		if err != nil {
			return err
		}
		for _, project := range applied {
			fmt.Fprintf(stderr, "fleet-console: config: %s %s\n", project.Result, project.Key)
		}
	}

	// The compat check runs at boot, as runtime-compat requires. A failure is
	// visible at /api/compat and in the log; the API still serves.
	//
	// The gate must not be frozen to this one read. On a cold boot the rig daemon
	// can be up to a minute behind the board (the seat launcher starts it, not
	// systemd), and a transient "Daemon not running" is indistinguishable from a
	// permanent incompatibility. Every consumer reads compat through the live
	// state below, and the act loop re-probes on each tick while the gate is
	// closed, so the loop opens the moment the daemon answers.
	compatLive := newCompatState(probe, stderr)
	report := compatLive.current()

	// One Telegram client for both kinds of message (design.md §6). Missing
	// credentials print one line and leave every trigger a no-op; they are a
	// configuration, not an error.
	notify := newNotifier(db, eff, stderr, boardURLFrom(eff.Listen))
	digest := &digestGate{interval: eff.NotifyInterval}
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
	if apply && gateErr == nil {
		// D6: boot reconciles a dispatch that was written before its queue row.
		if err := adoptPendingDispatches(ctx, db, rig.NewAdapter(rigRunner), stderr); err != nil {
			fmt.Fprintf(stderr, "fleet-console: adopt: %v\n", err)
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

	// The lifecycle's memory is the running process: a quiet window is not
	// persisted, so a restart simply gives every rig a fresh idle_after
	// (design D6). With the block absent the coordinator decides nothing.
	lifecycle := act.NewLifecycle(eff.RigLifecycle, db, rig.NewAdapter(rigRunner), func(format string, args ...any) {
		fmt.Fprintf(stderr, "%s\n", fmt.Sprintf(format, args...))
	})
	go (&mirror.Mirror{
		Store: db, Rig: rig.NewAdapter(rigRunner),
		ProjectTimeout: mirrorTimeouts(eff),
		Logf: func(format string, args ...any) {
			fmt.Fprintf(stderr, "mirror: %s\n", fmt.Sprintf(format, args...))
		},
	}).Loop(
		ctx,
		sources,
		pollCadence(eff), jitter,
		func(read mirror.TickResult) bool {
			// While the gate is closed, re-probe: a boot-time refusal is often just
			// the daemon not being up yet, and compat must not be the one read that
			// stays frozen for the life of the process. current() logs the reopen
			// exactly once; a genuinely incompatible rig keeps the gate closed.
			if gateErr != nil {
				gate = act.NewGate(compatLive.current())
				gateErr = gate.Check()
			}
			if apply && gateErr == nil {
				if err := adoptPendingDispatches(ctx, db, rig.NewAdapter(rigRunner), stderr); err != nil {
					fmt.Fprintf(stderr, "adopt: %v\n", err)
				}
			}
			rows, err := db.Projects(ctx)
			if err != nil {
				// A failed store read is as blind as a failed forge read.
				return true
			}
			for _, source := range specs {
				specByKey[source.Key] = source
			}
			// The plans are built for every project first, so the lifecycle sees
			// the whole tick's demand before it decides anything: a start for one
			// rig must not depend on the loop order (design D1).
			plans := make(map[string]decide.Plan, len(rows))
			reads := make(map[int64]mirror.ProjectResult, len(rows))
			// The progress report reads the dispatch rows the plan was built from,
			// so the same read serves both.
			dispatchRows := make(map[string][]store.DispatchRow, len(rows))
			for _, project := range rows {
				projectRead := read.Projects[project.Key]
				reads[project.ID] = projectRead
				// D4: reconcile the app's dispatch rows against the rig before
				// planning. A reconciled item must not be dispatched again in the
				// same tick.
				suppress := map[int]bool{}
				if apply && gateErr == nil {
					reconciled, err := act.Reconcile(ctx, db, project, projectRead.Rows, projectRead.CRs, read.At)
					if err != nil {
						fmt.Fprintf(stderr, "reconcile: %s: %v\n", project.Key, err)
					} else if len(reconciled.Changed) > 0 {
						fmt.Fprintf(stderr, "reconcile: %s ended=%d\n", project.Key, len(reconciled.Changed))
						for _, item := range reconciled.Changed {
							suppress[item] = true
						}
					}
				}
				dispatches, err1 := db.DispatchesForProject(ctx, project.ID)
				verdicts, err2 := db.VerdictsForProject(ctx, project.ID)
				if err1 != nil || err2 != nil {
					continue
				}
				snapshot := mirror.BuildSnapshot(project, projectRead, dispatches, verdicts)
				for item := range suppress {
					snapshot.Dispatched = append(snapshot.Dispatched, item)
				}
				plan := decide.PlanTick(snapshot, read.At)
				plans[project.Key] = plan
				dispatchRows[project.Key] = dispatches
				fmt.Fprintf(stderr, "mirror: %s status=%s intents=%d\n", project.Key, plan.Status, len(plan.Intents))
			}
			if !apply || gateErr != nil {
				// A closed gate and a dry run both skip the act phase, but neither
				// makes the fleet busy: the cadence describes what the read found,
				// and that verdict was already computed. Backing off here would
				// freeze the poll for an act:false box, which is exactly the
				// behaviour the spec forbids.
				busy, _ := mirror.Busy(read)
				return busy
			}
			// Start before any dispatch write, so a rig is up before its worktree
			// and its row exist (design D5). The facts come from the reads above:
			// a project's fresh flag, its plan's Ready, and the rows and seats the
			// tick already has, with no extra forge or rig call.
			lifecycleActions := lifecycle.Decide(lifecycleFacts(rows, reads, plans, eff), !read.RigStale, read.At)
			lifecycleFailed := lifecycle.Start(ctx, lifecycleActions)
			// The rig's roster and queue were read before the launch, so a rig that
			// just came up would plan against a stale fleet and reach the next tick
			// with a row no running seat ever sees. Re-read the roster once and
			// re-plan against it, only when a launch happened (spec: "the row is
			// created for a seat the launch started").
			if len(lifecycleActions) > 0 {
				if fresh, seatsErr := rig.NewAdapter(rigRunner).Seats(ctx); seatsErr == nil {
					for _, project := range rows {
						projectRead := reads[project.ID]
						projectRead.Seats = filterSeatsFor(fresh, project.Rigs)
						reads[project.ID] = projectRead
						dispatches, err := db.DispatchesForProject(ctx, project.ID)
						if err != nil {
							continue
						}
						verdicts, err := db.VerdictsForProject(ctx, project.ID)
						if err != nil {
							continue
						}
						snapshot := mirror.BuildSnapshot(project, projectRead, dispatches, verdicts)
						plans[project.Key] = decide.PlanTick(snapshot, read.At)
					}
				}
			}
			for _, project := range rows {
				plan := plans[project.Key]
				projectRead := reads[project.ID]
				source, ok := specByKey[project.Key]
				if !ok || source.Forge == nil {
					continue
				}
				gitRunner := act.ExecGit{RepoPath: source.RepoPath, DefaultBranch: project.DefaultBranch}
				projectKey := project.Key
				report, err := act.RunTick(ctx, act.TickInput{
					Store: db, Project: project, Forge: source.Forge,
					Rig: rig.NewAdapter(rigRunner), Git: gitRunner, Gate: gate, Apply: true,
					Items: projectRead.Items, CRs: projectRead.CRs, Rows: projectRead.Rows,
					Plan: plan, WorktreeRoot: worktreeRoot(eff, project, source),
					LifecycleFailed: lifecycleFailed,
					// A raised decision reaches the phone from here: the tick
					// knows the row exists, and the notifier owns the transport.
					// The note goes on the item first when decision_notes is on, so
					// the escalation's reply line points at a thread that already
					// carries the question.
					OnDecision: func(raised act.DecisionRaised) {
						noteCtx := noteContextFor(raised, eff, projectRead)
						notify.noteDecision(ctx, raised, noteCtx, source.Forge)
						notify.escalate(ctx, projectKey, raised, noteCtx)
					},
				})
				if err != nil {
					fmt.Fprintf(stderr, "act: %s: %v\n", project.Key, err)
					continue
				}
				fmt.Fprintf(stderr, "act: %s dispatches=%d verdicts=%d merges=%d skipped=%d\n",
					project.Key, len(report.Dispatches), len(report.Verdicts), len(report.Merges), len(report.Skipped))
				// A skip is the only place a dispatch failure is visible: name each one, because
				// "skipped=2" over one project tells the operator nothing about which write failed.
				for _, why := range report.Skipped {
					fmt.Fprintf(stderr, "act-skip: %s: %s\n", project.Key, why)
				}
				// A merge is the only "code done" moment (design.md §4.1), so
				// it is the only trigger that reports without being asked, and
				// it builds the snapshot after the merge rather than before.
				// A failed send never fails the tick and never blocks a merge.
				if len(report.Merges) > 0 || digest.allow(read.At) {
					seats, seatsErr := rig.NewAdapter(rigRunner).Seats(ctx)
					snapshot := progress.Build(read, dispatchRows[project.Key], project, eff, progress.Roster{
						Seats: seats, Err: seatsErr, Plan: &plan,
					})
					result, sendErr := notify.sendProgress(ctx, snapshot, read.At)
					switch {
					case sendErr != nil && !errors.Is(sendErr, alert.ErrNotConfigured):
						fmt.Fprintf(stderr, "notify: %s: %v\n", project.Key, sendErr)
					case result.Failed:
						fmt.Fprintf(stderr, "notify: %s: send failed: %s\n", project.Key, result.Reason)
					case result.Sent:
						fmt.Fprintf(stderr, "notify: %s: sent message %s\n", project.Key, result.MessageID)
					}
				}
			}
			// The stop runs after the dispatch phase, so a tick that merges the
			// last item and finds the window elapsed stops in the same tick (3.3).
			lifecycle.Stop(ctx, lifecycleActions)
			// D7: the verdict comes from the read this tick already did. A failed
			// dispatch or merge does not make the fleet busy; only the read does,
			// so the cadence stays a property of what the app can see.
			busy, _ := mirror.Busy(read)
			return busy
		},
	)

	app, err := web.New(web.Config{
		Token:      token,
		ConfigPath: eff.ConfigPath,
		Compat:     func(context.Context) rig.Report { return compatLive.current() },
		Store:      db,
		Rig:        rig.NewAdapter(rigRunner),
		// A board verdict is one comment write, then the act tick merges on the
		// facts. Budget 50 is a per-call ceiling, not a tick's 1500.
		ForgeFor: func(_ context.Context, project store.ProjectRow) (forge.Forge, error) {
			switch forge.Kind(project.ForgeKind) {
			case forge.KindGitHub:
				return forge.NewGitHub(project.ForgeProject, forgeRunner, &forge.Budget{Max: 50}), nil
			case forge.KindGitLab:
				return forge.NewGitLab(project.ForgeProject, forgeRunner, &forge.Budget{Max: 50}), nil
			default:
				return nil, fmt.Errorf("verdict: project %s has unknown forge %q", project.Key, project.ForgeKind)
			}
		},
		// The setup chain runs `mkdir`, `git clone` and the rig CLI. Only an allowlisted
		// step builds a command, and no step builds a login: the operator's credential
		// never passes through here.
		SetupExec: func(ctx context.Context, name string, args ...string) (string, error) {
			cmd := exec.CommandContext(ctx, name, args...)
			cmd.WaitDelay = 200 * time.Millisecond
			output, err := cmd.CombinedOutput()
			return string(output), err
		},
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

// compatState owns the current compat report. The probe is re-run while the act
// loop is blocked, and every reader — the tick's gate and the board's
// /api/compat — goes through current(), so neither shows a boot-time failure
// after the rig has answered. The reopen is logged once per transition, so the
// journal records recovery without a line per tick.
type compatState struct {
	probe *rig.Probe
	logf  func(format string, args ...any)

	mu      sync.Mutex
	report  rig.Report
	blocked bool
}

// newCompatState runs the first probe and reports it, matching the boot line the
// service has always printed.
func newCompatState(probe *rig.Probe, stderr io.Writer) *compatState {
	status := &compatState{probe: probe}
	if stderr == nil {
		stderr = io.Discard
	}
	status.logf = func(format string, args ...any) {
		fmt.Fprintf(stderr, "fleet-console: "+format+"\n", args...)
	}
	report := probe.Run(context.Background())
	status.report = report
	status.blocked = report.Blocked()
	if !report.OK {
		status.logf("compat: %s", report.Error)
	} else {
		status.logf("compat: rig %s at or above floor %s", report.Version, report.Floor)
	}
	return status
}

// current returns the live report, re-probing first while the gate is closed so
// a transient boot failure clears without a restart. The exchange rate is one
// probe per call while blocked — at most one per tick from the act loop, plus
// one per board read — and zero once the gate is open.
func (s *compatState) current() rig.Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.blocked {
		report := s.probe.Run(context.Background())
		if !report.Blocked() {
			s.blocked = false
			s.logf("compat: rig %s at or above floor %s; act loop open", report.Version, report.Floor)
		}
		s.report = report
	}
	return s.report
}

// adoptPendingDispatches resolves two-phase dispatches for every project. It is
// called at boot and on each act tick, under the one-writer lock.
func adoptPendingDispatches(ctx context.Context, db *store.Store, adapter *rig.Adapter, out io.Writer) error {
	projects, err := db.Projects(ctx)
	if err != nil {
		return err
	}
	for _, project := range projects {
		result, err := act.AdoptPending(ctx, db, adapter, project, "")
		if err != nil {
			return fmt.Errorf("project %s: %w", project.Key, err)
		}
		if len(result.Adopted) > 0 || len(result.Abandoned) > 0 || len(result.Decisions) > 0 {
			fmt.Fprintf(out, "adopt: %s adopted=%d abandoned=%d decisions=%d\n",
				project.Key, len(result.Adopted), len(result.Abandoned), len(result.Decisions))
		}
	}
	return nil
}
