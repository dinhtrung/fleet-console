package rig

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type scriptedRunner struct {
	mu    sync.Mutex
	calls [][]string
	fn    func(args []string) ([]byte, error)
}

func (r *scriptedRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	r.mu.Lock()
	r.calls = append(r.calls, append([]string(nil), args...))
	r.mu.Unlock()
	if r.fn == nil {
		return nil, fmt.Errorf("unexpected call: %s", strings.Join(args, " "))
	}
	return r.fn(args)
}

func (r *scriptedRunner) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *scriptedRunner) saw(prefix string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, call := range r.calls {
		if strings.HasPrefix(strings.Join(call, " "), prefix) {
			return true
		}
	}
	return false
}

func fixtureBytes(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", Floor, name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func TestQueueShowFixture(t *testing.T) {
	runner := &scriptedRunner{fn: func([]string) ([]byte, error) {
		return fixtureBytes(t, "queue-show.json"), nil
	}}
	item, err := NewAdapter(runner).QueueShow(context.Background(), "qitem-test")
	if err != nil {
		t.Fatalf("queue show: %v", err)
	}
	if item.ID != "qitem-20261028164026-242c10cda7e2c6b3" {
		t.Fatalf("id = %q", item.ID)
	}
	if item.State != "handed-off" || item.PickupState != "terminal" {
		t.Fatalf("state/pickup = %q/%q", item.State, item.PickupState)
	}
	if item.ObligationState != "handed-off" {
		t.Fatalf("obligation state = %q", item.ObligationState)
	}
}

func TestQueueWritesBuildExpectedArgv(t *testing.T) {
	runner := &scriptedRunner{fn: func([]string) ([]byte, error) {
		return fixtureBytes(t, "queue-show.json"), nil
	}}
	adapter := NewAdapter(runner)
	ctx := context.Background()
	if _, err := adapter.QueueCreate(ctx, CreateRequest{Destination: "seat@rig", Summary: "s", BodyFile: "/tmp/b", Priority: "routine", Tags: []string{"a", "b"}}); err != nil {
		t.Fatal(err)
	}
	if !runner.saw("queue create --destination seat@rig --summary s --body-file /tmp/b") {
		t.Fatalf("create argv: %v", runner.calls[0])
	}
	if _, err := adapter.QueueUpdate(ctx, "q1", UpdateRequest{Note: "n", State: "canceled", Resolution: "r"}); err != nil {
		t.Fatal(err)
	}
	if !runner.saw("queue update q1 --json --note n --state canceled --resolution r") {
		t.Fatalf("update argv: %v", runner.calls[1])
	}
	if _, err := adapter.QueueHandoff(ctx, "q1", HandoffRequest{To: "seat@rig", Note: "n"}); err != nil {
		t.Fatal(err)
	}
	if !runner.saw("queue handoff q1 --to seat@rig --json --note n") {
		t.Fatalf("handoff argv: %v", runner.calls[2])
	}
	if _, err := adapter.QueueResolve(ctx, "q1", "yes"); err != nil {
		t.Fatal(err)
	}
	if !runner.saw("queue resolve q1 --decision yes --json") {
		t.Fatalf("resolve argv: %v", runner.calls[3])
	}
}

func TestSeatsForRigBareArray(t *testing.T) {
	runner := &scriptedRunner{fn: func([]string) ([]byte, error) {
		return fixtureBytes(t, "ps-rig.json"), nil
	}}
	seats, err := NewAdapter(runner).SeatsForRig(context.Background(), "fleet-console")
	if err != nil {
		t.Fatalf("seats: %v", err)
	}
	if len(seats) != 3 {
		t.Fatalf("seats = %d, want 3", len(seats))
	}
	if seats[0].RigID == "" || seats[0].Session == "" || seats[0].Lifecycle != "running" {
		t.Fatalf("seat = %+v", seats[0])
	}
}

func TestParkedFixture(t *testing.T) {
	runner := &scriptedRunner{fn: func([]string) ([]byte, error) {
		return fixtureBytes(t, "parked.json"), nil
	}}
	report, err := NewAdapter(runner).Parked(context.Background(), "fleet-console")
	if err != nil {
		t.Fatalf("parked: %v", err)
	}
	if !report.OK || report.Rig.Parked {
		t.Fatalf("report = %+v", report)
	}
	if len(report.Rig.Seats) == 0 || report.Rig.Seats[0].SessionName == "" {
		t.Fatalf("seats = %+v", report.Rig.Seats)
	}
}

func TestProviderSignalsFixture(t *testing.T) {
	runner := &scriptedRunner{fn: func([]string) ([]byte, error) {
		return fixtureBytes(t, "provider-signals.json"), nil
	}}
	signals, err := NewAdapter(runner).ProviderSignals(context.Background())
	if err != nil {
		t.Fatalf("signals: %v", err)
	}
	if len(signals) == 0 || signals[0].Provider == "" {
		t.Fatalf("signals = %+v", signals)
	}
}

func TestSpecValidateValidAndInvalid(t *testing.T) {
	valid := &scriptedRunner{fn: func([]string) ([]byte, error) {
		return fixtureBytes(t, "spec-validate.json"), nil
	}}
	got, err := NewAdapter(valid).SpecValidate(context.Background(), "/tmp/rig.yaml")
	if err != nil || !got.Valid {
		t.Fatalf("valid spec: %+v, %v", got, err)
	}

	invalid := &scriptedRunner{fn: func([]string) ([]byte, error) {
		return fixtureBytes(t, "spec-validate-invalid.json"), fmt.Errorf("exit status 1")
	}}
	got, err = NewAdapter(invalid).SpecValidate(context.Background(), "/tmp/rig.yaml")
	if err != nil {
		t.Fatalf("invalid spec must decode from stdout, got error: %v", err)
	}
	if got.Valid || len(got.Errors) != 2 {
		t.Fatalf("invalid spec = %+v", got)
	}
}

func TestSpecsSyncFixture(t *testing.T) {
	runner := &scriptedRunner{fn: func([]string) ([]byte, error) {
		return fixtureBytes(t, "specs-sync.json"), nil
	}}
	entries, err := NewAdapter(runner).SpecsSync(context.Background())
	if err != nil {
		t.Fatalf("specs sync: %v", err)
	}
	if len(entries) != 2 || entries[0].Kind != "agent" {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestPlanFixture(t *testing.T) {
	runner := &scriptedRunner{fn: func([]string) ([]byte, error) {
		return fixtureBytes(t, "up-plan.json"), nil
	}}
	report, err := NewAdapter(runner).Plan(context.Background(), "/tmp/rig.yaml")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if report.Status != "planned" || report.RunID == "" {
		t.Fatalf("report = %+v", report)
	}
	if len(report.Stages) < 2 {
		t.Fatalf("stages = %+v", report.Stages)
	}
}

func TestGrowFixture(t *testing.T) {
	runner := &scriptedRunner{fn: func([]string) ([]byte, error) {
		return fixtureBytes(t, "grow.json"), nil
	}}
	report, err := NewAdapter(runner).Grow(context.Background(), "rig-1", "build-3", "dev", "/tmp/wt", "omp")
	if err != nil {
		t.Fatalf("grow: %v", err)
	}
	if !report.OK || report.Seats[0] != "dev.build-3" || report.Nodes[0].Status != "launched" {
		t.Fatalf("report = %+v", report)
	}
}

func TestDownFixture(t *testing.T) {
	runner := &scriptedRunner{fn: func([]string) ([]byte, error) {
		return fixtureBytes(t, "down.json"), nil
	}}
	report, err := NewAdapter(runner).Down(context.Background(), "demo-rig", true)
	if err != nil {
		t.Fatalf("down: %v", err)
	}
	if !report.Deleted || report.SessionsKilled != 3 || len(report.Errors) != 0 {
		t.Fatalf("report = %+v", report)
	}
}

func TestUpFixture(t *testing.T) {
	runner := &scriptedRunner{fn: func([]string) ([]byte, error) {
		return fixtureBytes(t, "up.json"), nil
	}}
	report, err := NewAdapter(runner).Up(context.Background(), "/tmp/rig.yaml")
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	if report.Status != "completed" || report.Stages[0].Detail.RigName != "fc-throwaway-4" {
		t.Fatalf("report = %+v", report)
	}
}

func TestSendArgv(t *testing.T) {
	runner := &scriptedRunner{fn: func([]string) ([]byte, error) { return nil, nil }}
	if err := NewAdapter(runner).Send(context.Background(), "dev-build-1@rig", "hello"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if !runner.saw("send dev-build-1@rig hello") {
		t.Fatalf("send argv: %v", runner.calls)
	}
}
