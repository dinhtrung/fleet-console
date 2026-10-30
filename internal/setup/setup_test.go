package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// fakeRig records what the chain asked the rig to do.
type fakeRig struct {
	seats    int
	synced   int
	upped    int
	upErr    error
	seatsErr error
}

func (f *fakeRig) LiveSeats(context.Context, string) (int, error) {
	if f.seatsErr != nil {
		return 0, f.seatsErr
	}
	return f.seats, nil
}

func (f *fakeRig) SpecsSync(context.Context) error { f.synced++; return nil }

func (f *fakeRig) Up(context.Context, string) error {
	f.upped++
	if f.upErr != nil {
		return f.upErr
	}
	// A real launch produces seats; the next LiveSeats must see them, which is
	// what makes the second run's up_rig a skip.
	f.seats = 3
	return nil
}

// fakeExec answers the clone without a network: it writes a file into the target
// the way a real clone would, so emptiness is observable afterwards.
func fakeExec(calls *[]string) func(context.Context, string, ...string) (string, error) {
	return func(_ context.Context, name string, args ...string) (string, error) {
		*calls = append(*calls, name+" "+strings.Join(args, " "))
		if name == "git" && len(args) == 3 && args[0] == "clone" {
			target := args[2]
			_ = os.MkdirAll(target, 0o755)
			_ = os.WriteFile(filepath.Join(target, "README.md"), []byte("cloned\n"), 0o644)
			return "Cloning into '" + target + "'...\n", nil
		}
		return "", nil
	}
}

func project(t *testing.T) rig.Project {
	t.Helper()
	return rig.Project{
		Key: "app", Rig: "rig-a", Name: "App", Summary: "summary",
		RepoPath: "/repo", WorktreeRoot: "/wt", Builders: 2,
		Runtime: rig.RuntimeCodex, BuilderModel: "model-x",
		ProviderEnv: "PROVIDER_API_KEY", ProviderKey: "unused-in-render",
		CulturePath: "", GuidancePath: "",
	}
}

func newRunner(t *testing.T, r Rig, calls *[]string) (*Runner, *store.Store) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "fleet.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return &Runner{Store: st, Rig: r, Exec: fakeExec(calls)}, st
}

func baseInput(t *testing.T, home, dir string) Input {
	t.Helper()
	p := project(t)
	// RenderRig needs a culture file that exists; give it one.
	culture := filepath.Join(t.TempDir(), "CULTURE.md")
	if err := os.WriteFile(culture, []byte("# rules\n"), 0o644); err != nil {
		t.Fatalf("culture: %v", err)
	}
	p.CulturePath = culture
	return Input{
		BaseDir: dir, RepoURL: "https://example.test/o/r",
		RigName: "rig-a", Project: p, Home: home, RigBinary: "rig",
	}
}

// TestChainTwiceIsAllSkipped is the acceptance: the second run does no work.
func TestChainTwiceIsAllSkipped(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	dir := filepath.Join(t.TempDir(), "checkout")
	var calls []string
	fake := &fakeRig{}
	runner, _ := newRunner(t, fake, &calls)
	in := baseInput(t, home, dir)

	first, err := runner.Run(ctx, in, nil)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if len(first) != 4 {
		t.Fatalf("first = %d steps, want 4", len(first))
	}
	for _, result := range first {
		if result.State != StateDone {
			t.Fatalf("first run step %s = %s (%s), want done", result.Step, result.State, result.Output)
		}
	}
	if fake.synced != 1 || fake.upped != 1 {
		t.Fatalf("rig synced=%d upped=%d, want one each", fake.synced, fake.upped)
	}

	second, err := runner.Run(ctx, in, nil)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	for _, result := range second {
		if result.State != StateSkipped {
			t.Fatalf("second run step %s = %s (%s), want skipped", result.Step, result.State, result.Output)
		}
	}
	if fake.synced != 1 || fake.upped != 1 {
		t.Fatalf("second run touched the rig: synced=%d upped=%d", fake.synced, fake.upped)
	}
}

// TestCloneNeverRunsTwice: the audit trail holds exactly one git clone.
func TestCloneNeverRunsTwice(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	dir := filepath.Join(t.TempDir(), "checkout")
	var calls []string
	runner, st := newRunner(t, &fakeRig{}, &calls)
	in := baseInput(t, home, dir)

	for run := 1; run <= 3; run++ {
		if _, err := runner.Run(ctx, in, nil); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
	}

	clones := 0
	for _, call := range calls {
		if strings.HasPrefix(call, "git clone") {
			clones++
		}
	}
	if clones != 1 {
		t.Fatalf("git clone ran %d times, want exactly 1 (calls: %v)", clones, calls)
	}

	events, err := st.RecentEvents(ctx, 50)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	audited := 0
	for _, event := range events {
		if event.Kind == "setup.step" && strings.Contains(event.Detail, "git clone") {
			audited++
		}
	}
	if audited != 1 {
		t.Fatalf("audit rows naming git clone = %d, want 1", audited)
	}
}

// TestCloneRefusesNonEmptyDirectory: a checkout with work in it is never cloned
// over, even on the first run.
func TestCloneRefusesNonEmptyDirectory(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "existing.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var calls []string
	runner, _ := newRunner(t, &fakeRig{}, &calls)
	results, err := runner.Run(ctx, baseInput(t, t.TempDir(), dir), []string{StepClone})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(results) != 1 || results[0].State != StateSkipped {
		t.Fatalf("results = %+v, want one skipped clone", results)
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %v, want none", calls)
	}
	if _, err := os.Stat(filepath.Join(dir, "existing.go")); err != nil {
		t.Fatal("the existing file was removed")
	}
}

// TestMkdirBaseSkipsExisting keeps an operator's real checkout untouched.
func TestMkdirBaseSkipsExisting(t *testing.T) {
	dir := t.TempDir()
	runner, _ := newRunner(t, &fakeRig{}, new([]string))
	results, err := runner.Run(context.Background(), baseInput(t, t.TempDir(), dir), []string{StepMkdirBase})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if results[0].State != StateSkipped {
		t.Fatalf("state = %s, want skipped", results[0].State)
	}
}

// TestFailedStepStopsTheChain: the response holds the failure and nothing after.
func TestFailedStepStopsTheChain(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	dir := filepath.Join(t.TempDir(), "checkout")
	var calls []string
	runner, _ := newRunner(t, &fakeRig{}, &calls)
	in := baseInput(t, home, dir)
	in.RepoURL = "" // clone cannot succeed without a URL

	results, err := runner.Run(ctx, in, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d entries, want 2 (mkdir_base, clone)", len(results))
	}
	if results[1].Step != StepClone || results[1].State != StateFailed {
		t.Fatalf("second = %+v, want a failed clone", results[1])
	}
}

// TestUpRigSkipsWhenAlreadyUp: a running rig is not launched twice.
func TestUpRigSkipsWhenAlreadyUp(t *testing.T) {
	fake := &fakeRig{seats: 3}
	runner, _ := newRunner(t, fake, new([]string))
	results, err := runner.Run(context.Background(), baseInput(t, t.TempDir(), t.TempDir()), []string{StepUpRig})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if results[0].State != StateSkipped || fake.upped != 0 {
		t.Fatalf("results = %+v upped=%d, want skipped and no launch", results, fake.upped)
	}
}

// TestOnlyAllowlistedSteps: an id outside the four is refused, not ignored.
func TestOnlyAllowlistedSteps(t *testing.T) {
	runner, _ := newRunner(t, &fakeRig{}, new([]string))
	for _, bad := range []string{"login", "rm_rf", "git_clone", ""} {
		if _, err := runner.Run(context.Background(), baseInput(t, t.TempDir(), t.TempDir()), []string{bad}); err == nil {
			t.Fatalf("step %q was accepted", bad)
		}
	}
}

// TestNoStepLogsIn is design §4's rule, checked over the audit trail of a full
// run: no command mentions login, auth or a token variable.
func TestNoStepLogsIn(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	var calls []string
	runner, st := newRunner(t, &fakeRig{}, &calls)
	if _, err := runner.Run(ctx, baseInput(t, home, filepath.Join(t.TempDir(), "co")), nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	events, err := st.RecentEvents(ctx, 50)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	rows := 0
	for _, event := range events {
		if event.Kind != "setup.step" {
			continue
		}
		rows++
		lower := strings.ToLower(event.Detail)
		for _, forbidden := range []string{"login", "auth", "_api_key", "token"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("audit row %q contains %q", event.Detail, forbidden)
			}
		}
	}
	if rows != 4 {
		t.Fatalf("audit rows = %d, want 4", rows)
	}
}

// TestAuditCarriesCommandAndLastLine: the trail answers what ran and what it said.
func TestAuditCarriesCommandAndLastLine(t *testing.T) {
	ctx := context.Background()
	var calls []string
	runner, st := newRunner(t, &fakeRig{}, &calls)
	dir := filepath.Join(t.TempDir(), "co")
	if _, err := runner.Run(ctx, baseInput(t, t.TempDir(), dir), []string{StepMkdirBase, StepClone}); err != nil {
		t.Fatalf("run: %v", err)
	}
	events, err := st.RecentEvents(ctx, 20)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	var cloneDetail string
	for _, event := range events {
		if event.Kind == "setup.step" && event.Subject == StepClone {
			cloneDetail = event.Detail
		}
	}
	if !strings.Contains(cloneDetail, "git clone") || !strings.Contains(cloneDetail, "Cloning into") {
		t.Fatalf("clone audit = %q, want the command and its last line", cloneDetail)
	}
}
