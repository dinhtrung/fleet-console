package configfile

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"github.com/dinhtrung/fleet-console/internal/config"
	"gopkg.in/yaml.v3"
)

// Outcome is the result of an upsert.
type Outcome string

const (
	OutcomeCreated   Outcome = "Created"
	OutcomeUnchanged Outcome = "Unchanged"
)

// writeMu serializes the config-file writers in this package — UpsertProject,
// UpdateProject and RemoveProject — so two in-process writers can never
// interleave their read-modify-write cycles over the operator's file (design
// D9). A second fleet-console process writing the same file is already a
// configuration error, so there is no cross-process protocol here.
var writeMu sync.Mutex

// The three refusals that belong to the caller, typed so a caller classifies an
// error by identity instead of by matching its text. Everything else this
// package returns is a server-side failure: the write could not be performed,
// not that the request was wrong.
var (
	// ErrDuplicateKey means the file already names the project's key with
	// different content.
	ErrDuplicateKey = errors.New("duplicate project key")
	// ErrNotFound means the file does not name the project's key: a remove or
	// an update of an absent entry is a caller mistake, not a server failure.
	ErrNotFound = errors.New("project not found in config file")
	// ErrStaleRead means the file moved after the caller read it.
	ErrStaleRead = errors.New("config.yaml changed on disk; reload and retry")
)

// UpsertProject writes a project to the config file at path.
//
// expectedHash is the sha256 of the file content the caller read; when it no
// longer matches the file on disk the write is refused rather than applied over
// someone's hand edit. The write is atomic: temp file in the same dir, fsync,
// original mode, rename.
//
// The new entry is spliced into the original bytes at the end of the projects
// sequence, so every byte the operator wrote — header comments, blank lines,
// each existing entry's own spacing — survives untouched. The merged document
// is then validated as a whole with the loader's own rules, before anything is
// written: a write that would leave the fleet unable to start never reaches the
// disk. That failure is a *config.ValidationError and the file is untouched.
func UpsertProject(path string, in Project, expectedHash *[32]byte) (Outcome, error) {
	writeMu.Lock()
	defer writeMu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("configfile: read %s: %w", path, err)
		}
		data = nil
	}
	if expectedHash != nil {
		if now := sha256.Sum256(data); now != *expectedHash {
			return "", ErrStaleRead
		}
	}

	out, outcome, err := splice(data, in)
	if err != nil {
		return "", err
	}

	dir := filepath.Dir(path)
	home, _ := os.UserHomeDir()
	if err := config.ValidateContent(out, dir, home); err != nil {
		return "", err
	}
	if outcome == OutcomeUnchanged {
		return outcome, nil
	}
	if err := writeAtomic(path, out); err != nil {
		return "", err
	}
	return OutcomeCreated, nil
}

// splice returns the file bytes with in appended to the projects sequence, or
// OutcomeUnchanged when the file already holds that project verbatim.
func splice(data []byte, in Project) ([]byte, Outcome, error) {
	entry, err := encodeEntry(in)
	if err != nil {
		return nil, "", err
	}
	item := indentEntry(entry, "  ")
	if len(bytes.TrimSpace(data)) == 0 {
		return []byte("projects:\n" + item), OutcomeCreated, nil
	}

	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, "", fmt.Errorf("configfile: unmarshal: %w", err)
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) == 0 {
		return nil, "", fmt.Errorf("configfile: the file is not a mapping")
	}
	root := document.Content[0]

	projectsKey, sequence := findProjects(root)
	if projectsKey == nil {
		return appendMapping(data, "projects:\n"+item), OutcomeCreated, nil
	}
	if sequence.Kind != yaml.SequenceNode {
		return nil, "", fmt.Errorf("configfile: projects is not a sequence")
	}
	if len(sequence.Content) == 0 {
		return replaceEmptySequence(data, projectsKey, entry)
	}

	for _, item := range sequence.Content {
		var existing Project
		if err := item.Decode(&existing); err != nil {
			return nil, "", fmt.Errorf("configfile: decode existing project: %w", err)
		}
		if existing.Key != in.Key {
			continue
		}
		if reflect.DeepEqual(existing, in) {
			return data, OutcomeUnchanged, nil
		}
		return nil, "", fmt.Errorf("%w %q", ErrDuplicateKey, in.Key)
	}

	indent := itemIndent(data, sequence)
	body := indentEntry(entry, indent)
	insertAt := sequenceEnd(data, sequence)
	return spliceAt(data, insertAt, body), OutcomeCreated, nil
}

// RemoveProject splices the entry whose key matches out of the config file at
// path, leaving every other byte — the header comments, the key order, the
// sibling entries and their spacing — as the operator wrote it. The entry's
// span runs from its dash line to the next entry's own start (a leading
// comment belongs to the entry that follows it, not to this one), or to the
// end of the sequence for the last entry, so the blank line that separated the
// entries goes with the removed one and nothing else moves. Removing the last
// entry turns the projects key into `projects: []`, a file the loader still
// accepts, rather than the dangling `projects:` key it refuses.
//
// expectedHash is the same stale-read contract UpsertProject takes. An absent
// key is ErrNotFound with no write; a moved file is ErrStaleRead; and the
// merged document is validated as a whole with the loader's own rules before
// the atomic write, so a removal that would leave the file unloadable is a
// *config.ValidationError and the file is untouched.
func RemoveProject(path string, key string, expectedHash *[32]byte) (Outcome, error) {
	writeMu.Lock()
	defer writeMu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w %q", ErrNotFound, key)
		}
		return "", fmt.Errorf("configfile: read %s: %w", path, err)
	}
	if expectedHash != nil {
		if now := sha256.Sum256(data); now != *expectedHash {
			return "", ErrStaleRead
		}
	}

	out, err := removeEntry(data, key)
	if err != nil {
		return "", err
	}

	dir := filepath.Dir(path)
	home, _ := os.UserHomeDir()
	if err := config.ValidateContent(out, dir, home); err != nil {
		return "", err
	}
	if err := writeAtomic(path, out); err != nil {
		return "", err
	}
	return OutcomeCreated, nil
}

// UpdateProject replaces the entry whose key matches in.Key with a rendering
// of in, in place: the new text is spliced over the old entry's byte span at
// that entry's own indentation, so the header comments and every sibling
// entry's bytes never move. The key itself is not renameable — the entry is
// located by it, so an update can never turn one key into another (a rename is
// a delete and a create).
//
// An identical entry is OutcomeUnchanged with no write; an absent key is
// ErrNotFound; a moved file is ErrStaleRead; and the merged document is
// validated as a whole before the atomic write, like UpsertProject.
func UpdateProject(path string, in Project, expectedHash *[32]byte) (Outcome, error) {
	writeMu.Lock()
	defer writeMu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w %q", ErrNotFound, in.Key)
		}
		return "", fmt.Errorf("configfile: read %s: %w", path, err)
	}
	if expectedHash != nil {
		if now := sha256.Sum256(data); now != *expectedHash {
			return "", ErrStaleRead
		}
	}

	out, unchanged, err := replaceEntry(data, in)
	if err != nil {
		return "", err
	}

	dir := filepath.Dir(path)
	home, _ := os.UserHomeDir()
	if err := config.ValidateContent(out, dir, home); err != nil {
		return "", err
	}
	if unchanged {
		return OutcomeUnchanged, nil
	}
	if err := writeAtomic(path, out); err != nil {
		return "", err
	}
	return OutcomeCreated, nil
}

// removeEntry returns the file bytes without the entry whose key matches.
func removeEntry(data []byte, key string) ([]byte, error) {
	projectsKey, sequence, err := projectsSequence(data, key)
	if err != nil {
		return nil, err
	}
	item, err := locateEntry(sequence, key)
	if err != nil {
		return nil, err
	}
	if item.Line == projectsKey.Line {
		return nil, fmt.Errorf("configfile: projects is not a block sequence")
	}
	start, end := entrySpan(data, sequence, item)
	if len(sequence.Content) > 1 {
		out := make([]byte, 0, len(data)-(end-start))
		out = append(out, data[:start]...)
		return append(out, data[end:]...), nil
	}
	// The last entry empties the sequence. A bare `projects:` key decodes as a
	// null the loader refuses, so the key keeps an inline empty sequence and
	// the bytes after the removed entry follow it.
	offset := lineOffset(data, projectsKey.Line)
	keyLine := data[offset:lineEnd(data, offset)]
	colon := bytes.IndexByte(keyLine, ':')
	if colon < 0 {
		return nil, fmt.Errorf("configfile: projects key has no colon")
	}
	if rest := strings.TrimSpace(string(keyLine[colon+1:])); rest != "" {
		return nil, fmt.Errorf("configfile: projects is not a block sequence")
	}
	var out bytes.Buffer
	out.Write(data[:offset+colon+1])
	out.WriteString(" []\n")
	out.Write(data[end:])
	return out.Bytes(), nil
}

// replaceEntry returns the file bytes with in written over the entry it names,
// and whether the entry was already identical.
func replaceEntry(data []byte, in Project) ([]byte, bool, error) {
	projectsKey, sequence, err := projectsSequence(data, in.Key)
	if err != nil {
		return nil, false, err
	}
	item, err := locateEntry(sequence, in.Key)
	if err != nil {
		return nil, false, err
	}
	if item.Line == projectsKey.Line {
		return nil, false, fmt.Errorf("configfile: projects is not a block sequence")
	}

	var existing Project
	if err := item.Decode(&existing); err != nil {
		return nil, false, fmt.Errorf("configfile: decode existing project: %w", err)
	}
	if reflect.DeepEqual(existing, in) {
		return data, true, nil
	}

	entry, err := encodeEntry(in)
	if err != nil {
		return nil, false, err
	}
	start, end := entrySpan(data, sequence, item)
	// The blank lines that separated the entry from the next one belong to the
	// old span, but a replacement keeps them: they are the file's spacing, not
	// the entry's content, so the new text is spliced in ahead of them.
	end = trimBlankLines(data, end)
	indent := entryIndent(data, item)
	out := make([]byte, 0, len(data)-(end-start)+len(entry))
	out = append(out, data[:start]...)
	out = append(out, indentEntry(entry, indent)...)
	return append(out, data[end:]...), false, nil
}

// trimBlankLines moves end up past the blank lines at the tail of a span. A
// removal takes them with the entry; a replacement leaves them in place. end
// must be the start of a line or the end of the file.
func trimBlankLines(data []byte, end int) int {
	for end > 0 {
		if data[end-1] != '\n' {
			// No trailing newline: the last line is content, not a blank.
			break
		}
		lineStart := end - 1
		for lineStart > 0 && data[lineStart-1] != '\n' {
			lineStart--
		}
		if !isBlankLine(data[lineStart:end]) {
			break
		}
		end = lineStart
	}
	return end
}

// isBlankLine reports whether the bytes from a line's start to just past its
// newline carry nothing but whitespace.
func isBlankLine(line []byte) bool {
	return strings.TrimSpace(string(bytes.TrimSuffix(line, []byte("\n")))) == ""
}

// projectsSequence returns the `projects` key node and its sequence from the
// parsed file, or ErrNotFound when the file names no projects at all: no key,
// a null, or an empty sequence.
func projectsSequence(data []byte, key string) (*yaml.Node, *yaml.Node, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil, fmt.Errorf("%w %q", ErrNotFound, key)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, nil, fmt.Errorf("configfile: unmarshal: %w", err)
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) == 0 {
		return nil, nil, fmt.Errorf("configfile: the file is not a mapping")
	}
	projectsKey, sequence := findProjects(document.Content[0])
	if projectsKey == nil || sequence == nil || sequence.Kind != yaml.SequenceNode || len(sequence.Content) == 0 {
		return nil, nil, fmt.Errorf("%w %q", ErrNotFound, key)
	}
	return projectsKey, sequence, nil
}

// locateEntry returns the sequence item whose decoded key matches.
func locateEntry(sequence *yaml.Node, key string) (*yaml.Node, error) {
	for _, item := range sequence.Content {
		var existing Project
		if err := item.Decode(&existing); err != nil {
			return nil, fmt.Errorf("configfile: decode project: %w", err)
		}
		if existing.Key == key {
			return item, nil
		}
	}
	return nil, fmt.Errorf("%w %q", ErrNotFound, key)
}

// entrySpan is the byte span of one entry: from its dash line to the next
// entry's own start — a leading comment belongs to the entry that follows it,
// not to this one — or to the end of the sequence for the last entry.
func entrySpan(data []byte, sequence, item *yaml.Node) (int, int) {
	start := lineOffset(data, item.Line)
	for index, candidate := range sequence.Content {
		if candidate != item {
			continue
		}
		if index+1 < len(sequence.Content) {
			return start, entryBoundary(data, sequence.Content[index+1].Line)
		}
		return start, sequenceEnd(data, sequence)
	}
	return start, start
}

// entryBoundary is where the next entry starts: the top of its leading comment
// block when it has one — the comment names the entry that follows it, so it
// survives the entry before — otherwise the dash line itself. The blank lines
// between the two entries stay inside the removed entry's span.
func entryBoundary(data []byte, line int) int {
	boundary := lineOffset(data, line)
	for boundary > 0 {
		lineStart := boundary
		for lineStart > 0 && data[lineStart-1] != '\n' {
			lineStart--
		}
		trimmed := strings.TrimLeft(string(data[lineStart:boundary]), " \t\r")
		if strings.HasPrefix(trimmed, "#") {
			boundary = lineStart
			continue
		}
		break
	}
	return boundary
}

// findProjects returns the `projects` key node and its value node, or nil.
func findProjects(root *yaml.Node) (*yaml.Node, *yaml.Node) {
	for index := 0; index+1 < len(root.Content); index += 2 {
		if root.Content[index].Value == "projects" {
			return root.Content[index], root.Content[index+1]
		}
	}
	return nil, nil
}

// itemIndent is the whitespace before the sequence's first dash, taken from the
// operator's own line rather than from a node column, which points at the item
// content (after the dash) and would shift every later entry by two spaces.
func itemIndent(data []byte, sequence *yaml.Node) string {
	return entryIndent(data, sequence.Content[0])
}

// entryIndent is the whitespace before one item's dash, taken from that item's
// own line, so an entry sitting at a different indentation keeps its own.
func entryIndent(data []byte, item *yaml.Node) string {
	start := lineOffset(data, item.Line)
	line := string(data[start:lineEnd(data, start)])
	trimmed := strings.TrimLeft(line, " \t")
	if dash := strings.Index(trimmed, "-"); dash >= 0 {
		return line[:len(line)-len(trimmed)]
	}
	return ""
}

// replaceEmptySequence turns `projects: []` (or a bare `projects:`) into a
// block sequence holding the one entry, leaving the rest of the line alone.
func replaceEmptySequence(data []byte, key *yaml.Node, entry string) ([]byte, Outcome, error) {
	offset := lineOffset(data, key.Line)
	end := lineEnd(data, offset)
	line := string(data[offset:end])
	colon := strings.Index(line, ":")
	if colon < 0 {
		return nil, "", fmt.Errorf("configfile: projects key has no colon")
	}
	rest := line[colon+1:]
	value := strings.TrimSpace(rest)
	if value != "" && value != "[]" && value != "null" && value != "~" {
		return nil, "", fmt.Errorf("configfile: projects is not an empty sequence")
	}
	indent := strings.Repeat(" ", key.Column-1+2)
	head := string(data[:offset+colon+1])
	remainder := string(data[end:])
	remainder = strings.TrimPrefix(remainder, "\n")
	replaced := head + "\n" + indentEntry(entry, indent)
	return []byte(replaced + remainder), OutcomeCreated, nil
}

// sequenceEnd is the byte offset just past the last line of the sequence: the
// start of the next root-level line, or the end of the file.
func sequenceEnd(data []byte, sequence *yaml.Node) int {
	last := sequence.Content[len(sequence.Content)-1]
	start := lineOffset(data, last.Line)
	offset := lineEnd(data, start)
	for offset < len(data) {
		next := offset + 1
		end := lineEnd(data, next)
		line := strings.TrimRight(string(data[next:end]), " \t")
		if line != "" && line[0] != '#' && line[0] != ' ' && line[0] != '\t' && line[0] != '\n' {
			return next
		}
		offset = end
	}
	return len(data)
}

// spliceAt inserts body at offset, keeping the file's final newline honest.
func spliceAt(data []byte, offset int, body string) []byte {
	var out bytes.Buffer
	out.Write(data[:offset])
	if len(data) > 0 && data[offset-1] != '\n' {
		out.WriteByte('\n')
	}
	out.WriteString(body)
	out.Write(data[offset:])
	return out.Bytes()
}

// appendMapping adds a whole top-level key after the existing content.
func appendMapping(data []byte, addition string) []byte {
	var out bytes.Buffer
	out.Write(data)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		out.WriteByte('\n')
	}
	out.WriteString("\n")
	out.WriteString(addition)
	return out.Bytes()
}

// encodeEntry marshals one project as a block mapping.
func encodeEntry(in Project) (string, error) {
	encoded, err := yaml.Marshal(in)
	if err != nil {
		return "", fmt.Errorf("configfile: marshal project: %w", err)
	}
	return string(encoded), nil
}

// indentEntry turns a marshalled mapping into a sequence entry at indent.
func indentEntry(entry, indent string) string {
	lines := strings.Split(strings.TrimRight(entry, "\n"), "\n")
	var out strings.Builder
	for index, line := range lines {
		if index == 0 {
			out.WriteString(indent + "- " + line + "\n")
			continue
		}
		if line == "" {
			out.WriteString("\n")
			continue
		}
		out.WriteString(indent + "  " + line + "\n")
	}
	return out.String()
}

func lineOffset(data []byte, line int) int {
	if line <= 1 {
		return 0
	}
	offset := 0
	for current := 1; current < line && offset < len(data); current++ {
		index := bytes.IndexByte(data[offset:], '\n')
		if index < 0 {
			return len(data)
		}
		offset += index + 1
	}
	return offset
}

func lineEnd(data []byte, offset int) int {
	if offset >= len(data) {
		return len(data)
	}
	index := bytes.IndexByte(data[offset:], '\n')
	if index < 0 {
		return len(data)
	}
	return offset + index
}

// writeAtomic writes data through a temp file in the target's directory.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "config.*.yaml.tmp")
	if err != nil {
		return fmt.Errorf("configfile: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("configfile: write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("configfile: fsync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("configfile: close temp: %w", err)
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode()
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return fmt.Errorf("configfile: chmod temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("configfile: rename: %w", err)
	}
	return nil
}

// Project is the subset of config.Project that the file stores.
type Project struct {
	Key            string            `yaml:"key"`
	Name           string            `yaml:"name"`
	Forge          string            `yaml:"forge"`
	Repo           string            `yaml:"repo"`
	DefaultBranch  string            `yaml:"default_branch"`
	Rigs           []string          `yaml:"rigs"`
	RigTemplate    string            `yaml:"rig_template"`
	ConcurrencyCap int               `yaml:"concurrency_cap"`
	GateCmd        string            `yaml:"gate_cmd"`
	Labels         map[string]string `yaml:"labels"`
	OnHold         *bool             `yaml:"onhold"`
	// CultureFile is where this project's rig culture lives. Omitted when empty:
	// config.Load derives it from the first rig, and a written `culture_file: ""`
	// would be a different (invalid) statement than "unset".
	CultureFile string `yaml:"culture_file,omitempty"`
	Paths       struct {
		Main      string `yaml:"main"`
		Worktrees string `yaml:"worktrees"`
	} `yaml:"paths"`
}
