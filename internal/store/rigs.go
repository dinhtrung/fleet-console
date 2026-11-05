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

// RigTier is one rig's place in the fill order. It mirrors the config's rig entry
// without importing config: the store persists what the config resolved, and the
// decide loop reads it back from the project row.
type RigTier struct {
	Name  string `json:"name"`
	Tier  int    `json:"tier"`
	Spill string `json:"spill"`
}

// encodeRigTiers writes the tier list. An entry with no tier and no trigger is written
// as the bare name so an existing rigs_json stays readable by older readers.
func encodeRigTiers(tiers []RigTier) string {
	if len(tiers) == 0 {
		return "[]"
	}
	values := make([]any, 0, len(tiers))
	for _, tier := range tiers {
		if tier.Tier == 0 && (tier.Spill == "" || tier.Spill == "on-full") {
			values = append(values, tier.Name)
			continue
		}
		values = append(values, tier)
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

// decodeRigTiers reads rigs_json into tiers. A bare string is tier 0 with the default
// trigger, which is what the flat list meant before tiers existed.
func decodeRigTiers(raw, fallback string) []RigTier {
	var values []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &values); err != nil || len(values) == 0 {
		names := normalizeRigs(fallback, nil)
		out := make([]RigTier, 0, len(names))
		for _, name := range names {
			out = append(out, RigTier{Name: name, Tier: 0, Spill: "on-full"})
		}
		return out
	}
	out := make([]RigTier, 0, len(values))
	for _, value := range values {
		var name string
		if err := json.Unmarshal(value, &name); err == nil {
			if name = strings.TrimSpace(name); name != "" {
				out = append(out, RigTier{Name: name, Tier: 0, Spill: "on-full"})
			}
			continue
		}
		var tier RigTier
		if err := json.Unmarshal(value, &tier); err != nil {
			continue
		}
		if tier.Name = strings.TrimSpace(tier.Name); tier.Name == "" {
			continue
		}
		if tier.Spill == "" {
			tier.Spill = "on-full"
		}
		out = append(out, tier)
	}
	return out
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
