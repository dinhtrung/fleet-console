// Package culture resolves, reads, guards and writes a rig's CULTURE.md.
//
// The path is decided here and nowhere else: a request may never steer it, so
// the only inputs are the project's config and the rigs root. Writes are
// conditional on the sha the editor saw, atomic (temp file, fsync, rename in the
// same directory), mode-preserving and leave a rolling backup.
package culture

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// MaxBytes is the ceiling for a culture file, on read as well as on write:
	// a hand-mangled file must not be streamed into a textarea.
	MaxBytes = 256 << 10
	// FileName is the per-rig file a project resolves to when it names none.
	FileName = "CULTURE.md"
	// DefaultSpecsRoot is where OpenRig keeps rig specs when OPENRIG_SPECS_DIR
	// says nothing.
	DefaultSpecsRoot = "~/.openrig/specs"
	// SpecsDirEnv overrides the specs root, for a box that keeps them elsewhere.
	SpecsDirEnv = "OPENRIG_SPECS_DIR"
	// BackupKeep is how many timestamped copies of the previous content survive.
	BackupKeep = 5
	// backupLayout is the UTC stamp in a backup's name.
	backupLayout = "20060102T150405Z"
	// defaultMode is what a newly created culture file gets.
	defaultMode os.FileMode = 0o644
)

// Source says why a path was chosen, so the screen can explain itself.
type Source string

const (
	// SourceConfig means projects[i].culture_file named it.
	SourceConfig Source = "config"
	// SourceDerived means it came from the project's first rig.
	SourceDerived Source = "derived"
	// SourceNotConfigured means there is nothing to read or write and no guess
	// was made.
	SourceNotConfigured Source = "not_configured"
)

// Typed refusals. Each names one cause so a handler maps it to a status without
// re-deriving the reason, and each leaves the target untouched.
var (
	// ErrNotConfigured: the project names no culture file and has no rigs.
	ErrNotConfigured = errors.New("culture: project names no culture file and has no rigs")
	// ErrTooLarge: the content is over MaxBytes.
	ErrTooLarge = errors.New("culture: content too large")
	// ErrNotText: the content is not UTF-8 or contains a NUL byte.
	ErrNotText = errors.New("culture: content must be UTF-8 text")
	// ErrNoFile: the file is absent and the caller did not say base_sha: null.
	ErrNoFile = errors.New("culture: no culture file")
	// ErrStale: base_sha does not match the file on disk.
	ErrStale = errors.New("culture: stale")
	// ErrNotWritable: the file or its directory refuses a write.
	ErrNotWritable = errors.New("culture: not writable")
	// ErrRefused: the path is a symlink or resolves outside the directory the
	// resolver chose.
	ErrRefused = errors.New("culture: refused")
)

// StaleError carries the current state so a screen can offer "load latest"
// without a second round trip.
type StaleError struct {
	Path  string
	SHA   string
	MTime time.Time
}

func (e *StaleError) Error() string {
	return fmt.Sprintf("culture: stale: %s is at %s since %s", e.Path, shortSHA(e.SHA), e.MTime.UTC().Format(time.RFC3339))
}
func (e *StaleError) Is(target error) bool { return target == ErrStale }

// SizeError reports the limit as well as the failure, because the response body
// carries both.
type SizeError struct {
	Bytes int
	Limit int
}

func (e *SizeError) Error() string {
	return fmt.Sprintf("culture: content too large: %d bytes, limit %d", e.Bytes, e.Limit)
}
func (e *SizeError) Is(target error) bool { return target == ErrTooLarge }

// NotWritableError names the path and the mode the operator has to change.
type NotWritableError struct {
	Path string
	Mode os.FileMode
}

func (e *NotWritableError) Error() string {
	return fmt.Sprintf("culture: not writable: %s (mode %04o)", e.Path, e.Mode.Perm())
}
func (e *NotWritableError) Is(target error) bool { return target == ErrNotWritable }

// RefusedError names both the path that was asked for and where it really goes.
type RefusedError struct {
	Path     string
	Resolved string
	Reason   string
}

func (e *RefusedError) Error() string {
	if e.Resolved == "" || e.Resolved == e.Path {
		return fmt.Sprintf("culture: refused: %s: %s", e.Path, e.Reason)
	}
	return fmt.Sprintf("culture: refused: %s -> %s: %s", e.Path, e.Resolved, e.Reason)
}
func (e *RefusedError) Is(target error) bool { return target == ErrRefused }

// Target is a resolved culture file and why it is that file.
//
// Root is the directory a write may not escape: the rig's spec directory for a
// derived path, and the configured file's own directory for an explicit one —
// the owner named that location, so it is the location that is allowed.
type Target struct {
	Path   string
	Source Source
	Rig    string
	Root   string
}

// Resolve decides once per project where its culture lives.
//
// projects[i].culture_file wins (with ~ expanded); otherwise the first rig gives
// <specsDir>/rigs/<rig>/CULTURE.md. specsDir may be empty, in which case
// OPENRIG_SPECS_DIR is used and then DefaultSpecsRoot. A project with neither a
// key nor a rig is not_configured: an empty Path is the honest answer, and no
// directory is invented.
func Resolve(project Project, specsDir string) Target {
	if configured := strings.TrimSpace(project.CultureFile); configured != "" {
		path := ExpandHome(configured, homeDir())
		return Target{
			Path:   path,
			Source: SourceConfig,
			Rig:    firstRig(project),
			Root:   filepath.Dir(path),
		}
	}
	rig := firstRig(project)
	if rig == "" {
		return Target{Source: SourceNotConfigured}
	}
	root := rigSpecDir(specsDir, rig)
	return Target{
		Path:   filepath.Join(root, FileName),
		Source: SourceDerived,
		Rig:    rig,
		Root:   root,
	}
}

// Project is the part of a project a culture path depends on. internal/config
// satisfies it by construction; naming it here keeps this package free of a
// config import for three fields.
type Project struct {
	CultureFile string
	Rigs        []string
}

// SpecsDir is the root that holds every rig's spec directory: the argument when
// set, else OPENRIG_SPECS_DIR, else DefaultSpecsRoot. A leading ~ is expanded.
func SpecsDir(configured string) string {
	value := strings.TrimSpace(configured)
	if value == "" {
		value = strings.TrimSpace(os.Getenv(SpecsDirEnv))
	}
	if value == "" {
		value = DefaultSpecsRoot
	}
	return ExpandHome(value, homeDir())
}

// ExpandHome expands a leading ~ or ~/ against home. Any other path is returned
// as it was, so a caller that has already resolved a path is unaffected.
func ExpandHome(path, home string) string {
	switch {
	case path == "~":
		return home
	case strings.HasPrefix(path, "~/"):
		return filepath.Join(home, path[2:])
	default:
		return path
	}
}

// Snapshot is the file as the board reads it. A missing file is a normal
// answer: Exists is false and Content is empty.
type Snapshot struct {
	Exists   bool
	Writable bool
	Bytes    int64
	SHA256   string
	MTime    time.Time
	Content  string
}

// Read reports the current file. A missing target is Snapshot{Exists: false}
// with no error; content over MaxBytes is a SizeError.
//
// An existing file passes Guard first. The write path needs that rule to avoid
// replacing the wrong file; the read path needs it so a symlink planted in a rig
// directory cannot stream an unrelated file (a key, a host file) into the board.
// A missing file is answered before the guard, because "not created yet" must
// stay a normal answer even on a box whose specs tree does not exist.
func Read(target Target) (Snapshot, error) {
	if target.Source == SourceNotConfigured || target.Path == "" {
		return Snapshot{}, ErrNotConfigured
	}
	info, err := os.Stat(target.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Snapshot{Exists: false, Writable: dirWritable(filepath.Dir(target.Path))}, nil
		}
		return Snapshot{}, fmt.Errorf("culture: read %s: %w", target.Path, err)
	}
	if err := Guard(target); err != nil {
		return Snapshot{}, err
	}
	if info.IsDir() {
		return Snapshot{}, &RefusedError{Path: target.Path, Reason: "it is a directory"}
	}
	if info.Size() > MaxBytes {
		return Snapshot{}, &SizeError{Bytes: int(info.Size()), Limit: MaxBytes}
	}
	data, err := os.ReadFile(target.Path)
	if err != nil {
		return Snapshot{}, fmt.Errorf("culture: read %s: %w", target.Path, err)
	}
	sum := sha256.Sum256(data)
	return Snapshot{
		Exists:   true,
		Writable: info.Mode().Perm()&0o222 != 0,
		Bytes:    int64(len(data)),
		SHA256:   hex.EncodeToString(sum[:]),
		MTime:    info.ModTime().UTC(),
		Content:  string(data),
	}, nil
}

// Guard refuses a path that is a symlink, or whose resolved directory leaves the
// directory Resolve chose. Both cases are 403s: the file the board edits must be
// the file the resolver named, not whatever a link points at.
func Guard(target Target) error {
	if target.Path == "" {
		return &RefusedError{Reason: "no path was resolved"}
	}
	info, err := os.Lstat(target.Path)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink != 0 {
			resolved, _ := filepath.EvalSymlinks(target.Path)
			return &RefusedError{Path: target.Path, Resolved: resolved, Reason: "the path is a symlink"}
		}
		if !info.Mode().IsRegular() && !info.IsDir() {
			return &RefusedError{Path: target.Path, Reason: "the path is not a regular file"}
		}
	case errors.Is(err, os.ErrNotExist):
		// A missing file is fine; its directory still has to be the right one.
	default:
		return fmt.Errorf("culture: inspect %s: %w", target.Path, err)
	}

	dir := filepath.Dir(target.Path)
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return &RefusedError{Path: target.Path, Reason: "the directory does not resolve: " + err.Error()}
	}
	root := target.Root
	if root == "" {
		root = dir
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		resolvedRoot = filepath.Clean(root)
	}
	if resolvedDir != resolvedRoot {
		return &RefusedError{Path: target.Path, Resolved: resolvedDir, Reason: "it resolves outside " + resolvedRoot}
	}
	return nil
}

// Result is what a successful write produced.
type Result struct {
	Path   string
	SHA256 string
	Bytes  int64
	MTime  time.Time
	Backup string
}

// Write saves content to the target in the order design §3 lists, first failure
// wins: size, text, absence/base rules, staleness, writability, guard. Exactly
// one typed error comes back per refusal and the file is untouched in every one
// of those cases.
//
// baseSHA is the sha the editor loaded; nil means "I expect no file" and creates
// one. A write that succeeds replaces the file atomically, preserves its mode,
// keeps the previous bytes in a rolling backup and records nothing else — the
// culture.edited event belongs to the caller that owns the store.
func Write(target Target, content string, baseSHA *string) (Result, error) {
	if target.Source == SourceNotConfigured || target.Path == "" {
		return Result{}, ErrNotConfigured
	}

	// 1. Size, then 2. text. Both are about the request alone.
	if len(content) > MaxBytes {
		return Result{}, &SizeError{Bytes: len(content), Limit: MaxBytes}
	}
	if !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
		return Result{}, ErrNotText
	}

	// 3. Absence and base rules.
	current, err := currentState(target.Path)
	if err != nil {
		return Result{}, err
	}
	switch {
	case !current.exists:
		if baseSHA != nil {
			return Result{}, ErrNoFile
		}
	case baseSHA == nil:
		return Result{}, &StaleError{Path: target.Path, SHA: current.sha, MTime: current.mtime}
	case *baseSHA != current.sha:
		// 4. Stale: the file moved since the editor read it.
		return Result{}, &StaleError{Path: target.Path, SHA: current.sha, MTime: current.mtime}
	}

	// 5. Writability, of the file if there is one and of its directory always,
	// because the write is a temp file beside it.
	if current.exists && current.mode.Perm()&0o222 == 0 {
		return Result{}, &NotWritableError{Path: target.Path, Mode: current.mode}
	}
	dir := filepath.Dir(target.Path)
	if info, err := os.Stat(dir); err != nil {
		return Result{}, &NotWritableError{Path: dir, Mode: 0}
	} else if !info.IsDir() {
		return Result{}, &RefusedError{Path: target.Path, Reason: "its parent is not a directory"}
	} else if info.Mode().Perm()&0o222 == 0 {
		return Result{}, &NotWritableError{Path: dir, Mode: info.Mode()}
	}

	// 6. Guard: the last check, and the one that decides what may be replaced.
	if err := Guard(target); err != nil {
		return Result{}, err
	}

	mode := defaultMode
	if current.exists {
		mode = current.mode.Perm()
	}
	backup, err := writeBackup(target.Path, current)
	if err != nil {
		return Result{}, err
	}
	if err := writeAtomic(target.Path, []byte(content), mode); err != nil {
		return Result{}, err
	}
	pruneBackups(target.Path, BackupKeep)

	written, err := currentState(target.Path)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Path:   target.Path,
		SHA256: written.sha,
		Bytes:  int64(len(content)),
		MTime:  written.mtime,
		Backup: backup,
	}, nil
}

// state is what the target looked like a moment ago.
type state struct {
	exists bool
	sha    string
	mtime  time.Time
	mode   os.FileMode
	data   []byte
}

func currentState(path string) (state, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return state{}, nil
		}
		return state{}, fmt.Errorf("culture: stat %s: %w", path, err)
	}
	if info.IsDir() {
		return state{}, &RefusedError{Path: path, Reason: "it is a directory"}
	}
	if info.Size() > MaxBytes {
		return state{}, &SizeError{Bytes: int(info.Size()), Limit: MaxBytes}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return state{}, fmt.Errorf("culture: read %s: %w", path, err)
	}
	sum := sha256.Sum256(data)
	return state{
		exists: true,
		sha:    hex.EncodeToString(sum[:]),
		mtime:  info.ModTime().UTC(),
		mode:   info.Mode(),
		data:   data,
	}, nil
}

// writeBackup copies the previous bytes beside the target as
// <name>.bak-<UTC yyyymmddThhmmssZ>. Nothing to back up means no file, and no
// error: a first save is not a failure.
//
// This is the one direct os.WriteFile in the package, and it writes a new
// sibling path, never the target: a seat reading CULTURE.md is untouched by a
// half-written backup. The target itself only ever moves through writeAtomic.
func writeBackup(path string, current state) (string, error) {
	if !current.exists {
		return "", nil
	}
	backup := path + ".bak-" + time.Now().UTC().Format(backupLayout)
	if err := os.WriteFile(backup, current.data, current.mode.Perm()); err != nil {
		return "", fmt.Errorf("culture: backup %s: %w", backup, err)
	}
	return backup, nil
}

// pruneBackups keeps the newest keep backups of path and removes the rest.
func pruneBackups(path string, keep int) {
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		return
	}
	prefix := filepath.Base(path) + ".bak-"
	var backups []string
	for _, entry := range entries {
		if name := entry.Name(); strings.HasPrefix(name, prefix) && !entry.IsDir() {
			backups = append(backups, name)
		}
	}
	if len(backups) <= keep {
		return
	}
	// The stamp sorts lexicographically in UTC, so newest-last by name.
	sort.Strings(backups)
	for _, name := range backups[:len(backups)-keep] {
		_ = os.Remove(filepath.Join(filepath.Dir(path), name))
	}
}

// writeAtomic is the only writer: temp file in the target's directory, fsync,
// rename over the target, then fsync the directory so the rename survives a
// crash. A reader sees the old file or the new one, never a half-written rule.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("culture: create temp beside %s: %w", path, err)
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpPath)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("culture: write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("culture: fsync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("culture: close temp: %w", err)
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("culture: chmod temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("culture: rename over %s: %w", path, err)
	}
	if handle, err := os.Open(dir); err == nil {
		_ = handle.Sync()
		handle.Close()
	}
	return nil
}

func rigSpecDir(specsDir, rig string) string {
	return filepath.Join(SpecsDir(specsDir), "rigs", rig)
}

func firstRig(project Project) string {
	if len(project.Rigs) == 0 {
		return ""
	}
	return strings.TrimSpace(project.Rigs[0])
}

func dirWritable(dir string) bool {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	return info.Mode().Perm()&0o222 != 0
}

func shortSHA(sha string) string {
	if len(sha) <= 8 {
		return sha
	}
	return sha[:8]
}

func homeDir() string {
	home, _ := os.UserHomeDir()
	return home
}
