package config

import (
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

// Back-compat is a test, not a promise (design D6). `rigs: [a, b]` is the form every
// existing config uses, and the migration guarantee is that it resolves to two
// tier-0, on-full entries whose names, order and resolved Project match what main
// produced before tiers existed.
func TestRigsBackCompat(t *testing.T) {
	// The pre-tier shape: what a `[]string` field decoded this file into. The test
	// builds it by hand rather than depending on a type that no longer exists.
	legacy := []string{"a", "b"}

	var got struct {
		Rigs RigRefs `yaml:"rigs"`
	}
	if err := yaml.Unmarshal([]byte("rigs: [a, b]\n"), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(got.Rigs) != len(legacy) {
		t.Fatalf("rigs = %d entries, want %d", len(got.Rigs), len(legacy))
	}
	// Field by field, against the pre-tier list.
	for i, name := range legacy {
		ref := got.Rigs[i]
		if ref.Name != name {
			t.Fatalf("rigs[%d].Name = %q, want %q (position %d must not change)", i, ref.Name, name, i)
		}
		if ref.Tier != 0 {
			t.Fatalf("rigs[%d].Tier = %d, want 0: a flat list is one tier", i, ref.Tier)
		}
		if ref.Spill != SpillOnFull {
			t.Fatalf("rigs[%d].Spill = %q, want %q: the flat list's overflow behaviour", i, ref.Spill, SpillOnFull)
		}
	}
	// Names() must give back exactly the original list, so storage and the plan see
	// no difference at all.
	if !reflect.DeepEqual(got.Rigs.Names(), legacy) {
		t.Fatalf("Names() = %v, want %v", got.Rigs.Names(), legacy)
	}
}

// The resolved project must be field-for-field what main produced. The tier fields
// are the only addition, and for a flat list they carry the values that mean "no
// change": tier 0 and spill on-full.
func TestRigsBackCompatResolvedProject(t *testing.T) {
	var file struct {
		Projects []Project `yaml:"projects"`
	}
	raw := `projects:
  - key: app
    forge: github
    repo: o/r
    rig: primary
`
	if err := yaml.Unmarshal([]byte(raw), &file); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(file.Projects) != 1 {
		t.Fatalf("projects = %d, want 1", len(file.Projects))
	}
	project := &file.Projects[0]
	if err := ValidateProject(project, "/tmp", "/home/u"); err != nil {
		t.Fatalf("validate: %v", err)
	}

	// The single-rig spelling resolves through the same union: one tier-0 entry.
	if len(project.Rigs) != 1 {
		t.Fatalf("rigs = %+v, want one entry", project.Rigs)
	}
	ref := project.Rigs[0]
	if ref.Name != "primary" || ref.Tier != 0 || ref.Spill != SpillOnFull {
		t.Fatalf("rigs[0] = %+v, want {primary 0 on-full}", ref)
	}
	// The legacy fields are untouched: Rig is cleared after promotion, as before.
	if project.Rig != "" {
		t.Fatalf("Rig = %q, want it cleared after promotion", project.Rig)
	}
	if names := project.Rigs.Names(); len(names) != 1 || names[0] != "primary" {
		t.Fatalf("Names() = %v, want [primary]", names)
	}
}
