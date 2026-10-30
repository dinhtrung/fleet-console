package culture

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func shaOf(t *testing.T, path string) string {
	t.Helper()
	snapshot, err := Read(Target{Path: path, Source: SourceDerived, Root: filepath.Dir(path)})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return snapshot.SHA256
}

func TestResolveConfigBeatsDerived(t *testing.T) {
	specs := t.TempDir()
	project := Project{Rigs: []string{"fleet-console"}, CultureFile: filepath.Join(specs, "custom", "RULES.md")}

	target := Resolve(project, specs)
	if target.Source != SourceConfig || target.Path != project.CultureFile || target.Rig != "fleet-console" {
		t.Fatalf("target = %+v, want the configured path", target)
	}
	if target.Root != filepath.Dir(project.CultureFile) {
		t.Fatalf("root = %q, want the configured file's directory", target.Root)
	}
}

func TestResolveDerivedFromFirstRig(t *testing.T) {
	specs := t.TempDir()
	target := Resolve(Project{Rigs: []string{"second", "first"}}, specs)
	want := filepath.Join(specs, "rigs", "second", "CULTURE.md")
	if target.Source != SourceDerived || target.Path != want || target.Rig != "second" {
		t.Fatalf("target = %+v, want %s from the first rig", target, want)
	}
	if target.Root != filepath.Dir(want) {
		t.Fatalf("root = %q, want the rig spec dir", target.Root)
	}
}

func TestResolveNotConfigured(t *testing.T) {
	target := Resolve(Project{}, t.TempDir())
	if target.Source != SourceNotConfigured || target.Path != "" || target.Rig != "" {
		t.Fatalf("target = %+v, want a not_configured target with no path", target)
	}
	if _, err := Read(target); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Read = %v, want ErrNotConfigured", err)
	}
	if _, err := Write(target, "x", nil); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Write = %v, want ErrNotConfigured", err)
	}
}

// TestResolveSpecsRoot: an explicit argument wins, then OPENRIG_SPECS_DIR, then
// the documented default, and ~ expands against the home directory.
func TestResolveSpecsRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	t.Setenv(SpecsDirEnv, filepath.Join(home, "elsewhere"))
	target := Resolve(Project{Rigs: []string{"rig-a"}}, "")
	if want := filepath.Join(home, "elsewhere", "rigs", "rig-a", "CULTURE.md"); target.Path != want {
		t.Fatalf("path = %q, want %q from %s", target.Path, want, SpecsDirEnv)
	}

	t.Setenv(SpecsDirEnv, "")
	target = Resolve(Project{Rigs: []string{"rig-a"}}, "~/specs")
	if want := filepath.Join(home, "specs", "rigs", "rig-a", "CULTURE.md"); target.Path != want {
		t.Fatalf("path = %q, want %q with ~ expanded", target.Path, want)
	}

	if got := SpecsDir(""); got != filepath.Join(home, ".openrig", "specs") {
		t.Fatalf("SpecsDir() = %q, want the default under home", got)
	}
}

// TestReadExisting: the sha is over the bytes on disk, so it matches what an
// independent sha256sum would print.
func TestReadExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CULTURE.md")
	content := "# a rig culture\n\n- be plain\n"
	writeFile(t, path, content)

	snapshot, err := Read(Target{Path: path, Source: SourceDerived, Root: dir})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !snapshot.Exists || !snapshot.Writable {
		t.Fatalf("snapshot = %+v, want exists and writable", snapshot)
	}
	if snapshot.Content != content || snapshot.Bytes != int64(len(content)) {
		t.Fatalf("snapshot = %+v, want the file's bytes", snapshot)
	}
	if snapshot.SHA256 != shaOf(t, path) || len(snapshot.SHA256) != 64 {
		t.Fatalf("sha = %q, want the sha256 of the bytes", snapshot.SHA256)
	}
	if snapshot.MTime.IsZero() || snapshot.MTime.Location() != time.UTC {
		t.Fatalf("mtime = %v, want a UTC stamp", snapshot.MTime)
	}
}

func TestReadMissingFileIsNormal(t *testing.T) {
	dir := t.TempDir()
	snapshot, err := Read(Target{Path: filepath.Join(dir, "CULTURE.md"), Source: SourceDerived, Root: dir})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if snapshot.Exists || snapshot.Content != "" || snapshot.SHA256 != "" {
		t.Fatalf("snapshot = %+v, want an absent file with no error", snapshot)
	}
}

func TestReadRefusesTooLarge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CULTURE.md")
	writeFile(t, path, strings.Repeat("a", MaxBytes+1))

	_, err := Read(Target{Path: path, Source: SourceDerived, Root: dir})
	var size *SizeError
	if !errors.As(err, &size) || !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want a SizeError", err)
	}
	if size.Limit != MaxBytes || size.Bytes != MaxBytes+1 {
		t.Fatalf("size = %+v, want the real bytes and the limit", size)
	}

	// Exactly at the limit is allowed: the ceiling is 256 KiB, not 256 KiB minus one.
	writeFile(t, path, strings.Repeat("a", MaxBytes))
	if _, err := Read(Target{Path: path, Source: SourceDerived, Root: dir}); err != nil {
		t.Fatalf("read at the limit: %v", err)
	}
}

func TestGuardRefusesSymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "ELSEWHERE.md")
	writeFile(t, outside, "someone else's file\n")
	link := filepath.Join(root, "CULTURE.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	err := Guard(Target{Path: link, Source: SourceDerived, Root: root})
	var refused *RefusedError
	if !errors.As(err, &refused) || !errors.Is(err, ErrRefused) {
		t.Fatalf("guard = %v, want a RefusedError", err)
	}
	if refused.Resolved != outside {
		t.Fatalf("resolved = %q, want the link target %q", refused.Resolved, outside)
	}
}

// TestGuardRefusesEscape: the file itself is regular, but its directory is a link
// out of the rig's spec dir, so a write would land somewhere else.
func TestGuardRefusesEscape(t *testing.T) {
	rigDir := filepath.Join(t.TempDir(), "rigs", "rig-a")
	if err := os.MkdirAll(rigDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(rigDir, "linked")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	path := filepath.Join(rigDir, "linked", "CULTURE.md")

	err := Guard(Target{Path: path, Source: SourceDerived, Root: rigDir})
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("guard = %v, want ErrRefused for a directory that escapes", err)
	}
}

func TestGuardAllowsTheResolvedPath(t *testing.T) {
	dir := t.TempDir()
	target := Target{Path: filepath.Join(dir, "CULTURE.md"), Source: SourceDerived, Root: dir}
	if err := Guard(target); err != nil {
		t.Fatalf("guard a missing file: %v", err)
	}
	writeFile(t, target.Path, "content\n")
	if err := Guard(target); err != nil {
		t.Fatalf("guard an existing file: %v", err)
	}
}

func TestWriteCreatesWithNilBase(t *testing.T) {
	dir := t.TempDir()
	target := Target{Path: filepath.Join(dir, "CULTURE.md"), Source: SourceDerived, Root: dir}

	result, err := Write(target, "# new culture\n", nil)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if result.Bytes != int64(len("# new culture\n")) || result.Backup != "" {
		t.Fatalf("result = %+v, want 13 bytes and no backup on create", result)
	}
	data, err := os.ReadFile(target.Path)
	if err != nil || string(data) != "# new culture\n" {
		t.Fatalf("file = %q (%v)", data, err)
	}
	info, err := os.Stat(target.Path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != defaultMode {
		t.Fatalf("mode = %04o, want %04o for a created file", info.Mode().Perm(), defaultMode)
	}
	if result.SHA256 != shaOf(t, target.Path) {
		t.Fatalf("sha = %q, want the sha of what was written", result.SHA256)
	}
	assertNoTempFiles(t, dir)
}

func TestWriteRefusesNoFileWithABase(t *testing.T) {
	dir := t.TempDir()
	target := Target{Path: filepath.Join(dir, "CULTURE.md"), Source: SourceDerived, Root: dir}
	base := strings.Repeat("0", 64)

	if _, err := Write(target, "content", &base); !errors.Is(err, ErrNoFile) {
		t.Fatalf("write = %v, want ErrNoFile for a base on a missing file", err)
	}
	if _, err := os.Stat(target.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the refused write created the file")
	}
}

// TestWriteRefusesStale: the answer carries the current sha and mtime so the
// screen can offer "load latest" without asking again.
func TestWriteRefusesStale(t *testing.T) {
	dir := t.TempDir()
	target := Target{Path: filepath.Join(dir, "CULTURE.md"), Source: SourceDerived, Root: dir}
	writeFile(t, target.Path, "current content\n")
	current := shaOf(t, target.Path)

	base := strings.Repeat("f", 64)
	_, err := Write(target, "new content\n", &base)
	var stale *StaleError
	if !errors.As(err, &stale) || !errors.Is(err, ErrStale) {
		t.Fatalf("write = %v, want a StaleError", err)
	}
	if stale.SHA != current || stale.MTime.IsZero() || stale.Path != target.Path {
		t.Fatalf("stale = %+v, want the current sha and mtime", stale)
	}
	data, _ := os.ReadFile(target.Path)
	if string(data) != "current content\n" {
		t.Fatalf("file = %q, want it untouched", data)
	}

	// A matching base succeeds.
	if _, err := Write(target, "new content\n", &current); err != nil {
		t.Fatalf("write with the current sha: %v", err)
	}
}

// TestWriteRefusesReadOnlyFile: 0444 is not writable even though the directory
// would allow replacing it, because the mode is the operator's statement.
func TestWriteRefusesReadOnlyFile(t *testing.T) {
	dir := t.TempDir()
	target := Target{Path: filepath.Join(dir, "CULTURE.md"), Source: SourceDerived, Root: dir}
	writeFile(t, target.Path, "locked\n")
	if err := os.Chmod(target.Path, 0o444); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(target.Path, 0o644) })
	current := shaOf(t, target.Path)

	_, err := Write(target, "changed\n", &current)
	var notWritable *NotWritableError
	if !errors.As(err, &notWritable) || !errors.Is(err, ErrNotWritable) {
		t.Fatalf("write = %v, want a NotWritableError", err)
	}
	if notWritable.Mode.Perm() != 0o444 || notWritable.Path != target.Path {
		t.Fatalf("not writable = %+v, want the path and 0444", notWritable)
	}
	data, _ := os.ReadFile(target.Path)
	if string(data) != "locked\n" {
		t.Fatalf("file = %q, want it untouched", data)
	}

	// The board reports the same fact on read.
	snapshot, err := Read(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if snapshot.Writable {
		t.Fatalf("snapshot = %+v, want writable false", snapshot)
	}
}

func TestWriteRefusesNonText(t *testing.T) {
	dir := t.TempDir()
	target := Target{Path: filepath.Join(dir, "CULTURE.md"), Source: SourceDerived, Root: dir}

	cases := map[string]string{
		"NUL byte":     "text with a \x00 in it",
		"invalid utf8": "text with \xff in it",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Write(target, content, nil); !errors.Is(err, ErrNotText) {
				t.Fatalf("write = %v, want ErrNotText", err)
			}
			if _, err := os.Stat(target.Path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("the refused write created the file")
			}
		})
	}
}

func TestWriteRefusesTooLarge(t *testing.T) {
	dir := t.TempDir()
	target := Target{Path: filepath.Join(dir, "CULTURE.md"), Source: SourceDerived, Root: dir}

	_, err := Write(target, strings.Repeat("a", MaxBytes+1), nil)
	var size *SizeError
	if !errors.As(err, &size) || !errors.Is(err, ErrTooLarge) {
		t.Fatalf("write = %v, want a SizeError", err)
	}
	if _, err := os.Stat(target.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the refused write created the file")
	}
}

func TestWriteRefusesSymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "OTHER.md")
	writeFile(t, outside, "original\n")
	link := filepath.Join(root, "CULTURE.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	target := Target{Path: link, Source: SourceDerived, Root: root}

	// Read refuses it too: a link in a rig directory must not stream an
	// unrelated file into the board.
	if _, err := Read(target); !errors.Is(err, ErrRefused) {
		t.Fatalf("read = %v, want ErrRefused for a symlink", err)
	}
	// Hash the link's target directly, so the write is tested against the bytes
	// that are really there rather than a value Read already refused to produce.
	outsideBytes, err := os.ReadFile(outside)
	if err != nil {
		t.Fatalf("read outside: %v", err)
	}
	outsideSum := sha256.Sum256(outsideBytes)
	current := hex.EncodeToString(outsideSum[:])

	_, err = Write(target, "overwritten\n", &current)
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("write = %v, want ErrRefused for a symlink", err)
	}
	data, _ := os.ReadFile(outside)
	if string(data) != "original\n" {
		t.Fatalf("outside file = %q, want it untouched", data)
	}
}

// TestWritePreservesModeAndBacksUp: a save keeps 0644, leaves the previous bytes
// in a timestamped backup, and never exposes a partial file.
func TestWritePreservesModeAndBacksUp(t *testing.T) {
	dir := t.TempDir()
	target := Target{Path: filepath.Join(dir, "CULTURE.md"), Source: SourceDerived, Root: dir}
	if err := os.WriteFile(target.Path, []byte("first rule\n"), 0o640); err != nil {
		t.Fatalf("write: %v", err)
	}
	info, err := os.Stat(target.Path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	current := shaOf(t, target.Path)

	result, err := Write(target, "second rule\n", &current)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if result.Backup == "" || !strings.Contains(filepath.Base(result.Backup), ".bak-") {
		t.Fatalf("backup = %q, want <name>.bak-<stamp>", result.Backup)
	}
	backup, err := os.ReadFile(result.Backup)
	if err != nil || string(backup) != "first rule\n" {
		t.Fatalf("backup = %q (%v), want the previous bytes", backup, err)
	}
	info, err = os.Stat(target.Path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %04o, want the previous 0640", info.Mode().Perm())
	}
	data, _ := os.ReadFile(target.Path)
	if string(data) != "second rule\n" {
		t.Fatalf("file = %q, want exactly the new content", data)
	}
	assertNoTempFiles(t, dir)
}

// TestBackupRotation: the five newest backups survive a save that would make a
// sixth. Old stamps are pre-seeded rather than slept through — the name has
// second resolution, so six real saves inside one second would collapse into
// one backup (and the rotation rule is what is under test here).
func TestBackupRotation(t *testing.T) {
	dir := t.TempDir()
	target := Target{Path: filepath.Join(dir, "CULTURE.md"), Source: SourceDerived, Root: dir}
	writeFile(t, target.Path, "current rule\n")

	stamps := []string{
		"20260101T000001Z", "20260101T000002Z", "20260101T000003Z",
		"20260101T000004Z", "20260101T000005Z",
	}
	for _, stamp := range stamps {
		path := target.Path + ".bak-" + stamp
		if err := os.WriteFile(path, []byte("older "+stamp+"\n"), 0o644); err != nil {
			t.Fatalf("seed backup: %v", err)
		}
	}

	current := shaOf(t, target.Path)
	if _, err := Write(target, "newest rule\n", &current); err != nil {
		t.Fatalf("write: %v", err)
	}

	backups := listBackups(t, dir)
	if len(backups) != BackupKeep {
		t.Fatalf("backups = %v, want %d kept", backups, BackupKeep)
	}
	for _, backup := range backups {
		if strings.Contains(backup, stamps[0]) {
			t.Fatalf("the oldest backup survived: %v", backups)
		}
	}
	// A sixth pre-seeded stamp should also go: the rule is a count, not an age.
	if err := os.WriteFile(target.Path+".bak-20260101T000000Z", []byte("ancient\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	current = shaOf(t, target.Path)
	if _, err := Write(target, "newer rule\n", &current); err != nil {
		t.Fatalf("write: %v", err)
	}
	for _, backup := range listBackups(t, dir) {
		if strings.Contains(backup, "20260101T000000Z") {
			t.Fatalf("prune kept the oldest stamp: %v", listBackups(t, dir))
		}
	}
}

// TestWriteLeavesNoPartialFile: a directory that refuses a temp file fails the
// write with the old content still in place and no stray temp behind.
func TestWriteLeavesNoPartialFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CULTURE.md")
	writeFile(t, path, "original rule\n")
	current := shaOf(t, path)

	// A read-only directory: the temp file cannot be created.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	_, err := Write(Target{Path: path, Source: SourceDerived, Root: dir}, "replacement\n", &current)
	var notWritable *NotWritableError
	if !errors.As(err, &notWritable) || !errors.Is(err, ErrNotWritable) {
		t.Fatalf("write = %v, want a NotWritableError for a read-only directory", err)
	}

	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod back: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "original rule\n" {
		t.Fatalf("file = %q (%v), want the old bytes", data, err)
	}
	assertNoTempFiles(t, dir)
}

func TestWriteIsAtomicAcrossReads(t *testing.T) {
	dir := t.TempDir()
	target := Target{Path: filepath.Join(dir, "CULTURE.md"), Source: SourceDerived, Root: dir}
	oldContent := strings.Repeat("old rule\n", 4096)
	newContent := strings.Repeat("new rule\n", 4096)
	if err := os.WriteFile(target.Path, []byte(oldContent), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	current := shaOf(t, target.Path)

	stop := make(chan struct{})
	firstRead := make(chan struct{})
	bad := make(chan string, 1)
	var reads atomic.Int64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		started := false
		for {
			select {
			case <-stop:
				return
			default:
			}
			data, err := os.ReadFile(target.Path)
			if err != nil {
				continue
			}
			reads.Add(1)
			if !started {
				started = true
				close(firstRead)
			}
			if text := string(data); text != oldContent && text != newContent {
				select {
				case bad <- "reader saw " + strconv.Itoa(len(data)) + " bytes, neither version":
				default:
				}
				return
			}
		}
	}()

	// The write waits for the reader to have observed the old file at least
	// once, so the test cannot pass by having the write finish first.
	<-firstRead
	if _, err := Write(target, newContent, &current); err != nil {
		close(stop)
		wg.Wait()
		t.Fatalf("write: %v", err)
	}
	// Give the reader a moment past the rename, then stop it.
	deadline := time.Now().Add(20 * time.Millisecond)
	for time.Now().Before(deadline) && reads.Load() < 2 {
		time.Sleep(time.Millisecond)
	}
	close(stop)
	wg.Wait()
	select {
	case message := <-bad:
		t.Fatal(message)
	default:
	}
	if reads.Load() == 0 {
		t.Fatal("the reader never observed the file; the test proved nothing")
	}
}

func listBackups(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	var names []string
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".bak-") {
			names = append(names, entry.Name())
		}
	}
	return names
}

func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("temp file left behind: %s", entry.Name())
		}
	}
}

// TestReadRefusesSymlink: an existing file that is a link is refused on read
// too. Without this, a symlink planted in a rig directory is a read primitive
// for anything the service can open, which is the opposite of the resolver's
// promise that the path comes from configuration.
func TestReadRefusesSymlink(t *testing.T) {
	root := t.TempDir()
	secret := filepath.Join(t.TempDir(), "id_ed25519")
	writeFile(t, secret, "PRIVATE KEY\n")
	link := filepath.Join(root, "CULTURE.md")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	_, err := Read(Target{Path: link, Source: SourceDerived, Root: root})
	var refused *RefusedError
	if !errors.As(err, &refused) || !errors.Is(err, ErrRefused) {
		t.Fatalf("read = %v, want a RefusedError", err)
	}
	if strings.Contains(err.Error(), "PRIVATE KEY") {
		t.Fatalf("the refusal leaked the linked file's content: %v", err)
	}
}
