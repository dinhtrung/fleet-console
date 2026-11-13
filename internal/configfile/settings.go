package configfile

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/dinhtrung/fleet-console/internal/config"
	"gopkg.in/yaml.v3"
)

// UpdateSettings writes top-level scalars and the rig_lifecycle block to the
// config file at path.
//
// It is UpsertProject with a different splice target and the same four
// guarantees (design D1): the caller's sha256 must still match the file on
// disk, the edit is textual so the operator's comments survive, the merged
// document is validated with the loader's own rules before anything is written,
// and the replace is atomic. A stale read returns ErrStaleRead; a document the
// loader would refuse returns *config.ValidationError and the file is untouched.
//
// values is keyed by YAML key. A top-level scalar is set in place, or appended
// when the file does not carry it yet. A rig_lifecycle field is addressed as
// rig_lifecycle.<field> and updates just that value inside the existing block,
// leaving the block's siblings and its comments alone. Every key must be
// writable per config.EditableKey; anything else is refused before the file is
// read, because an unwritable key is a caller bug, not a race.
//
// The outcome is OutcomeUnchanged when the merged bytes equal the bytes on
// disk, which is the "a double save is a no-op" answer the settings response
// reports as saved:false.
func UpdateSettings(path string, values map[string]string, expectedHash *[32]byte) (Outcome, error) {
	if len(values) == 0 {
		return OutcomeUnchanged, nil
	}
	// Refuse unwritable keys before touching the disk: a handler bug must not
	// reach the file even when the hash happens to match.
	keys := make([]string, 0, len(values))
	for key := range values {
		if ok, reason := config.EditableKey(key); !ok {
			return "", fmt.Errorf("configfile: %s", reason)
		}
		keys = append(keys, key)
	}
	// Sorted so the splice is deterministic: two identical requests produce
	// byte-identical files.
	sort.Strings(keys)

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

	out, err := spliceSettings(data, keys, values)
	if err != nil {
		return "", err
	}

	dir := filepath.Dir(path)
	home, _ := os.UserHomeDir()
	// The loader's own rules, including the cross-checks ValidateContent stops
	// short of: a settings write must never leave a file the app refuses to boot
	// from. The candidate bytes are validated in place before the atomic replace.
	if err := config.ValidateDocument(out, dir, home); err != nil {
		return "", err
	}
	if bytes.Equal(out, data) {
		return OutcomeUnchanged, nil
	}
	if err := writeAtomic(path, out); err != nil {
		return "", err
	}
	return OutcomeCreated, nil
}

// spliceSettings applies the settings to the document text. It reads the YAML
// node tree to locate each key's byte span, then rewrites only those spans, so
// whole-line comments, blank lines and every unrelated block keep their bytes.
func spliceSettings(data []byte, keys []string, values map[string]string) ([]byte, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return renderFresh(keys, values)
	}
	root, err := documentRoot(data)
	if err != nil {
		return nil, err
	}
	// Every span is read from the original bytes before any edit lands, then
	// applied from the highest offset down, so a length change never invalidates
	// a span still to be applied.
	type edit struct {
		start, end int
		body       string
	}
	edits := make([]edit, 0, len(keys))
	missing := make([]string, 0, len(keys))
	for _, key := range keys {
		span, quoted, found := keySpan(data, root, key)
		if !found {
			missing = append(missing, key)
			continue
		}
		body, err := encodeValue(key, values[key], quoted)
		if err != nil {
			return nil, err
		}
		edits = append(edits, edit{start: span.start, end: span.end, body: body})
	}
	sort.Slice(edits, func(a, b int) bool { return edits[a].start > edits[b].start })
	out := data
	for _, e := range edits {
		out = spliceRange(out, e.start, e.end, e.body)
	}
	for _, key := range missing {
		body, err := encodeValue(key, values[key], false)
		if err != nil {
			return nil, err
		}
		out = appendSetting(out, key, body)
	}
	return out, nil
}

// documentRoot parses data and returns the top-level mapping node.
func documentRoot(data []byte) (*yaml.Node, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("configfile: unmarshal: %w", err)
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) == 0 {
		return nil, fmt.Errorf("configfile: the file is not a mapping")
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("configfile: the file is not a mapping")
	}
	return root, nil
}

// span is a byte range in the document.
type span struct{ start, end int }

// keySpan returns the value's byte range for key and whether the operator quoted
// it. A dotted key addresses a field inside a nested block, so only that field's
// value is replaced. The third return is false when the file does not carry the
// key.
func keySpan(data []byte, root *yaml.Node, key string) (span, bool, bool) {
	block, field, nested := strings.Cut(key, ".")
	for index := 0; index+1 < len(root.Content); index += 2 {
		name := root.Content[index]
		value := root.Content[index+1]
		if nested {
			if name.Value != block || value.Kind != yaml.MappingNode {
				continue
			}
			for inner := 0; inner+1 < len(value.Content); inner += 2 {
				if value.Content[inner].Value != field {
					continue
				}
				if s, quoted, ok := valueSpan(data, value.Content[inner+1]); ok {
					return s, quoted, true
				}
				return span{}, false, false
			}
			return span{}, false, false
		}
		if name.Value != key {
			continue
		}
		if s, quoted, ok := valueSpan(data, value); ok {
			return s, quoted, true
		}
		return span{}, false, false
	}
	return span{}, false, false
}

// valueSpan returns the byte range of a scalar value on its key's line, keeping
// any trailing inline comment outside the range. A non-scalar value has no
// replaceable span: replacing a block wholesale would destroy its comments.
//
// The second return reports whether the operator wrote the value in double
// quotes and the third whether a replaceable span was found. An edit preserves
// the quote style, because rewriting `"30s"` as `30s` changes bytes nobody asked
// to change — and a re-save would then report a change that never happened.
func valueSpan(data []byte, value *yaml.Node) (span, bool, bool) {
	if value.Kind != yaml.ScalarNode || value.Line <= 0 || value.Column <= 0 {
		return span{}, false, false
	}
	start := lineOffset(data, value.Line) + value.Column - 1
	if start >= len(data) {
		return span{}, false, false
	}
	if data[start] == '"' {
		// Walk past the closing quote so the quote pair stays inside the range.
		for end := start + 1; end < len(data) && data[end] != '\n'; end++ {
			if data[end] == '"' {
				return span{start: start, end: end + 1}, true, true
			}
		}
		return span{start: start, end: len(data)}, true, true
	}
	end := start
	for end < len(data) && data[end] != '\n' {
		// A " #" pair starts an inline comment; leave it in place.
		if data[end] == ' ' && end+1 < len(data) && data[end+1] == '#' {
			break
		}
		end++
	}
	// Trailing spaces before the comment or the newline are not the value.
	for end > start && (data[end-1] == ' ' || data[end-1] == '\t') {
		end--
	}
	return span{start: start, end: end}, false, true
}

// encodeValue renders one YAML value. A boolean key renders as a bare bool, so a
// quoted "true" would decode as a string the bool field refuses. A duration key
// keeps the operator's quote style when the file already carried the key.
func encodeValue(key, value string, quoted bool) (string, error) {
	switch key {
	case "act", "rig_lifecycle.enabled":
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "true":
			return "true", nil
		case "false":
			return "false", nil
		}
		return "", fmt.Errorf("configfile: %s must be true or false, got %q", key, value)
	case "events_keep_days":
		trimmed := strings.TrimSpace(value)
		days, err := strconv.Atoi(trimmed)
		if err != nil || days < 0 {
			return "", fmt.Errorf("configfile: events_keep_days must be 0 or a positive number of days, got %q", value)
		}
		return strconv.Itoa(days), nil
	case "tick_interval", "notify_interval", "rig_lifecycle.idle_after", "rig_lifecycle.idle_poll_max":
		trimmed := strings.TrimSpace(value)
		if quoted {
			return `"` + trimmed + `"`, nil
		}
		return trimmed, nil
	}
	return "", fmt.Errorf("configfile: %s is not a writable scalar", key)
}

// appendSetting adds a key the file does not carry yet, after the last one.
func appendSetting(data []byte, key, body string) []byte {
	var out bytes.Buffer
	out.Write(data)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		out.WriteByte('\n')
	}
	if key == "rig_lifecycle" {
		out.WriteString(key + ":\n  enabled: " + body + "\n")
		return out.Bytes()
	}
	if block, field, nested := strings.Cut(key, "."); nested {
		// A field appended without its block needs the block header too.
		out.WriteString(block + ":\n  " + field + ": " + body + "\n")
		return out.Bytes()
	}
	out.WriteString(key + ": " + body + "\n")
	return out.Bytes()
}

// renderFresh writes a whole document when the file does not exist yet.
func renderFresh(keys []string, values map[string]string) ([]byte, error) {
	var out bytes.Buffer
	for _, key := range keys {
		body, err := encodeValue(key, values[key], true)
		if err != nil {
			return nil, err
		}
		out.Write(appendSetting(nil, key, body))
	}
	return out.Bytes(), nil
}

// spliceRange replaces data[start:end] with body.
func spliceRange(data []byte, start, end int, body string) []byte {
	var out bytes.Buffer
	out.Write(data[:start])
	out.WriteString(body)
	out.Write(data[end:])
	return out.Bytes()
}
