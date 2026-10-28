package rig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The installer pins OpenRig in stack.lock. If that pin falls below the app's
// runtime-compat floor, the two files disagree about which rig this app speaks
// to; fail before anyone dispatches.
func TestLockSatisfiesFloor(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "stack.lock"))
	if err != nil {
		t.Fatalf("read stack.lock: %v", err)
	}
	pin := ""
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(key) == "openrig" {
			pin = strings.TrimSpace(value)
			break
		}
	}
	if pin == "" {
		t.Fatal("stack.lock has no openrig pin")
	}
	pinned, err := ParseVersion(pin)
	if err != nil {
		t.Fatalf("stack.lock openrig pin %q: %v", pin, err)
	}
	floor, err := ParseVersion(Floor)
	if err != nil {
		t.Fatalf("floor %q: %v", Floor, err)
	}
	if pinned.Less(floor) {
		t.Fatalf("stack.lock pins OpenRig %s, below the runtime-compat floor %s", pin, Floor)
	}
}
