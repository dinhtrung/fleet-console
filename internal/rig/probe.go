package rig

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
)

// QueueShape is the parsed shape of `rig queue list -A -a --json`.
type QueueShape struct {
	Rows   int       `json:"rows"`
	Sample *QueueRow `json:"sample,omitempty"`
}

// SeatsShape is the parsed shape of `rig ps --nodes --full --json`.
type SeatsShape struct {
	Seats  int   `json:"seats"`
	Sample *Seat `json:"sample,omitempty"`
}

// Report is the compat probe result: boot check, GET /api/compat and
// `fleet-console --compat` all print this value.
type Report struct {
	OK         bool        `json:"ok"`
	Version    string      `json:"version,omitempty"`
	VersionRaw string      `json:"version_raw,omitempty"`
	Floor      string      `json:"floor"`
	VersionOK  bool        `json:"version_ok"`
	Error      string      `json:"error,omitempty"`
	Queue      *QueueShape `json:"queue,omitempty"`
	Seats      *SeatsShape `json:"seats,omitempty"`
}

// Probe checks a rig CLI against the floor and the supported JSON shapes.
type Probe struct {
	runner Runner
	logf   func(format string, args ...any)
	mu     sync.Mutex
	logged map[string]struct{}
}

// NewProbe returns a probe. A nil logger writes to stderr.
func NewProbe(runner Runner, logf func(format string, args ...any)) *Probe {
	if logf == nil {
		logger := log.New(os.Stderr, "", 0)
		logf = logger.Printf
	}
	return &Probe{runner: runner, logf: logf, logged: make(map[string]struct{})}
}

// Run executes the version check and the two required reads. It never returns
// a raw payload; the report is the whole contract.
func (p *Probe) Run(ctx context.Context) Report {
	report := Report{Floor: Floor}

	raw, err := p.runner.Run(ctx, "--version")
	if err != nil {
		report.Error = fmt.Sprintf("rig --version: %v", err)
		return report
	}
	report.VersionRaw = strings.TrimSpace(string(raw))
	version, err := ParseVersion(report.VersionRaw)
	if err != nil {
		report.Error = fmt.Sprintf("rig --version: %v", err)
		return report
	}
	report.Version = version.String()
	floor, err := ParseVersion(Floor)
	if err != nil {
		report.Error = fmt.Sprintf("rig floor: %v", err)
		return report
	}
	report.VersionOK = !version.Less(floor)
	if !report.VersionOK {
		report.Error = fmt.Sprintf("rig %s is below the floor %s; reads refused and the act loop stays off", report.Version, Floor)
		return report
	}

	unknown := func(key string) { p.logUnknown(report.Version, key) }

	data, err := p.runner.Run(ctx, "queue", "list", "-A", "-a", "--json")
	if err != nil {
		report.Error = fmt.Sprintf("rig queue list: %v", err)
		return report
	}
	rows, err := ParseQueueList(data, unknown)
	if err != nil {
		report.Error = err.Error()
		return report
	}
	report.Queue = &QueueShape{Rows: len(rows)}
	if len(rows) > 0 {
		report.Queue.Sample = &rows[0]
	}

	data, err = p.runner.Run(ctx, "ps", "--nodes", "--full", "--json")
	if err != nil {
		report.Error = fmt.Sprintf("rig ps --nodes: %v", err)
		return report
	}
	seats, err := ParseSeats(data, unknown)
	if err != nil {
		report.Error = err.Error()
		return report
	}
	report.Seats = &SeatsShape{Seats: len(seats)}
	if len(seats) > 0 {
		report.Seats.Sample = &seats[0]
	}

	report.OK = true
	return report
}

func (p *Probe) logUnknown(version, key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	id := version + "\x00" + key
	if _, ok := p.logged[id]; ok {
		return
	}
	p.logged[id] = struct{}{}
	p.logf("rig compat: unknown key %q in rig %s", key, version)
}
