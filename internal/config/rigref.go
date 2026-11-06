package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Spill triggers decide when a later tier may be used. The zero value is
// SpillOnFull, so a file that never names a trigger gets today's behaviour:
// overflow only when the earlier tier has no free seat.
const (
	// SpillNever keeps the later tier cold while any earlier tier exists.
	SpillNever = "never"
	// SpillOnFull allows the later tier when no free seat remains in the
	// earlier tiers, the concurrency cap included. This is the default.
	SpillOnFull = "on-full"
	// SpillOnUnavailable allows the later tier only when an earlier-tier rig has
	// no running seat or its read failed.
	SpillOnUnavailable = "on-unavailable"
)

// RigRef is one entry of a project's `rigs` list: a rig name with the tier it
// belongs to and the trigger that lets a later tier spill into it.
//
// The union decode (design D1) is what keeps every existing config valid: a bare
// string is a tier-0 rig with spill on-full, which is exactly what `Rigs []string`
// meant before tiers existed. A mapping names the fields explicitly.
type RigRef struct {
	Name  string `yaml:"name"`
	Tier  int    `yaml:"tier"`
	Spill string `yaml:"spill"`
}

// UnmarshalYAML accepts either form. A scalar becomes a tier-0, on-full entry; a
// mapping reads name, tier and spill. Anything else is refused with the form
// named, so a typo fails at load instead of silently dropping a rig.
func (r *RigRef) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		name := strings.TrimSpace(node.Value)
		if name == "" {
			return fmt.Errorf("rigs: an entry must name a rig")
		}
		*r = RigRef{Name: name, Tier: 0, Spill: SpillOnFull}
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("rigs: an entry must be a rig name or a mapping with name, tier and spill")
	}
	// Decode into an alias so this method does not call itself: decoding into a
	// RigRef would re-enter UnmarshalYAML and recurse until the stack dies. The
	// alias carries the same fields, so the tags still apply. Defaults are set on
	// the alias first, because a plain struct decode leaves an omitted spill as ""
	// and validation would refuse a file that only set a name.
	type plain RigRef
	out := plain{Tier: 0, Spill: SpillOnFull}
	if err := node.Decode(&out); err != nil {
		return err
	}
	out.Name = strings.TrimSpace(out.Name)
	if out.Name == "" {
		return fmt.Errorf("rigs: a mapping entry must name a rig")
	}
	*r = RigRef(out)
	return nil
}

// Rigs returns the rig names in declaration order. Storage and the plan keep
// working in names; the tiers ride along in RigRefs.
func (r RigRefs) Names() []string {
	out := make([]string, 0, len(r))
	for _, ref := range r {
		out = append(out, ref.Name)
	}
	return out
}

// RigRefs is the declared list. The named type exists so the config layer can
// validate once and the rest of the app keeps treating the rigs as names.
type RigRefs []RigRef

// SpillNames lists the accepted triggers for an error message.
func SpillNames() string {
	return strings.Join([]string{SpillNever, SpillOnFull, SpillOnUnavailable}, ", ")
}

// validSpill reports whether the trigger is one this build understands.
func validSpill(spill string) bool {
	switch spill {
	case SpillNever, SpillOnFull, SpillOnUnavailable:
		return true
	}
	return false
}

// NamedRigs builds the tier-0, on-full list from plain names. It is the
// programmatic equivalent of the scalar YAML form, for callers and tests that
// build a Project in Go rather than reading one from a file.
func NamedRigs(names ...string) RigRefs {
	out := make(RigRefs, 0, len(names))
	for _, name := range names {
		out = append(out, RigRef{Name: name, Tier: 0, Spill: SpillOnFull})
	}
	return out
}
