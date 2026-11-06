package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A bare string is the form every existing config uses. It must resolve to tier 0
// with spill on-full, or the union decode has broken the files it exists to keep
// valid.
func TestRigRefScalarForm(t *testing.T) {
	var got struct {
		Rigs RigRefs `yaml:"rigs"`
	}
	raw := "rigs: [primary, secondary]\n"
	if err := yaml.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Rigs) != 2 {
		t.Fatalf("rigs = %d entries, want 2", len(got.Rigs))
	}
	for i, want := range []string{"primary", "secondary"} {
		ref := got.Rigs[i]
		if ref.Name != want {
			t.Fatalf("rigs[%d].Name = %q, want %q", i, ref.Name, want)
		}
		if ref.Tier != 0 {
			t.Fatalf("rigs[%d].Tier = %d, want 0", i, ref.Tier)
		}
		if ref.Spill != SpillOnFull {
			t.Fatalf("rigs[%d].Spill = %q, want %q", i, ref.Spill, SpillOnFull)
		}
	}
}

// The mapping form names the fields. An omitted spill takes the default, so a
// file that only sets a tier still loads.
func TestRigRefMappingForm(t *testing.T) {
	var got struct {
		Rigs RigRefs `yaml:"rigs"`
	}
	raw := `rigs:
  - name: primary
  - name: standby
    tier: 1
    spill: on-unavailable
  - name: cold
    tier: 2
    spill: never
`
	if err := yaml.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Rigs) != 3 {
		t.Fatalf("rigs = %d entries, want 3", len(got.Rigs))
	}
	// Omitted tier and spill both default.
	if got.Rigs[0].Tier != 0 || got.Rigs[0].Spill != SpillOnFull {
		t.Fatalf("rigs[0] = %+v, want tier 0 spill on-full", got.Rigs[0])
	}
	if got.Rigs[1].Tier != 1 || got.Rigs[1].Spill != SpillOnUnavailable {
		t.Fatalf("rigs[1] = %+v, want tier 1 spill on-unavailable", got.Rigs[1])
	}
	if got.Rigs[2].Tier != 2 || got.Rigs[2].Spill != SpillNever {
		t.Fatalf("rigs[2] = %+v, want tier 2 spill never", got.Rigs[2])
	}
}

// Names is what storage and the plan consume, so it must preserve declaration
// order: the fill order depends on it.
func TestRigRefsNamesPreservesOrder(t *testing.T) {
	refs := RigRefs{
		{Name: "b", Tier: 1, Spill: SpillOnFull},
		{Name: "a", Tier: 0, Spill: SpillOnFull},
	}
	got := refs.Names()
	if len(got) != 2 || got[0] != "b" || got[1] != "a" {
		t.Fatalf("Names() = %v, want [b a] in declaration order", got)
	}
}

// The three refusals the spec names. Each is asserted through ValidateProject so
// the test proves the message reaches an operator, not only the decoder.
func TestRigRefRefusals(t *testing.T) {
	cases := []struct {
		name string
		rigs RigRefs
		want string
	}{
		{
			name: "duplicate name",
			rigs: RigRefs{{Name: "a", Tier: 0, Spill: SpillOnFull}, {Name: "a", Tier: 1, Spill: SpillOnFull}},
			want: "duplicates rig",
		},
		{
			name: "negative tier",
			rigs: RigRefs{{Name: "a", Tier: -1, Spill: SpillOnFull}},
			want: "negative tier",
		},
		{
			name: "unknown trigger",
			rigs: RigRefs{{Name: "a", Tier: 1, Spill: "when-busy"}},
			want: "unknown spill",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			project := &Project{Key: "app", Forge: "github", Repo: "o/r", Rigs: tc.rigs}
			err := ValidateProject(project, "/tmp", "/home/u")
			if err == nil {
				t.Fatalf("ValidateProject accepted %+v, want an error naming %q", tc.rigs, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
		})
	}
}

// The unknown trigger must name the value the file carried, so an operator can see
// which word is wrong rather than which entry.
func TestRigRefUnknownTriggerNamesTheValue(t *testing.T) {
	project := &Project{Key: "app", Forge: "github", Repo: "o/r",
		Rigs: RigRefs{{Name: "primary", Tier: 1, Spill: "when-busy"}}}
	err := ValidateProject(project, "/tmp", "/home/u")
	if err == nil {
		t.Fatal("want an error for spill when-busy")
	}
	for _, want := range []string{"when-busy", "primary", "never", "on-full", "on-unavailable"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to name %q", err.Error(), want)
		}
	}
}
