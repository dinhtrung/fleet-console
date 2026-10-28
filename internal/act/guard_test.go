package act

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/rig"
)

type fixtureRunner map[string][]byte

func (f fixtureRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	data, ok := f[strings.Join(args, " ")]
	if !ok {
		return nil, os.ErrNotExist
	}
	return data, nil
}

func rigFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "rig", "testdata", rig.Floor, name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

// TestGateRefusesOnRenamedField is the evidence for #22: a recorded compat
// fixture with a renamed required field makes the act loop refuse with that
// one named reason.
func TestGateRefusesOnRenamedField(t *testing.T) {
	runner := fixtureRunner{
		"--version":                []byte("0.7.0 (c99450f3)\n"),
		"queue list -A -a --json":  rigFixture(t, "queue-list-renamed-pickup.json"),
		"ps --nodes --full --json": rigFixture(t, "ps-nodes.json"),
	}
	report := rig.NewProbe(runner, func(string, ...any) {}).Run(context.Background())
	if report.OK {
		t.Fatal("report is ok with the renamed fixture")
	}
	if report.Status != rig.StatusBlocked {
		t.Fatalf("status = %q, want %q", report.Status, rig.StatusBlocked)
	}

	gate := NewGate(report)
	err := gate.Check()
	if err == nil {
		t.Fatal("gate allowed the act loop on a failed compat report")
	}
	var refusal *Refusal
	if !errors.As(err, &refusal) {
		t.Fatalf("error = %T %v, want *Refusal", err, err)
	}
	if !strings.Contains(refusal.Reason, "pickup_state") {
		t.Fatalf("reason = %q, want the renamed field", refusal.Reason)
	}
	if gate.Reason() != refusal.Reason {
		t.Fatalf("Reason() = %q, want %q", gate.Reason(), refusal.Reason)
	}
	if !strings.HasPrefix(err.Error(), "act loop refused: ") {
		t.Fatalf("error = %q, want the act-loop refusal prefix", err)
	}
}

func TestGateAllowsAnOKReport(t *testing.T) {
	gate := NewGate(rig.Report{OK: true, Status: rig.StatusOK})
	if err := gate.Check(); err != nil {
		t.Fatalf("Check = %v, want nil", err)
	}
	if gate.Reason() != "" {
		t.Fatalf("Reason = %q, want empty", gate.Reason())
	}
}
