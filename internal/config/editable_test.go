package config

import (
	"strings"
	"testing"
)

// A key added to File without a decision must fail here rather than become
// silently writable through the settings surface. This walks the struct's own
// tags, so it cannot drift from the shape the way a hand-kept list would.
func TestEveryFileKeyIsClassified(t *testing.T) {
	keys := FileKeys()
	if len(keys) == 0 {
		t.Fatal("FileKeys returned nothing; the reflection walk is broken")
	}
	for _, key := range keys {
		editable, reason := EditableKey(key)
		if editable {
			if reason != "" {
				t.Errorf("%q is editable but carries a refusal reason %q", key, reason)
			}
			continue
		}
		// A read-only key must say why. "unknown key" means the key is not in the
		// classification table at all, which is exactly the slip this catches.
		if strings.HasPrefix(reason, "unknown key") {
			t.Errorf("File declares %q but EditableKey does not classify it: %s", key, reason)
		}
		if reason == "" {
			t.Errorf("%q is read-only with no reason", key)
		}
	}
}

// The four writable scalars and the three lifecycle fields are the whole
// writable surface; anything else must be refused.
func TestEditableKeyAllowsOnlyTheDocumentedSurface(t *testing.T) {
	writable := []string{
		"tick_interval", "notify_interval", "act", "rig_lifecycle",
		"rig_lifecycle.enabled", "rig_lifecycle.idle_after", "rig_lifecycle.idle_poll_max",
	}
	for _, key := range writable {
		if ok, reason := EditableKey(key); !ok {
			t.Errorf("%q should be writable, refused with %q", key, reason)
		}
	}
	refused := map[string]string{
		"listen":                 "boot",
		"database":               "boot",
		"token":                  "boot",
		"token_file":             "boot",
		"rig_binary":             "boot",
		"gh_binary":              "boot",
		"glab_binary":            "boot",
		"project_timeout":        "per-project",
		"projects":               "own route",
		"rig_lifecycle.interval": "not a rig_lifecycle field",
		"nonsense":               "unknown key",
	}
	for key, want := range refused {
		ok, reason := EditableKey(key)
		if ok {
			t.Errorf("%q must be refused", key)
			continue
		}
		if !strings.Contains(reason, want) {
			t.Errorf("%q refused with %q, want it to mention %q", key, reason, want)
		}
	}
}

// FileKeys must name the keys File actually declares, including the nested
// project_timeout and the pointer block, so the walk above has real coverage.
func TestFileKeysNamesTheDeclaredKeys(t *testing.T) {
	got := strings.Join(FileKeys(), ",")
	for _, want := range []string{
		"listen", "database", "token", "token_file", "rig_binary", "gh_binary",
		"glab_binary", "tick_interval", "notify_interval", "project_timeout",
		"act", "rig_lifecycle", "projects",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("FileKeys is missing %q; got %s", want, got)
		}
	}
}
