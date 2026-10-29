package store

import (
	"encoding/json"
	"strings"
)

// normalizeRigs keeps the single-rig column and the list consistent.
func normalizeRigs(rig string, rigs []string) []string {
	values := make([]string, 0, len(rigs)+1)
	if len(rigs) == 0 && strings.TrimSpace(rig) != "" {
		values = append(values, strings.TrimSpace(rig))
	}
	seen := make(map[string]bool, len(rigs))
	for _, value := range rigs {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		values = append(values, value)
	}
	if len(values) == 0 && strings.TrimSpace(rig) != "" {
		values = append(values, strings.TrimSpace(rig))
	}
	return values
}

func encodeRigs(rigs []string) string {
	if len(rigs) == 0 {
		return "[]"
	}
	raw, err := json.Marshal(rigs)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

func decodeRigs(raw, fallback string) []string {
	var rigs []string
	if err := json.Unmarshal([]byte(raw), &rigs); err != nil || len(rigs) == 0 {
		return normalizeRigs(fallback, nil)
	}
	return normalizeRigs("", rigs)
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
