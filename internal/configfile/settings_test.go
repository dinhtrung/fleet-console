package configfile

import (
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/config"
)

const settingsFixture = `# fleet-console settings. Hand-written header comment.
# A second line that must survive the splice.

listen: "127.0.0.1:8787"
database: "fleet-console.db"
token: "secret-token-value"

# Poll cadence. Tuned for a quiet box.
tick_interval: "15s"
notify_interval: "0s"
act: false

rig_lifecycle:
  # The opt-in the owner turned on deliberately.
  enabled: true
  idle_after: "10m"
  idle_poll_max: "10m"

projects:
  # The one project this box runs.
  - key: "app"
    name: "App"
    forge: "github"
    repo: "owner/app"
    default_branch: "main"
    rigs:
      - "rig-1"
    concurrency_cap: 2
`

func writeFixture(t *testing.T) (string, *[32]byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(settingsFixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	hash := sha256.Sum256([]byte(settingsFixture))
	return path, &hash
}

// The splice must keep the operator's header comment, an inline comment, the
// lifecycle block's own comment and the untouched project block, while changing
// only the values asked for.
func TestUpdateSettingsKeepsCommentsAndProjects(t *testing.T) {
	path, hash := writeFixture(t)
	outcome, err := UpdateSettings(path, map[string]string{
		"tick_interval":            "30s",
		"rig_lifecycle.idle_after": "20m",
	}, hash)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if outcome != OutcomeCreated {
		t.Fatalf("outcome = %q, want Created", outcome)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	text := string(got)

	for _, want := range []string{
		"# fleet-console settings. Hand-written header comment.",
		"# A second line that must survive the splice.",
		"# Poll cadence. Tuned for a quiet box.",
		"# The opt-in the owner turned on deliberately.",
		"# The one project this box runs.",
		`tick_interval: "30s"`,
		`idle_after: "20m"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("spliced file lost %q:\n%s", want, text)
		}
	}
	// The untouched values and the whole project block are byte-identical.
	if !strings.Contains(text, `idle_poll_max: "10m"`) {
		t.Errorf("idle_poll_max changed:\n%s", text)
	}
	if !strings.Contains(text, `    repo: "owner/app"`) {
		t.Errorf("project block changed:\n%s", text)
	}
	if !strings.Contains(text, `notify_interval: "0s"`) {
		t.Errorf("notify_interval changed:\n%s", text)
	}

	// And the loader accepts the merged document, with the new values.
	var file config.File
	if err := config.ValidateContent(got, filepath.Dir(path), ""); err != nil {
		t.Fatalf("merged document does not load: %v", err)
	}
	_ = file
}

// A stale hash must refuse the write and leave the file byte-identical.
func TestUpdateSettingsRefusesStaleRead(t *testing.T) {
	path, _ := writeFixture(t)
	stale := sha256.Sum256([]byte("a different document"))
	before, _ := os.ReadFile(path)

	_, err := UpdateSettings(path, map[string]string{"act": "true"}, &stale)
	if !errors.Is(err, ErrStaleRead) {
		t.Fatalf("err = %v, want ErrStaleRead", err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("the file changed despite the stale-read refusal")
	}
}

// A value the loader refuses must not reach the disk. A malformed duration is
// the cheapest realistic case.
func TestUpdateSettingsRefusesInvalidDocument(t *testing.T) {
	path, hash := writeFixture(t)
	before, _ := os.ReadFile(path)

	_, err := UpdateSettings(path, map[string]string{"tick_interval": "not-a-duration"}, hash)
	if err == nil {
		t.Fatal("an invalid duration was accepted")
	}
	var validation *config.ValidationError
	if !errors.As(err, &validation) && !strings.Contains(err.Error(), "duration") {
		t.Fatalf("err = %v, want a validation failure naming the duration", err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("the file changed despite the validation refusal")
	}
}

// Saving the same values twice must report Unchanged and not touch the file.
func TestUpdateSettingsUnchangedIsNoWrite(t *testing.T) {
	path, hash := writeFixture(t)
	info, _ := os.Stat(path)

	outcome, err := UpdateSettings(path, map[string]string{"tick_interval": "15s"}, hash)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if outcome != OutcomeUnchanged {
		t.Fatalf("outcome = %q, want Unchanged", outcome)
	}
	after, _ := os.Stat(path)
	if !after.ModTime().Equal(info.ModTime()) {
		t.Fatal("the file was rewritten although nothing changed")
	}
}

// An unwritable key is refused before the file is read, so a handler bug cannot
// reach the disk even with a matching hash.
func TestUpdateSettingsRefusesReadOnlyKey(t *testing.T) {
	path, hash := writeFixture(t)
	before, _ := os.ReadFile(path)

	for _, key := range []string{"listen", "database", "token", "projects", "project_timeout"} {
		_, err := UpdateSettings(path, map[string]string{key: "x"}, hash)
		if err == nil {
			t.Fatalf("%q was accepted", key)
		}
		if !strings.Contains(err.Error(), key) {
			t.Errorf("refusal for %q does not name it: %v", key, err)
		}
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("the file changed despite the read-only refusals")
	}
}

// A key the file does not carry is appended, and the appended document loads.
func TestUpdateSettingsAppendsMissingKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("listen: \"127.0.0.1:8787\"\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	_, err := UpdateSettings(path, map[string]string{"act": "true"}, nil)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "act: true") {
		t.Fatalf("missing key not appended:\n%s", got)
	}
	if err := config.ValidateContent(got, filepath.Dir(path), ""); err != nil {
		t.Fatalf("appended document does not load: %v", err)
	}
}

// A fresh file is created with the settings and loads.
func TestUpdateSettingsCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	outcome, err := UpdateSettings(path, map[string]string{"act": "true", "tick_interval": "5s"}, nil)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if outcome != OutcomeCreated {
		t.Fatalf("outcome = %q, want Created", outcome)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if err := config.ValidateContent(got, filepath.Dir(path), ""); err != nil {
		t.Fatalf("fresh document does not load:\n%s\n%v", got, err)
	}
}

// The loader enforces rig_lifecycle.idle_poll_max >= tick_interval in Load, not
// in ValidateContent. A settings write must honour that cross-check too, or it
// would leave a file the app refuses to boot from. This is the case the task's
// evidence list names.
func TestUpdateSettingsRefusesPollMaxBelowTick(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	fixture := `listen: "127.0.0.1:8787"
tick_interval: "30s"
rig_lifecycle:
  enabled: true
  idle_poll_max: "30s"
`
	if err := os.WriteFile(path, []byte(fixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	hash := sha256.Sum256([]byte(fixture))
	before, _ := os.ReadFile(path)

	_, err := UpdateSettings(path, map[string]string{"rig_lifecycle.idle_poll_max": "5s"}, &hash)
	if err == nil {
		t.Fatal("idle_poll_max below tick_interval was accepted")
	}
	if !strings.Contains(err.Error(), "idle_poll_max") {
		t.Errorf("refusal does not name idle_poll_max: %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("the file changed despite the cross-check refusal")
	}
	// The temp validation file must not survive the refusal.
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "settings-validate") {
			t.Errorf("validation temp file left behind: %s", entry.Name())
		}
	}
}

// An UNQUOTED scalar is the case the first version of this splice got wrong: its
// valueSpan reported "not found" for it, so spliceSettings appended a duplicate
// key instead of replacing in place, and the loader refused the merged document
// with "mapping key tick_interval already defined". Every value in the main
// fixture is quoted, which is why the suite missed it. This fixture is unquoted
// end to end, the way a hand-written config usually is.
func TestUpdateSettingsReplacesUnquotedScalarsInPlace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	fixture := `# A hand-written file with no quotes anywhere.
listen: 127.0.0.1:8787
database: fleet-console.db
tick_interval: 15s
act: false
rig_lifecycle:
  enabled: true
  idle_after: 10m
  idle_poll_max: 10m
`
	if err := os.WriteFile(path, []byte(fixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	hash := sha256.Sum256([]byte(fixture))

	outcome, err := UpdateSettings(path, map[string]string{
		"tick_interval":            "30s",
		"rig_lifecycle.idle_after": "20m",
	}, &hash)
	if err != nil {
		t.Fatalf("update refused an unquoted file: %v", err)
	}
	if outcome != OutcomeCreated {
		t.Fatalf("outcome = %q, want Created", outcome)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	text := string(got)

	// The duplicate key is the bug: the loader's own complaint names it.
	if strings.Count(text, "tick_interval:") != 1 {
		t.Fatalf("tick_interval appears %d times; the splice appended instead of replacing:\n%s",
			strings.Count(text, "tick_interval:"), text)
	}
	if !strings.Contains(text, "tick_interval: 30s") {
		t.Errorf("the value was not replaced:\n%s", text)
	}
	if !strings.Contains(text, "idle_after: 20m") {
		t.Errorf("the nested unquoted value was not replaced:\n%s", text)
	}
	// An unquoted file stays unquoted: the splice must not add quotes.
	if strings.Contains(text, `"30s"`) || strings.Contains(text, `"20m"`) {
		t.Errorf("the splice introduced quotes into an unquoted file:\n%s", text)
	}
	if !strings.Contains(text, "# A hand-written file with no quotes anywhere.") {
		t.Errorf("the header comment was lost:\n%s", text)
	}
	if !strings.Contains(text, "act: false") {
		t.Errorf("an untouched bool changed:\n%s", text)
	}
	if err := config.ValidateContent(got, dir, ""); err != nil {
		t.Fatalf("merged document does not load:\n%s\n%v", text, err)
	}
}
