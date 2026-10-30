package configfile

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/dinhtrung/fleet-console/internal/config"
	"gopkg.in/yaml.v3"
)

// Outcome is the result of an upsert.
type Outcome string

const (
	OutcomeCreated   Outcome = "Created"
	OutcomeUnchanged Outcome = "Unchanged"
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
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("configfile: read %s: %w", path, err)
		}
		data = nil
	}
	if expectedHash != nil {
		if now := sha256.Sum256(data); now != *expectedHash {
			return "", fmt.Errorf("config.yaml changed on disk; reload and retry")
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
		return nil, "", fmt.Errorf("duplicate project key %q", in.Key)
	}

	indent := itemIndent(data, sequence)
	body := indentEntry(entry, indent)
	insertAt := sequenceEnd(data, sequence)
	return spliceAt(data, insertAt, body), OutcomeCreated, nil
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
	start := lineOffset(data, sequence.Content[0].Line)
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
	Paths          struct {
		Main      string `yaml:"main"`
		Worktrees string `yaml:"worktrees"`
	} `yaml:"paths"`
}
