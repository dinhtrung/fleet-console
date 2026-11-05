package config

import (
	"strings"
	"testing"
	"time"
)

func TestLifecycleDefaultsWhenNoBlock(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "tick_interval: 15s\n")
	eff, err := Load(Options{FlagPath: path, Env: envMap(nil), Home: dir, Cwd: dir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// Written false is the contract the poll cadence reads: no block, no backoff,
	// so this box keeps polling at tick_interval exactly as it did before.
	want := LifecycleSettings{Written: false, Enabled: false, IdleAfter: 10 * time.Minute, IdlePollMax: 10 * time.Minute}
	if eff.RigLifecycle != want {
		t.Fatalf("rig_lifecycle = %+v, want %+v", eff.RigLifecycle, want)
	}
	for _, key := range []string{"rig_lifecycle.enabled", "rig_lifecycle.idle_after", "rig_lifecycle.idle_poll_max"} {
		if eff.Sources[key] != "default" {
			t.Fatalf("sources[%s] = %q, want default", key, eff.Sources[key])
		}
	}
}

func TestLifecycleBlockFromFile(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "tick_interval: 30s\nrig_lifecycle:\n  enabled: true\n  idle_after: 20m\n  idle_poll_max: 8m\n")
	eff, err := Load(Options{FlagPath: path, Env: envMap(nil), Home: dir, Cwd: dir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := LifecycleSettings{Written: true, Enabled: true, IdleAfter: 20 * time.Minute, IdlePollMax: 8 * time.Minute}
	if eff.RigLifecycle != want {
		t.Fatalf("rig_lifecycle = %+v, want %+v", eff.RigLifecycle, want)
	}
	if eff.Sources["rig_lifecycle.enabled"] != "file" || eff.Sources["rig_lifecycle.idle_after"] != "file" ||
		eff.Sources["rig_lifecycle.idle_poll_max"] != "file" {
		t.Fatalf("sources = %+v, want all file", eff.Sources)
	}
	printed, err := eff.Print()
	if err != nil {
		t.Fatalf("print: %v", err)
	}
	for _, line := range []string{"rig_lifecycle:", "enabled: true", "idle_after: 20m0s", "idle_poll_max: 8m0s"} {
		if !strings.Contains(string(printed), line) {
			t.Fatalf("print-config missing %q:\n%s", line, printed)
		}
	}
}

// TestLifecycleCeilingBelowTickIsRefused pins the one refusal: a clamp would
// make the operator's number a lie, so the load fails and names both values.
func TestLifecycleCeilingBelowTickIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "tick_interval: 5m\nrig_lifecycle:\n  enabled: true\n  idle_poll_max: 1m\n")
	_, err := Load(Options{FlagPath: path, Env: envMap(nil), Home: dir, Cwd: dir})
	if err == nil {
		t.Fatal("load accepted idle_poll_max below tick_interval")
	}
	message := err.Error()
	for _, want := range []string{"rig_lifecycle.idle_poll_max", "tick_interval", "1m0s", "5m0s"} {
		if !strings.Contains(message, want) {
			t.Fatalf("error %q does not name %q", message, want)
		}
	}
}

// TestLifecycleCeilingNotCheckedWhenBlockAbsent: an upgraded box with a large
// tick_interval and no rig_lifecycle block must not be refused.
func TestLifecycleCeilingNotCheckedWhenBlockAbsent(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "tick_interval: 1h\n")
	eff, err := Load(Options{FlagPath: path, Env: envMap(nil), Home: dir, Cwd: dir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if eff.RigLifecycle.Enabled {
		t.Fatalf("rig_lifecycle enabled without a block: %+v", eff.RigLifecycle)
	}
}

func TestProjectLifecycleOnOffAndInherit(t *testing.T) {
	dir := t.TempDir()
	body := "rig_lifecycle:\n  enabled: true\nprojects:\n" +
		"  - key: warm\n    forge: github\n    repo: o/a\n    rigs: [r1]\n    lifecycle: off\n" +
		"  - key: cold\n    forge: github\n    repo: o/b\n    rigs: [r2]\n"
	path := writeConfig(t, dir, body)
	eff, err := Load(Options{FlagPath: path, Env: envMap(nil), Home: dir, Cwd: dir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if eff.LifecycleOn("warm") {
		t.Fatal("warm project with lifecycle: off is managed")
	}
	if !eff.LifecycleOn("cold") {
		t.Fatal("project without lifecycle did not inherit enabled")
	}
	if !eff.LifecycleOn("absent") {
		t.Fatal("a project absent from the file must inherit the block")
	}
	printed, err := eff.Print()
	if err != nil {
		t.Fatalf("print: %v", err)
	}
	text := string(printed)
	if !strings.Contains(text, "lifecycles:") || !strings.Contains(text, `lifecycle: "off"`) ||
		!strings.Contains(text, "source: inherit") {
		t.Fatalf("print-config lacks the per-project values:\n%s", text)
	}
}

// TestProjectLifecycleRejectsUnknownValue: anything but on/off is an error, so
// an operator's typo cannot silently mean "on".
func TestProjectLifecycleRejectsUnknownValue(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "projects:\n  - key: a\n    forge: github\n    repo: o/a\n    rigs: [r1]\n    lifecycle: maybe\n")
	_, err := Load(Options{FlagPath: path, Env: envMap(nil), Home: dir, Cwd: dir})
	if err == nil {
		t.Fatal("load accepted lifecycle: maybe")
	}
	if !strings.Contains(err.Error(), "lifecycle must be on or off") {
		t.Fatalf("error = %q, want the strict-decode message", err)
	}
}
