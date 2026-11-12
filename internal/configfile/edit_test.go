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

// The shared fixture: a header comment, a settings key, and three projects
// separated by blank lines — the shape the byte-level promises are about. The
// parts are named so a test can build the expected file from the same bytes.
var (
	editHead = "# fleet-console configuration\n" +
		"# managed by the board\n" +
		"listen: 127.0.0.1:8787\n" +
		"\n" +
		"projects:\n"
	editAlpha = "  - key: alpha\n" +
		"    name: Alpha\n" +
		"    forge: github\n" +
		"    repo: org/alpha\n" +
		"    rigs:\n" +
		"      - rig-a\n"
	editBlank = "\n"
	editBeta  = "  - key: beta\n" +
		"    name: Beta\n" +
		"    forge: gitlab\n" +
		"    repo: group/beta\n" +
		"    rigs:\n" +
		"      - rig-b\n"
	editGamma = "  - key: gamma\n" +
		"    name: Gamma\n" +
		"    forge: github\n" +
		"    repo: org/gamma\n" +
		"    rigs:\n" +
		"      - rig-c\n"
)

func writeEditFixture(t *testing.T, body string) (path, dir string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path, dir
}

func readBytes(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// TestRemoveProjectMiddleEntry: removing the middle entry leaves the header
// comment and the other two entries byte-identical, and takes the one blank
// line that separated the removed entry with it — nothing else moves.
func TestRemoveProjectMiddleEntry(t *testing.T) {
	path, _ := writeEditFixture(t, editHead+editAlpha+editBlank+editBeta+editBlank+editGamma)

	outcome, err := RemoveProject(path, "beta", nil)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if outcome != OutcomeCreated {
		t.Fatalf("outcome = %q, want Created", outcome)
	}
	want := editHead + editAlpha + editBlank + editGamma
	if got := readBytes(t, path); got != want {
		t.Fatalf("the removal rewrote the file:\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
}

// TestRemoveProjectMissingKey: an absent key is ErrNotFound and the file is
// exactly as it was; so is a remove from a file that does not exist.
func TestRemoveProjectMissingKey(t *testing.T) {
	path, dir := writeEditFixture(t, editHead+editAlpha+editBlank+editBeta+editBlank+editGamma)

	if _, err := RemoveProject(path, "delta", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("remove delta = %v, want ErrNotFound", err)
	}
	if got := readBytes(t, path); got != editHead+editAlpha+editBlank+editBeta+editBlank+editGamma {
		t.Fatalf("the refused remove changed the file:\n%s", got)
	}

	if _, err := RemoveProject(filepath.Join(dir, "nope.yaml"), "beta", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("remove from a missing file = %v, want ErrNotFound", err)
	}
}

// TestRemoveProjectStaleRead: a hash that no longer matches is refused and the
// file's sha256 is exactly what the other writer left.
func TestRemoveProjectStaleRead(t *testing.T) {
	path, _ := writeEditFixture(t, editHead+editAlpha+editBlank+editBeta+editBlank+editGamma)
	original := []byte(editHead + editAlpha + editBlank + editBeta + editBlank + editGamma)
	hash := sha256.Sum256(original)

	touched := append([]byte{}, original...)
	touched = append(touched, []byte("\n# modified by another writer\n")...)
	if err := os.WriteFile(path, touched, 0o644); err != nil {
		t.Fatalf("write touched: %v", err)
	}

	if _, err := RemoveProject(path, "beta", &hash); !errors.Is(err, ErrStaleRead) {
		t.Fatalf("remove = %v, want ErrStaleRead", err)
	}
	if now := sha256.Sum256([]byte(readBytes(t, path))); now != sha256.Sum256(touched) {
		t.Fatalf("the refused remove changed the file's bytes")
	}
}

// TestRemoveProjectRefusedByValidation: a removal that would leave an
// unloadable file is a *config.ValidationError and nothing is written (tasks
// 1.5); removing the bad entry itself still works.
func TestRemoveProjectRefusedByValidation(t *testing.T) {
	badForge := "  - key: badfor\n" +
		"    name: Bad\n" +
		"    forge: bitbucket\n" +
		"    repo: org/bad\n" +
		"    rigs:\n" +
		"      - rig-b\n"
	path, _ := writeEditFixture(t, editHead+editAlpha+editBlank+badForge)

	_, err := RemoveProject(path, "alpha", nil)
	var invalid *config.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("remove = %v, want *config.ValidationError", err)
	}
	if message := invalid.Fields()["forge"]; !strings.Contains(message, "forge must be github or gitlab") {
		t.Fatalf("message = %q, want it to name the bad forge", message)
	}
	if got := readBytes(t, path); got != editHead+editAlpha+editBlank+badForge {
		t.Fatalf("the refused remove changed the file:\n%s", got)
	}

	if _, err := RemoveProject(path, "badfor", nil); err != nil {
		t.Fatalf("remove badfor: %v", err)
	}
	if got := readBytes(t, path); got != editHead+editAlpha+editBlank {
		t.Fatalf("removing the bad entry left:\n%s", got)
	}
}

// TestRemoveLastProject: entries 1, 2 and 3 removed in turn; after each the
// loader accepts the file, and the last one leaves `projects: []` — never a
// dangling `projects:` key — with zero projects (tasks 1.2).
func TestRemoveLastProject(t *testing.T) {
	path, dir := writeEditFixture(t, editHead+editAlpha+editBlank+editBeta+editBlank+editGamma)
	wantCounts := []int{2, 1, 0}

	for index, key := range []string{"alpha", "beta", "gamma"} {
		if _, err := RemoveProject(path, key, nil); err != nil {
			t.Fatalf("remove %s: %v", key, err)
		}
		eff, err := config.Load(config.Options{FlagPath: path, Env: func(string) string { return "" }, Home: dir, Cwd: dir})
		if err != nil {
			t.Fatalf("load after removing %s: %v", key, err)
		}
		if len(eff.Projects) != wantCounts[index] {
			t.Fatalf("after removing %s the loader reports %d projects, want %d",
				key, len(eff.Projects), wantCounts[index])
		}
	}

	if got := readBytes(t, path); !strings.Contains(got, "projects: []") {
		t.Fatalf("the emptied file does not carry `projects: []`:\n%s", got)
	}
}

// TestUpdateProjectReplacesInPlace: the entry is replaced in place at its own
// indentation, and the header comment and both sibling entries are
// byte-identical.
func TestUpdateProjectReplacesInPlace(t *testing.T) {
	path, dir := writeEditFixture(t, editHead+editAlpha+editBlank+editBeta+editBlank+editGamma)

	in := Project{
		Key: "beta", Name: "Beta Prime", Forge: "gitlab", Repo: "group/beta",
		DefaultBranch: "main", Rigs: []string{"rig-b"}, RigTemplate: "default",
		ConcurrencyCap: 3, Labels: map[string]string{},
	}
	outcome, err := UpdateProject(path, in, nil)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if outcome != OutcomeCreated {
		t.Fatalf("outcome = %q, want Created", outcome)
	}

	entry, err := encodeEntry(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	want := editHead + editAlpha + editBlank + indentEntry(entry, "  ") + editBlank + editGamma
	if got := readBytes(t, path); got != want {
		t.Fatalf("the update rewrote the file:\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}

	// And the loader reads the new value back.
	eff, err := config.Load(config.Options{FlagPath: path, Env: func(string) string { return "" }, Home: dir, Cwd: dir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(eff.Projects) != 3 || eff.Projects[1].Name != "Beta Prime" || eff.Projects[1].ConcurrencyCap != 3 {
		t.Fatalf("loaded = %+v, want the updated middle entry", eff.Projects)
	}
}

// TestUpdateProjectUnchanged: an identical entry is OutcomeUnchanged, the file
// is not rewritten, and the siblings stay byte-identical.
func TestUpdateProjectUnchanged(t *testing.T) {
	path, _ := writeEditFixture(t, editHead+editAlpha+editBlank+editBeta+editBlank+editGamma)

	in := Project{
		Key: "beta", Name: "Beta Renamed", Forge: "gitlab", Repo: "group/beta",
		DefaultBranch: "main", Rigs: []string{"rig-b"}, RigTemplate: "default",
		ConcurrencyCap: 5, Labels: map[string]string{},
	}
	if _, err := UpdateProject(path, in, nil); err != nil {
		t.Fatalf("first update: %v", err)
	}
	after := readBytes(t, path)

	outcome, err := UpdateProject(path, in, nil)
	if err != nil {
		t.Fatalf("second update: %v", err)
	}
	if outcome != OutcomeUnchanged {
		t.Fatalf("outcome = %q, want Unchanged", outcome)
	}
	if got := readBytes(t, path); got != after {
		t.Fatalf("the unchanged update rewrote the file:\n%s", got)
	}
}

// TestUpdateProjectMissingKey: an absent key is ErrNotFound and nothing is
// written.
func TestUpdateProjectMissingKey(t *testing.T) {
	path, _ := writeEditFixture(t, editHead+editAlpha+editBlank+editBeta+editBlank+editGamma)

	in := Project{Key: "delta", Name: "Delta", Forge: "github", Repo: "org/delta", Rigs: []string{"rig-d"}}
	if _, err := UpdateProject(path, in, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update delta = %v, want ErrNotFound", err)
	}
	if got := readBytes(t, path); got != editHead+editAlpha+editBlank+editBeta+editBlank+editGamma {
		t.Fatalf("the refused update changed the file:\n%s", got)
	}
}

// TestUpdateProjectStaleRead: a moved file is refused and its sha256 is
// exactly what the other writer left.
func TestUpdateProjectStaleRead(t *testing.T) {
	path, _ := writeEditFixture(t, editHead+editAlpha+editBlank+editBeta+editBlank+editGamma)
	original := []byte(editHead + editAlpha + editBlank + editBeta + editBlank + editGamma)
	hash := sha256.Sum256(original)

	touched := append([]byte{}, original...)
	touched = append(touched, []byte("\n# modified by another writer\n")...)
	if err := os.WriteFile(path, touched, 0o644); err != nil {
		t.Fatalf("write touched: %v", err)
	}

	in := Project{Key: "beta", Name: "Beta Prime", Forge: "gitlab", Repo: "group/beta", Rigs: []string{"rig-b"}}
	if _, err := UpdateProject(path, in, &hash); !errors.Is(err, ErrStaleRead) {
		t.Fatalf("update = %v, want ErrStaleRead", err)
	}
	if now := sha256.Sum256([]byte(readBytes(t, path))); now != sha256.Sum256(touched) {
		t.Fatalf("the refused update changed the file's bytes")
	}
}

// TestUpdateProjectRefusedByValidation: the whole merged document is checked
// before the write, so an update that would steal a sibling's rig is refused
// with the original bytes intact and no temp file left behind (tasks 1.4).
func TestUpdateProjectRefusedByValidation(t *testing.T) {
	path, dir := writeEditFixture(t, editHead+editAlpha+editBlank+editBeta+editBlank+editGamma)

	in := Project{Key: "alpha", Name: "Alpha", Forge: "github", Repo: "org/alpha", Rigs: []string{"rig-b"}}
	_, err := UpdateProject(path, in, nil)
	var invalid *config.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("update = %v, want *config.ValidationError", err)
	}
	if message := invalid.Fields()["rigs"]; !strings.Contains(message, `rig "rig-b" is assigned to both`) {
		t.Fatalf("message = %q, want it to name the rig and both projects", message)
	}
	if got := readBytes(t, path); got != editHead+editAlpha+editBlank+editBeta+editBlank+editGamma {
		t.Fatalf("the refused update changed the file:\n%s", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() != "config.yaml" {
			t.Fatalf("temp file left behind: %s", entry.Name())
		}
	}
}
