package main

import (
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/config"
)

// The spec's "an existing configuration is unchanged" scenario: a box with no
// rig_lifecycle block must poll at the fixed tick_interval, not at the block's
// 10m default. The gap is 40x on defaults (15s vs 10m), so getting this wrong is
// invisible in a unit test and obvious to an operator watching the board go
// quiet.
func TestPollCadenceWithoutTheBlockIsFixed(t *testing.T) {
	eff := &config.Effective{
		TickInterval: 15 * time.Second,
		// What Load resolves when the file has no rig_lifecycle block: the
		// defaults are filled in, but Written stays false.
		RigLifecycle: config.LifecycleSettings{
			Written: false, Enabled: false,
			IdleAfter: 10 * time.Minute, IdlePollMax: 10 * time.Minute,
		},
	}
	cadence := pollCadence(eff)
	if cadence.Base != 15*time.Second || cadence.Max != 15*time.Second {
		t.Fatalf("cadence = {%v, %v}, want both 15s", cadence.Base, cadence.Max)
	}
	// Every quiet tick must return the same wait, or a block-less box would back
	// off to 10m without anyone asking it to.
	for i := range 20 {
		if got := cadence.Next(true); got != 15*time.Second {
			t.Fatalf("quiet tick %d: wait = %v, want 15s", i+1, got)
		}
	}
}

// With the block written, the ceiling is the operator's number.
func TestPollCadenceWithTheBlockUsesTheCeiling(t *testing.T) {
	eff := &config.Effective{
		TickInterval: 15 * time.Second,
		RigLifecycle: config.LifecycleSettings{
			Written: true, Enabled: true,
			IdleAfter: 10 * time.Minute, IdlePollMax: 2 * time.Minute,
		},
	}
	cadence := pollCadence(eff)
	if cadence.Base != 15*time.Second || cadence.Max != 2*time.Minute {
		t.Fatalf("cadence = {%v, %v}, want {15s, 2m}", cadence.Base, cadence.Max)
	}
	want := []time.Duration{
		15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 2 * time.Minute,
	}
	for i, w := range want {
		if got := cadence.Next(true); got != w {
			t.Fatalf("quiet tick %d: wait = %v, want %v", i+1, got, w)
		}
	}
}
