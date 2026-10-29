package store

import (
	"encoding/json"
	"strings"
)

// normalizeRigs returns the ordered rig list for a project. rig is the legacy
// single-name column and only contributes when rigs is empty.
func normalizeRigs(rig string, rigs []string) []string {
	values := make([]string, 0, len(rigs)+1)
	seen := make(map[string]bool, len(rigs)+1)
	for _, value := range rigs {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		values = append(values, value)
	}
	if len(values) == 0 {
		if value := strings.TrimSpace(rig); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func encodeRigs(rigs []string) string {
	raw, err := json.Marshal(rigs)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

// decodeRigs reads the list column, falling back to the legacy rig column for
// rows written before schemaV3.
func decodeRigs(raw, fallback string) []string {
	var rigs []string
	if err := json.Unmarshal([]byte(raw), &rigs); err == nil {
		if values := normalizeRigs("", rigs); len(values) > 0 {
			return values
		}
	}
	return normalizeRigs(fallback, nil)
}

func sameRigs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}
