package rig

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type fakeRunner map[string][]byte

func (f fakeRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	data, ok := f[strings.Join(args, " ")]
	if !ok {
		return nil, &os.PathError{Op: "rig", Path: strings.Join(args, " "), Err: os.ErrNotExist}
	}
	return data, nil
}

type logCounter struct {
	mu sync.Mutex
	n  int
}

func (l *logCounter) logf(string, ...any) {
	l.mu.Lock()
	l.n++
	l.mu.Unlock()
}

func (l *logCounter) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.n
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", Floor, name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func recordedProbes(t *testing.T) fakeRunner {
	t.Helper()
	return fakeRunner{
		"--version":                []byte("0.7.0 (c99450f3)\n"),
		"queue list -A -a --json":  fixture(t, "queue-list.json"),
		"ps --nodes --full --json": fixture(t, "ps-nodes.json"),
	}
}

func TestProbeParsesRecordedFixtures(t *testing.T) {
	logs := &logCounter{}
	report := NewProbe(recordedProbes(t), logs.logf).Run(context.Background())

	if !report.OK {
		t.Fatalf("report not ok: %s", report.Error)
	}
	if report.Status != StatusOK {
		t.Fatalf("status = %q, want %q", report.Status, StatusOK)
	}
	if report.Version != "0.7.0" || !report.VersionOK {
		t.Fatalf("version = %q ok=%v, want 0.7.0 at or above floor", report.Version, report.VersionOK)
	}
	if report.Queue == nil || report.Queue.Rows != 100 {
		t.Fatalf("queue rows = %+v, want 100", report.Queue)
	}
	if report.Queue.Sample == nil || report.Queue.Sample.Destination == "" || report.Queue.Sample.PickupState == "" {
		t.Fatalf("queue sample is incomplete: %+v", report.Queue.Sample)
	}
	if report.Seats == nil || report.Seats.Seats != 3 {
		t.Fatalf("seats = %+v, want 3", report.Seats)
	}
	if report.Seats.Sample == nil || report.Seats.Sample.Session == "" || report.Seats.Sample.NodeID == "" {
		t.Fatalf("seat sample is incomplete: %+v", report.Seats.Sample)
	}
	if got := logs.count(); got != 0 {
		t.Fatalf("recorded fixture logged %d unknown keys, want 0", got)
	}
}

func TestProbeFailsWhenPickupStateIsRenamed(t *testing.T) {
	runner := recordedProbes(t)
	renamed := strings.ReplaceAll(string(fixture(t, "queue-list.json")), `"pickup":{"state":`, `"pickup":{"pickupState":`)
	runner["queue list -A -a --json"] = []byte(renamed)

	report := NewProbe(runner, func(string, ...any) {}).Run(context.Background())
	if report.OK {
		t.Fatal("report is ok with a renamed pickup.state")
	}
	if report.Status != StatusBlocked {
		t.Fatalf("status = %q, want %q", report.Status, StatusBlocked)
	}
	if !strings.Contains(report.Error, "pickup_state") {
		t.Fatalf("error = %q, want it to name pickup_state", report.Error)
	}
	if report.Queue != nil {
		t.Fatalf("queue shape = %+v, want none after a failed check", report.Queue)
	}
}

func TestProbeLogsUnknownKeyOncePerVersion(t *testing.T) {
	runner := recordedProbes(t)
	runner["queue list -A -a --json"] = []byte(strings.Replace(
		string(fixture(t, "queue-list.json")), `"qitemId"`, `"newReleaseField":1,"qitemId"`, 1))

	logs := &logCounter{}
	probe := NewProbe(runner, logs.logf)
	if report := probe.Run(context.Background()); !report.OK {
		t.Fatalf("unknown key must not fail the read: %s", report.Error)
	}
	if report := probe.Run(context.Background()); !report.OK {
		t.Fatalf("second read failed: %s", report.Error)
	}
	if got := logs.count(); got != 1 {
		t.Fatalf("unknown key logged %d times, want 1", got)
	}
}

func TestProbeRefusesRigBelowFloor(t *testing.T) {
	runner := fakeRunner{"--version": []byte("0.6.9 (deadbee)\n")}
	report := NewProbe(runner, func(string, ...any) {}).Run(context.Background())
	if report.OK || report.VersionOK {
		t.Fatalf("old rig reported ok: %+v", report)
	}
	if !strings.Contains(report.Error, "below the floor 0.7.0") {
		t.Fatalf("error = %q, want the floor named", report.Error)
	}
	if report.Queue != nil || report.Seats != nil {
		t.Fatalf("old rig must not be read: %+v", report)
	}
}

func TestParseVersion(t *testing.T) {
	got, err := ParseVersion("0.7.4 (abc1234)")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.String() != "0.7.4" {
		t.Fatalf("version = %s, want 0.7.4", got)
	}
	if _, err := ParseVersion("rig 7"); err == nil {
		t.Fatal("unparseable version: want error")
	}
	if !(Version{0, 7, 0}).Less(Version{0, 7, 4}) {
		t.Fatal("0.7.0 must be less than 0.7.4")
	}
}
