// Package quota raises one decision when two seats have lost their provider signal
// within the window, and pauses dispatch to that rig. The source is
// `rig provider signals`.
package quota

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// Window is the stall window.
const Window = 5 * time.Minute

// Finding is the one decision a quota breach raises.
type Finding struct {
	Rig      string
	Seats    []string
	Question string
}

// Detector reads signals, owns the once-per-window rule and pauses the project.
type Detector struct {
	Store *store.Store
	Now   func() time.Time
}

func (d *Detector) now() time.Time {
	if d.Now != nil {
		return d.Now().UTC()
	}
	return time.Now().UTC()
}

// StalledSeats returns the seats whose newest signal is unknown inside the window.
func StalledSeats(signals []rig.ProviderSignal, now time.Time) []string {
	newest := map[string]time.Time{}
	stalled := map[string]bool{}
	for _, signal := range signals {
		if signal.SeatSession == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, signal.AsOf)
		if err != nil {
			if at, err = time.Parse(time.RFC3339, signal.AsOf); err != nil {
				continue
			}
		}
		if at.After(newest[signal.SeatSession]) {
			newest[signal.SeatSession] = at
		}
		if at.After(now.Add(-Window)) && strings.EqualFold(signal.SourceClass, "unknown") {
			stalled[signal.SeatSession] = true
		}
	}
	seats := make([]string, 0, len(stalled))
	for seat := range stalled {
		seats = append(seats, seat)
	}
	sort.Strings(seats)
	return seats
}

// Check raises the decision and pause once per window. The second call in the same
// window returns the existing finding with raised=false and writes nothing.
func (d *Detector) Check(ctx context.Context, projectID int64, rigName string, signals []rig.ProviderSignal) (*Finding, bool, error) {
	now := d.now()
	seats := StalledSeats(signals, now)
	if len(seats) < 2 {
		return nil, false, nil
	}
	previous, err := d.Store.EventByKindAndSubject(ctx, "quota.decision", rigName)
	if err != nil {
		return nil, false, err
	}
	finding := &Finding{
		Rig:   rigName,
		Seats: seats,
		Question: fmt.Sprintf("%s: %d seats have no provider signal within 5 minutes (%s). Dispatch is paused for this rig.",
			rigName, len(seats), strings.Join(seats, ", ")),
	}
	if previous != nil {
		at, err := time.Parse(time.RFC3339Nano, previous.At)
		if err != nil {
			at, _ = time.Parse(time.RFC3339, previous.At)
		}
		if !at.IsZero() && now.Sub(at) < Window {
			return finding, false, nil
		}
	}
	if _, err := d.Store.SetProjectOnHold(ctx, projectID, true); err != nil {
		return nil, false, err
	}
	if _, err := d.Store.AppendEvent(ctx, store.Event{
		Actor: "quota", Kind: "quota.decision", Subject: rigName, Detail: finding.Question,
	}); err != nil {
		return nil, false, err
	}
	if _, err := d.Store.AppendEvent(ctx, store.Event{
		Actor: "quota", Kind: "quota.pause", Subject: rigName, Detail: "dispatch paused for this rig",
	}); err != nil {
		return nil, false, err
	}
	return finding, true, nil
}
