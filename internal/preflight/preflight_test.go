package preflight

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/config"
	"github.com/dinhtrung/fleet-console/internal/rig"
)

// The tests run the real git binary against fixtures on disk for git_url and
// base_dir: the promise is "the command's own last line", and a fake git could
// not keep it. Everything a check cannot reach without a host is injected
// through Deps.

func gitAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

// bareRepo creates a bare repository with one branch and returns its path.
func bareRepo(t *testing.T, branch string) string {
	t.Helper()
	gitAvailable(t)
	dir := filepath.Join(t.TempDir(), "origin.git")
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = filepath.Dir(dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "--bare", "--initial-branch="+branch, dir)
	// A branch needs a commit to exist in the bare repository's ref list.
	work := filepath.Join(t.TempDir(), "work")
	run("init", "--initial-branch="+branch, work)
	cmd := exec.Command("git", "-C", work, "commit", "--allow-empty", "-m", "first")
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, out)
	}
	push := exec.Command("git", "-C", work, "push", dir, branch)
	if out, err := push.CombinedOutput(); err != nil {
		t.Fatalf("git push: %v: %s", err, out)
	}
	return dir
}

// checkoutWithOrigin creates a small checkout whose origin is the given URL.
func checkoutWithOrigin(t *testing.T, origin string) string {
	t.Helper()
	gitAvailable(t)
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", dir},
		{"-C", dir, "remote", "add", "origin", origin},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeSpec writes a rendered rig spec with the given builder count and runtime
// under <home>/.openrig/specs/rigs/<name>/rig.yaml.
func writeSpec(t *testing.T, home, name string, builders int, runtime string, reviewerRuntime string) {
	t.Helper()
	dir := specDir(home, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("version: \"0.2\"\nname: " + name + "\nsummary: fixture\nculture_file: CULTURE.md\npods:\n  - id: dev\n    label: Dev\n    members:\n")
	for i := 1; i <= builders; i++ {
		b.WriteString("      - id: build-")
		b.WriteString(string(rune('0' + i)))
		b.WriteString("\n        agent_ref: local:./agents/development/implementer\n        runtime: " + runtime +
			"\n        model: m\n        profile: default\n        env:\n          KEY: \"v\"\n        cwd: /tmp/wt\n")
	}
	if reviewerRuntime == "" {
		reviewerRuntime = runtime
	}
	b.WriteString("      - id: review\n        agent_ref: local:./agents/development/qa\n        runtime: " + reviewerRuntime +
		"\n        model: m\n        profile: default\n        env:\n          KEY: \"v\"\n        cwd: /tmp/repo\n")
	if err := os.WriteFile(filepath.Join(dir, "rig.yaml"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

// seatsFor is a Seats edge that answers from a fixed list.
func seatsFor(seats ...rig.Seat) func(ctx context.Context, name string) ([]rig.Seat, error) {
	return func(ctx context.Context, name string) ([]rig.Seat, error) { return seats, nil }
}

func seat(session, runtime string) rig.Seat {
	return rig.Seat{Session: session, Rig: "r", Runtime: runtime}
}

func noEnv(string) string { return "" }

func depsFor(home string) Deps {
	return Deps{Home: home, Env: noEnv, Timeout: 5 * time.Second}
}

func checkByID(t *testing.T, report Report, id string) Check {
	t.Helper()
	for _, check := range report.Checks {
		if check.ID == id {
			return check
		}
	}
	t.Fatalf("report has no %s check; got %d checks", id, len(report.Checks))
	return Check{}
}

// TestReportAlwaysHasFiveChecks pins the shape the surfaces render: exactly the
// five checks, in order, whatever the host answers.
func TestReportAlwaysHasFiveChecks(t *testing.T) {
	home := t.TempDir()
	repo := bareRepo(t, "main")
	p := Project{Key: "p", Rig: "r", CloneURL: repo, DefaultBranch: "main", MainPath: filepath.Join(home, "base")}
	report := RunWith(context.Background(), p, config.Effective{}, Deps{
		Home: home, Env: noEnv, LookPath: func(string) (string, error) { return "", errors.New("missing") },
		Seats: seatsFor(), Timeout: 5 * time.Second,
	})
	want := []string{CheckGitURL, CheckForgeWrite, CheckBaseDir, CheckRig, CheckRuntime}
	if len(report.Checks) != len(want) {
		t.Fatalf("checks = %d, want %d", len(report.Checks), len(want))
	}
	for i, id := range want {
		if report.Checks[i].ID != id {
			t.Fatalf("check %d = %q, want %q", i, report.Checks[i].ID, id)
		}
	}
	if report.Project != "p" {
		t.Fatalf("project = %q, want p", report.Project)
	}
	if report.CheckedAt.IsZero() {
		t.Fatal("CheckedAt is zero")
	}
}

func TestGitURLChecks(t *testing.T) {
	home := t.TempDir()
	main := bareRepo(t, "main")
	trunk := bareRepo(t, "trunk")
	missing := filepath.Join(t.TempDir(), "not-a-repository")

	tests := []struct {
		name    string
		project Project
		deps    Deps
		want    State
		detail  string // substring the Detail must carry
	}{
		{
			name:    "ok: a local bare repository with the default branch",
			project: Project{CloneURL: main, DefaultBranch: "main"},
			want:    OK,
			detail:  "answers",
		},
		{
			name:    "warn: the repository answers but lacks the default branch",
			project: Project{CloneURL: trunk, DefaultBranch: "main"},
			want:    Warn,
			detail:  "has no branch named main",
		},
		{
			name:    "fail: a path that does not exist",
			project: Project{CloneURL: missing},
			want:    Fail,
			detail:  "does not appear to be a git repository",
		},
		{
			name:    "unknown: the command does not answer in time",
			project: Project{CloneURL: main},
			deps: Deps{Timeout: time.Nanosecond, Git: func(ctx context.Context, args ...string) (string, string, error) {
				<-ctx.Done()
				return "", "", ctx.Err()
			}},
			want:   Unknown,
			detail: "did not answer",
		},
		{
			name:    "fail: the row names a forge with no URL",
			project: Project{Forge: "bitbucket", Repo: "o/r"},
			want:    Fail,
			detail:  "no URL to read",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			deps := tc.deps
			deps.Home = home
			deps.Env = noEnv
			if deps.Timeout == 0 {
				deps.Timeout = 5 * time.Second
			}
			report := RunWith(context.Background(), tc.project, config.Effective{}, deps)
			check := checkByID(t, report, CheckGitURL)
			if check.State != tc.want {
				t.Fatalf("state = %q, want %q (detail %q)", check.State, tc.want, check.Detail)
			}
			if !strings.Contains(check.Detail, tc.detail) {
				t.Fatalf("detail = %q, want it to contain %q", check.Detail, tc.detail)
			}
			if check.State == Unknown && check.Fix.Command == "" {
				t.Fatal("unknown must name the command that answers it")
			}
			if check.State == Fail && check.Fix.Why == "" {
				t.Fatal("a failure owes the operator a reason")
			}
		})
	}
}

// TestGitURLDetailIsTheCommandsOwnLine is the promise that makes the detail
// useful: a typo and a missing token produce different lines, so the line has to
// be git's, verbatim.
func TestGitURLDetailIsTheCommandsOwnLine(t *testing.T) {
	line := "fatal: could not read Username for 'https://github.com': terminal prompts disabled"
	deps := Deps{
		Home: t.TempDir(), Env: noEnv, Timeout: time.Second,
		Git: func(ctx context.Context, args ...string) (string, string, error) {
			return "", "warning: something\n" + line + "\n", errors.New("exit status 128")
		},
	}
	report := RunWith(context.Background(), Project{CloneURL: "https://github.com/o/r.git"}, config.Effective{}, deps)
	check := checkByID(t, report, CheckGitURL)
	if check.State != Fail {
		t.Fatalf("state = %q, want fail", check.State)
	}
	if check.Detail != line {
		t.Fatalf("detail = %q, want git's own last line %q", check.Detail, line)
	}
}

// TestForgeWriteChecks pins the probe's mapping: a validation refusal on the
// probe's own invalid field is OK, a permission refusal is Fail with the
// forge's own line and the scope-appropriate fix, no open item is Unknown
// (never fail), and a timeout is Unknown too. The Forge edge is injected; no
// test shells out to gh or glab.
func TestForgeWriteChecks(t *testing.T) {
	home := t.TempDir()

	// answer builds one Forge edge from a single recorded (stdout, stderr, err).
	answer := func(stdout, stderr string, err error) func(context.Context, string, ...string) (string, string, error) {
		return func(context.Context, string, ...string) (string, string, error) { return stdout, stderr, err }
	}

	tests := []struct {
		name    string
		project Project
		forge   func(ctx context.Context, binary string, args ...string) (string, string, error)
		want    State
		detail  string // substring the Detail must carry
		command string // substring the Fix.Command must carry; "" skips
		why     string // substring the Fix.Why must carry; "" skips
	}{
		{
			name:    "ok: 422 validation failed on the probe head, note probed on the newest open item",
			project: Project{Forge: "github", Repo: "o/r", DefaultBranch: "main", ProbeItemNumber: 42},
			forge:   forgeProbeOK,
			want:    OK,
			detail:  "may comment and open a change request",
		},
		{
			name:    "ok: 422 without a default branch probes base=main",
			project: Project{Forge: "github", Repo: "o/r", ProbeItemNumber: 42},
			forge:   forgeProbeOK,
			want:    OK,
			detail:  "may comment",
		},
		{
			name:    "fail: 403 on the change-request probe",
			project: Project{Forge: "github", Repo: "o/r", DefaultBranch: "main"},
			forge:   forgeProbeForbidden,
			want:    Fail,
			detail:  "Resource not accessible by personal access token",
			why:     "may not open a change request",
		},
		{
			name:    "fail: 403 on the note probe only",
			project: Project{Forge: "github", Repo: "o/r", DefaultBranch: "main", ProbeItemNumber: 42},
			forge: func(ctx context.Context, binary string, args ...string) (string, string, error) {
				if strings.Contains(strings.Join(args, " "), "/comments") {
					return forgeProbeForbidden(ctx, binary, args...)
				}
				return forgeProbeOK(ctx, binary, args...)
			},
			want:    Fail,
			detail:  "Resource not accessible",
			why:     "may not comment",
		},
		{
			name:    "unknown: no open item to probe a note on",
			project: Project{Forge: "github", Repo: "o/r", DefaultBranch: "main"},
			forge: func(ctx context.Context, binary string, args ...string) (string, string, error) {
				// A blank-body refusal never arrives: the PR probe passed, and
				// there is no item to aim the note probe at.
				if strings.Contains(strings.Join(args, " "), "/comments") {
					return "", "", errors.New("must not be called")
				}
				return forgeProbeOK(ctx, binary, args...)
			},
			want:   Unknown,
			detail: "no open item to probe a note on",
		},
		{
			name:    "unknown: the probe does not answer in time",
			project: Project{Forge: "github", Repo: "o/r", DefaultBranch: "main"},
			forge: func(ctx context.Context, binary string, args ...string) (string, string, error) {
				<-ctx.Done()
				return "", "", ctx.Err()
			},
			want:   Unknown,
			detail: "did not answer",
		},
		{
			name:    "unknown: a rate limit is not a permission fact",
			project: Project{Forge: "github", Repo: "o/r", DefaultBranch: "main"},
			forge: answer("",
				"gh: API rate limit exceeded for installation ID 1", errors.New("exit status 1")),
			want:   Unknown,
			detail: "did not classify",
		},
		{
			name:    "unknown: the 422 lands on a different field",
			project: Project{Forge: "github", Repo: "o/r", DefaultBranch: "main"},
			forge: answer(`{"message":"Validation Failed","errors":[{"resource":"PullRequest","field":"base","code":"invalid"}],"status":"422"}`,
				"gh: HTTP 422: Validation Failed", errors.New("exit status 1")),
			want:   Unknown,
			detail: "refused on \"base\"",
		},
		{
			name:    "unknown: an unexpectedly successful probe is owned up to, never green",
			project: Project{Forge: "github", Repo: "o/r", DefaultBranch: "main"},
			forge:   answer(`{"id":1}`, "", nil),
			want:    Unknown,
			detail:  "unexpectedly succeeded",
		},
		{
			name:    "unknown: no repository named",
			project: Project{Forge: "github"},
			forge:   forgeProbeOK,
			want:    Unknown,
			detail:  "names no repository",
		},
		{
			name:    "unknown: a forge the check cannot probe",
			project: Project{Forge: "bitbucket", Repo: "o/r"},
			forge:   forgeProbeOK,
			want:    Unknown,
			detail:  "not github or gitlab",
		},
		{
			name:    "ok: gitlab 400 on the invalid source branch",
			project: Project{Forge: "gitlab", Repo: "g/p", DefaultBranch: "main"},
			forge: answer(`{"message":{"source_branch":["is invalid"]}}`,
				"glab: HTTP 400: Bad Request", errors.New("exit status 1")),
			want:   OK,
			detail: "may comment and open a change request",
		},
		{
			name:    "fail: gitlab 403 on the merge-request probe",
			project: Project{Forge: "gitlab", Repo: "g/p", DefaultBranch: "main"},
			forge: answer(`{"message":"403 Forbidden"}`,
				"glab: error: 403 Forbidden", errors.New("exit status 1")),
			want:   Fail,
			detail: "403 Forbidden",
			why:    "may not open merge requests",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			deps := depsFor(home)
			deps.Forge = tc.forge
			report := RunWith(context.Background(), tc.project, config.Effective{}, deps)
			check := checkByID(t, report, CheckForgeWrite)
			if check.State != tc.want {
				t.Fatalf("state = %q, want %q (detail %q)", check.State, tc.want, check.Detail)
			}
			if !strings.Contains(check.Detail, tc.detail) {
				t.Fatalf("detail = %q, want it to contain %q", check.Detail, tc.detail)
			}
			if tc.command != "" && !strings.Contains(check.Fix.Command, tc.command) {
				t.Fatalf("fix command = %q, want it to contain %q", check.Fix.Command, tc.command)
			}
			if tc.why != "" && !strings.Contains(check.Fix.Why, tc.why) {
				t.Fatalf("fix why = %q, want it to contain %q", check.Fix.Why, tc.why)
			}
			if check.State == Fail && check.Fix.Why == "" {
				t.Fatal("a failure owes the operator a reason")
			}
		})
	}
}

// TestForgeWriteNeverWarnsAndNeverLeavesState: Warn is the one state this
// check must never produce (a token that cannot write means no item can be
// delivered), and a probe that left state behind must not read as green.
func TestForgeWriteNeverWarnsAndNeverLeavesState(t *testing.T) {
	deps := depsFor(t.TempDir())
	deps.Forge = forgeProbeForbidden
	report := RunWith(context.Background(), Project{Forge: "github", Repo: "o/r", DefaultBranch: "main"},
		config.Effective{}, deps)
	check := checkByID(t, report, CheckForgeWrite)
	if check.State == Warn {
		t.Fatal("forge_write warned; a token that cannot write stops every dispatch and must fail")
	}
	if report.Ready {
		t.Fatal("report ready with a forge_write failure; Ready must turn false on Fail")
	}
}

func TestBaseDirChecks(t *testing.T) {
	origin := "https://github.com/o/r.git"
	matching := checkoutWithOrigin(t, origin)
	other := checkoutWithOrigin(t, "https://github.com/o/other.git")
	empty := t.TempDir()
	plain := t.TempDir()
	if err := os.WriteFile(filepath.Join(plain, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	unreadable := t.TempDir()
	if err := os.Chmod(unreadable, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o755) })

	worktrees := t.TempDir()
	tests := []struct {
		name       string
		mainPath   string
		worktrees  string
		want       State
		wantStep   string
		detailPart []string
	}{
		{name: "ok: a checkout of the project's repository", mainPath: matching, worktrees: worktrees, want: OK, detailPart: []string{"o/r"}},
		{name: "warn: the checkout is right but paths.worktrees is absent", mainPath: matching, worktrees: filepath.Join(t.TempDir(), "no-worktrees"), want: Warn, detailPart: []string{"does not exist yet"}},
		{name: "fail: missing directory offers mkdir_base", mainPath: filepath.Join(t.TempDir(), "nope"), want: Fail, wantStep: StepMkdirBase, detailPart: []string{"does not exist"}},
		{name: "fail: empty directory offers clone", mainPath: empty, want: Fail, wantStep: StepClone, detailPart: []string{"is empty"}},
		{name: "fail: wrong origin names both and offers no step", mainPath: other, want: Fail, wantStep: "", detailPart: []string{"https://github.com/o/other.git", origin}},
		{name: "fail: not a repository", mainPath: plain, want: Fail, wantStep: "", detailPart: []string{"is not a git checkout"}},
		{name: "fail: a file, not a directory", mainPath: filePath, want: Fail, wantStep: "", detailPart: []string{"is a file"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := Project{Key: "p", Forge: "github", Repo: "o/r", MainPath: tc.mainPath, Worktrees: tc.worktrees}
			report := RunWith(context.Background(), p, config.Effective{}, depsFor(t.TempDir()))
			check := checkByID(t, report, CheckBaseDir)
			if check.State != tc.want {
				t.Fatalf("state = %q, want %q (detail %q)", check.State, tc.want, check.Detail)
			}
			if check.Fix.Step != tc.wantStep {
				t.Fatalf("step = %q, want %q", check.Fix.Step, tc.wantStep)
			}
			for _, part := range tc.detailPart {
				if !strings.Contains(check.Detail, part) {
					t.Fatalf("detail = %q, want it to contain %q", check.Detail, part)
				}
			}
			if check.State == Fail && check.Fix.Step == "" && check.Detail == "" {
				t.Fatal("a fail with no step still owes a diagnosis")
			}
		})
	}

	t.Run("unknown: the directory cannot be listed", func(t *testing.T) {
		p := Project{Key: "p", Forge: "github", Repo: "o/r", MainPath: unreadable}
		report := RunWith(context.Background(), p, config.Effective{}, depsFor(t.TempDir()))
		check := checkByID(t, report, CheckBaseDir)
		if check.State != Unknown {
			t.Fatalf("state = %q, want unknown (detail %q)", check.State, check.Detail)
		}
		if check.Fix.Command == "" {
			t.Fatal("unknown must carry the command that answers it")
		}
	})
}

func TestRigChecks(t *testing.T) {
	const rigName = "r"
	tests := []struct {
		name     string
		project  Project
		builders int
		specRT   string
		reviewRT string
		noSpec   bool
		seats    []rig.Seat
		seatsErr error
		want     State
		detail   string
		wantStep string
	}{
		{
			name:     "ok: two builders and a reviewer, all on the project's runtime",
			project:  Project{Key: "p", Rig: rigName, Runtime: "omp"},
			builders: 2, specRT: "omp",
			seats:  []rig.Seat{seat("dev-build-1@r", "omp"), seat("dev-build-2@r", "omp"), seat("dev-review@r", "omp")},
			want:   OK,
			detail: "2 builders and one reviewer",
		},
		{
			name:     "fail: the rig is not up",
			project:  Project{Key: "p", Rig: rigName},
			builders: 1, specRT: "omp",
			seats:    nil,
			want:     Fail,
			detail:   "is not up",
			wantStep: StepUpRig,
		},
		{
			name:     "fail: the seat count disagrees with the spec",
			project:  Project{Key: "p", Rig: rigName},
			builders: 2, specRT: "omp",
			seats:  []rig.Seat{seat("dev-build-1@r", "omp"), seat("dev-review@r", "omp")},
			want:   Fail,
			detail: "is running 2 seats; the spec declares 2 builders and one reviewer (3 seats)",
		},
		{
			name:     "fail: a seat runs another runtime",
			project:  Project{Key: "p", Rig: rigName, Runtime: "omp"},
			builders: 1, specRT: "omp",
			seats:  []rig.Seat{seat("dev-build-1@r", "claude"), seat("dev-review@r", "omp")},
			want:   Fail,
			detail: "dev-build-1@r runs claude; the project runs omp",
		},
		{
			name:     "warn: the spec is stale against the row",
			project:  Project{Key: "p", Rig: rigName, Runtime: "claude"},
			builders: 1, specRT: "omp",
			seats:    []rig.Seat{seat("dev-build-1@r", "claude"), seat("dev-review@r", "claude")},
			want:     Warn,
			detail:   "still declares omp",
			wantStep: StepApplyRig,
		},
		{
			name:     "fail: no spec for the rig",
			project:  Project{Key: "p", Rig: rigName},
			noSpec:   true,
			want:     Fail,
			detail:   "is not in the specs",
			wantStep: StepApplyRig,
		},
		{
			name:    "fail: the project names no rig",
			project: Project{Key: "p"},
			noSpec:  true,
			want:    Fail,
			detail:  "names no rig",
		},
		{
			name:     "unknown: the rig CLI does not answer",
			project:  Project{Key: "p", Rig: rigName, Runtime: "omp"},
			builders: 1, specRT: "omp",
			seatsErr: errors.New("rig: daemon is not running"),
			want:     Unknown,
			detail:   "could not be read",
		},
		{
			name:     "unknown: a draft has no rig yet",
			project:  Project{Key: "p", Rig: rigName, Draft: true},
			builders: 1, specRT: "omp",
			want:   Unknown,
			detail: "created with the project",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if !tc.noSpec {
				writeSpec(t, home, rigName, tc.builders, tc.specRT, tc.reviewRT)
			}
			deps := depsFor(home)
			deps.Seats = func(ctx context.Context, name string) ([]rig.Seat, error) {
				if tc.seatsErr != nil {
					return nil, tc.seatsErr
				}
				return tc.seats, nil
			}
			report := RunWith(context.Background(), tc.project, config.Effective{}, deps)
			check := checkByID(t, report, CheckRig)
			if check.State != tc.want {
				t.Fatalf("state = %q, want %q (detail %q)", check.State, tc.want, check.Detail)
			}
			if !strings.Contains(check.Detail, tc.detail) {
				t.Fatalf("detail = %q, want it to contain %q", check.Detail, tc.detail)
			}
			if tc.wantStep != "" && check.Fix.Step != tc.wantStep {
				t.Fatalf("step = %q, want %q", check.Fix.Step, tc.wantStep)
			}
			if check.State == Unknown && check.Fix.Command == "" {
				t.Fatal("unknown must carry the command that answers it")
			}
		})
	}
}

func TestRuntimeChecks(t *testing.T) {
	tests := []struct {
		name       string
		runtime    string
		found      bool
		env        map[string]string
		unreadable bool
		want       State
		detail     string
	}{
		{name: "ok: installed with a credential", runtime: "omp", found: true, env: map[string]string{"ANTHROPIC_API_KEY": "x"}, want: OK, detail: "a credential exists"},
		{name: "fail: installed, nobody logged in", runtime: "claude", found: true, env: nil, want: Fail, detail: "no claude credential"},
		{name: "fail: not installed", runtime: "codex", found: false, env: map[string]string{"OPENAI_API_KEY": "x"}, want: Fail, detail: "not installed"},
		{name: "fail: a value outside the four", runtime: "gpt", found: true, env: map[string]string{"OPENAI_API_KEY": "x"}, want: Fail, detail: "not one of pi, omp, codex, claude"},
		{name: "unknown: the credential store cannot be read", runtime: "codex", found: true, env: nil, unreadable: true, want: Unknown, detail: "could not be read"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if tc.unreadable {
				// A credential file exists but its directory denies access: the
				// probe cannot tell "absent" from "forbidden", so the honest
				// answer is unknown rather than "log in".
				dir := filepath.Join(home, ".codex")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(dir, 0o000); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
			}
			deps := Deps{
				Home: home,
				Env:  func(name string) string { return tc.env[name] },
				LookPath: func(string) (string, error) {
					if tc.found {
						return "/usr/bin/" + tc.runtime, nil
					}
					return "", errors.New("not found")
				},
				Timeout: time.Second,
				Seats:   seatsFor(),
			}
			p := Project{Key: "p", Rig: "r", Runtime: tc.runtime, Draft: true}
			report := RunWith(context.Background(), p, config.Effective{}, deps)
			check := checkByID(t, report, CheckRuntime)
			if check.State != tc.want {
				t.Fatalf("state = %q, want %q (detail %q)", check.State, tc.want, check.Detail)
			}
			if !strings.Contains(check.Detail, tc.detail) {
				t.Fatalf("detail = %q, want it to contain %q", check.Detail, tc.detail)
			}
			if tc.want == Fail && strings.Contains(tc.detail, "credential") && check.Fix.Command == "" {
				t.Fatal("a missing credential owes the login command")
			}
		})
	}
}

// TestRuntimeCredentialFileIsDetected proves the file half of the probe: a
// credential on disk counts even with an empty environment.
func TestRuntimeCredentialFileIsDetected(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), []byte(`{"token":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	deps := Deps{Home: home, Env: noEnv, LookPath: func(string) (string, error) { return "/usr/bin/claude", nil },
		Timeout: time.Second, Seats: seatsFor()}
	report := RunWith(context.Background(), Project{Key: "p", Rig: "r", Runtime: "claude", Draft: true}, config.Effective{}, deps)
	check := checkByID(t, report, CheckRuntime)
	if check.State != OK {
		t.Fatalf("state = %q, want ok (detail %q)", check.State, check.Detail)
	}
	if !report.Runtime.AuthPresent {
		t.Fatal("report.Runtime.AuthPresent = false, want true")
	}
}

// TestNoCredentialReachesTheReport is the rule that a boolean stands in for the
// secret: a credential planted in a temp HOME and in the environment must not
// appear as any string in the report.
func TestNoCredentialReachesTheReport(t *testing.T) {
	const secret = "sk-very-secret-credential-do-not-print-4f2a"

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "auth.json"),
		[]byte(`{"token":"`+secret+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"OPENAI_API_KEY": secret, "ANTHROPIC_API_KEY": secret}
	repo := bareRepo(t, "main")
	deps := Deps{
		Home:     home,
		Env:      func(name string) string { return env[name] },
		LookPath: func(string) (string, error) { return "/usr/bin/codex", nil },
		Timeout:  5 * time.Second,
		Seats:    seatsFor(),
	}
	report := RunWith(context.Background(), Project{
		Key: "p", Rig: "r", Runtime: "codex", CloneURL: repo, DefaultBranch: "main",
		MainPath: filepath.Join(home, "base"),
	}, config.Effective{}, deps)

	for _, value := range reportStrings(report) {
		if strings.Contains(value, secret) {
			t.Fatalf("report carries the credential: %q", value)
		}
	}
	if !report.Runtime.AuthPresent {
		t.Fatal("AuthPresent = false, want true — the credential exists, only its value is withheld")
	}
}

// reportStrings is every string a report can carry, so one assertion covers the
// whole body a handler would serialise.
func reportStrings(report Report) []string {
	out := []string{report.Project}
	for _, check := range report.Checks {
		out = append(out, check.ID, check.Label, string(check.State), check.Detail,
			check.Fix.Command, check.Fix.Step, check.Fix.Why)
	}
	out = append(out, report.Runtime.ID, report.Runtime.LoginHint, report.Runtime.LoginDoc)
	return out
}

// forgeProbeOK answers both GitHub probes the way a forge answers a
// deliberately invalid write from a token that cleared authorization: a 422 on
// the probe's own invalid field, never a 403.
func forgeProbeOK(ctx context.Context, binary string, args ...string) (string, string, error) {
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "-i user"):
		return "HTTP/2 200\nx-oauth-scopes: repo\n\n", "", nil
	case strings.Contains(joined, "/comments"):
		return `{"message":"Validation Failed","errors":[{"resource":"IssueComment","code":"unprocessable","field":"data","message":"Body cannot be blank"}],"status":"422"}`,
			"gh: HTTP 422: Validation Failed", errors.New("exit status 1")
	case strings.Contains(joined, "/pulls"):
		return `{"message":"Validation Failed","errors":[{"resource":"PullRequest","field":"head","code":"invalid"}],"status":"422"}`,
			"gh: HTTP 422: Validation Failed", errors.New("exit status 1")
	}
	return "", "", errors.New("unexpected forge call " + joined)
}

// forgeProbeForbidden answers both probes the way a forge answers a write from
// a token that may read but may not write.
func forgeProbeForbidden(ctx context.Context, binary string, args ...string) (string, string, error) {
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "-i user"):
		return "HTTP/2 200\nx-oauth-scopes: repo:read\n\n", "", nil
	case strings.Contains(joined, "/comments"), strings.Contains(joined, "/pulls"):
		return `{"message":"Resource not accessible by personal access token","status":"403"}`,
			"gh: HTTP 403: Resource not accessible by personal access token", errors.New("exit status 1")
	}
	return "", "", errors.New("unexpected forge call " + joined)
}

// TestReadyFoldsStates pins the rule the surfaces show: warn is still ready,
// unknown is not.
func TestReadyFoldsStates(t *testing.T) {
	// Every case runs the whole check set through Run: Ready is the one field a
	// surface gates on, so it is asserted where the checks actually meet.
	ready := func(t *testing.T, project Project, deps Deps) Report {
		t.Helper()
		return RunWith(context.Background(), project, config.Effective{}, deps)
	}
	repo := bareRepo(t, "main")
	checkout := checkoutWithOrigin(t, repo)

	t.Run("ok is ready", func(t *testing.T) {
		home := t.TempDir()
		writeSpec(t, home, "r", 1, "omp", "omp")
		deps := depsFor(home)
		deps.LookPath = func(string) (string, error) { return "/usr/bin/omp", nil }
		deps.Seats = seatsFor(seat("dev-build-1@r", "omp"), seat("dev-review@r", "omp"))
		deps.Env = func(name string) string {
			if name == "ANTHROPIC_API_KEY" {
				return "present"
			}
			return ""
		}
		deps.Forge = forgeProbeOK
		report := ready(t, Project{
			Key: "p", Rig: "r", Runtime: "omp", Forge: "github", Repo: "o/r",
			CloneURL: repo, DefaultBranch: "main", MainPath: checkout, ProbeItemNumber: 12,
		}, deps)
		if !report.Ready {
			t.Fatalf("ready = false, want true: %+v", report.Checks)
		}
		for _, check := range report.Checks {
			if check.State != OK {
				t.Fatalf("%s = %q, want ok", check.ID, check.State)
			}
		}
	})

	t.Run("a warning alone is still ready", func(t *testing.T) {
		home := t.TempDir()
		writeSpec(t, home, "r", 1, "omp", "omp")
		deps := depsFor(home)
		deps.LookPath = func(string) (string, error) { return "/usr/bin/omp", nil }
		deps.Seats = seatsFor(seat("dev-build-1@r", "omp"), seat("dev-review@r", "omp"))
		deps.Env = func(name string) string {
			if name == "ANTHROPIC_API_KEY" {
				return "present"
			}
			return ""
		}
		deps.Forge = forgeProbeOK
		// The repository answers but has no "main", so git_url warns; the
		// checkout has to be of that same repository for base_dir to pass.
		trunk := bareRepo(t, "trunk")
		trunkCheckout := checkoutWithOrigin(t, trunk)
		report := ready(t, Project{
			Key: "p", Rig: "r", Runtime: "omp", Forge: "github", Repo: "o/r",
			CloneURL: trunk, DefaultBranch: "main", MainPath: trunkCheckout, ProbeItemNumber: 12,
		}, deps)
		if got := checkByID(t, report, CheckGitURL).State; got != Warn {
			t.Fatalf("git_url = %q, want warn", got)
		}
		if !report.Ready {
			t.Fatalf("ready = false, want true: %+v", report.Checks)
		}
	})

	t.Run("a failure is not ready", func(t *testing.T) {
		home := t.TempDir()
		writeSpec(t, home, "r", 1, "omp", "omp")
		deps := depsFor(home)
		deps.LookPath = func(string) (string, error) { return "/usr/bin/omp", nil }
		deps.Seats = seatsFor(seat("dev-build-1@r", "omp"), seat("dev-review@r", "omp"))
		report := ready(t, Project{
			Key: "p", Rig: "r", Runtime: "omp", CloneURL: repo, DefaultBranch: "main", MainPath: checkout,
		}, deps) // no credential anywhere: runtime fails
		if report.Ready {
			t.Fatalf("ready = true, want false: %+v", report.Checks)
		}
		if got := checkByID(t, report, CheckRuntime).State; got != Fail {
			t.Fatalf("runtime = %q, want fail", got)
		}
	})

	t.Run("an unknown is not ready", func(t *testing.T) {
		home := t.TempDir()
		writeSpec(t, home, "r", 1, "omp", "omp")
		deps := depsFor(home)
		deps.LookPath = func(string) (string, error) { return "/usr/bin/omp", nil }
		deps.Seats = func(ctx context.Context, name string) ([]rig.Seat, error) {
			return nil, errors.New("rig: daemon is not running")
		}
		deps.Env = func(string) string { return "present" }
		report := ready(t, Project{
			Key: "p", Rig: "r", Runtime: "omp", CloneURL: repo, DefaultBranch: "main", MainPath: checkout,
		}, deps)
		if got := checkByID(t, report, CheckRig).State; got != Unknown {
			t.Fatalf("rig = %q, want unknown", got)
		}
		if report.Ready {
			t.Fatalf("ready = true, want false: %+v", report.Checks)
		}
	})
}

// TestKnownRuntime pins the closed enum the rest of the app names.
func TestKnownRuntime(t *testing.T) {
	for _, id := range []string{"pi", "omp", "codex", "claude", "OMP", " Claude "} {
		if !KnownRuntime(id) {
			t.Fatalf("KnownRuntime(%q) = false", id)
		}
	}
	for _, id := range []string{"", "gpt", "claud", "console"} {
		if KnownRuntime(id) {
			t.Fatalf("KnownRuntime(%q) = true", id)
		}
	}
	if DefaultRuntime != "omp" {
		t.Fatalf("DefaultRuntime = %q, want omp", DefaultRuntime)
	}
}

// TestFromConfigReadsTheRow pins the row mapping, including the rigs list
// fallback: the registry accepts either spelling.
func TestFromConfigReadsTheRow(t *testing.T) {
	var row config.Project
	row.Key = "k"
	row.Forge = "github"
	row.Repo = "o/r"
	row.DefaultBranch = "main"
	row.Rigs = config.NamedRigs("second", "third")
	row.Paths.Main = "/tmp/main"
	p := FromConfig(row)
	if p.Rig != "second" {
		t.Fatalf("rig = %q, want the first of rigs", p.Rig)
	}
	if p.MainPath != "/tmp/main" || p.DefaultBranch != "main" || p.Repo != "o/r" {
		t.Fatalf("row facts = %+v", p)
	}
	row.Rig = "single"
	if got := FromConfig(row).Rig; got != "single" {
		t.Fatalf("rig = %q, want the rig field when set", got)
	}
}

// TestFromConfigCarriesTheForgeHost pins the write probe's host plumbing
// (forge-host D4): a self-hosted project's probe adapter is bound to the same
// host the mirror reads, and a project with nothing to resolve from leaves it
// empty — no fallback invented.
func TestFromConfigCarriesTheForgeHost(t *testing.T) {
	selfHosted := config.Project{Key: "mnp", Forge: "gitlab", ForgeHost: "gitlab.horisen.org", Repo: "x"}
	if got := FromConfig(selfHosted).ForgeHost; got != "gitlab.horisen.org" {
		t.Fatalf("explicit host = %q, want gitlab.horisen.org", got)
	}
	fromRepo := config.Project{Key: "mnp", Forge: "gitlab", RepoURL: "https://gitlab.horisen.org/horisen/mnp.git", Repo: "x"}
	if got := FromConfig(fromRepo).ForgeHost; got != "gitlab.horisen.org" {
		t.Fatalf("repo_url host = %q, want gitlab.horisen.org", got)
	}
	neither := config.Project{Key: "mnp", Forge: "gitlab", Repo: "x"}
	if got := FromConfig(neither).ForgeHost; got != "gitlab.com" {
		t.Fatalf("default host = %q, want the resolver's gitlab.com", got)
	}
	// A GitHub project resolves to nothing — the host is gh's business.
	github := config.Project{Key: "mnp", Forge: "github", Repo: "x"}
	if got := FromConfig(github).ForgeHost; got != "" {
		t.Fatalf("github host = %q, want empty", got)
	}
}

// TestNormalizeRemote pins the comparisons that decide whether a checkout is
// this project's: transports and a .git suffix must not matter.
func TestNormalizeRemote(t *testing.T) {
	same := [][2]string{
		{"https://github.com/o/r.git", "https://github.com/o/r"},
		{"git@github.com:o/r.git", "https://github.com/o/r.git"},
		{"ssh://git@github.com/o/r.git", "https://github.com/o/r"},
		{"https://GitHub.com/o/r/", "https://github.com/o/r.git"},
	}
	for _, pair := range same {
		if normalizeRemote(pair[0]) != normalizeRemote(pair[1]) {
			t.Fatalf("%q and %q should be the same repository", pair[0], pair[1])
		}
	}
	if normalizeRemote("https://github.com/o/r") == normalizeRemote("https://github.com/o/other") {
		t.Fatal("different repositories compared equal")
	}
}
