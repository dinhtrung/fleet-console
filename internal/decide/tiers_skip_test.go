package decide

import (
	"strings"
	"testing"
)

// Task 2.2: every no_free_builder skip names the tier and the trigger that refused it. Driven through
// Decide so the detail the operator reads is what this asserts, not fillSeats' return value.
func TestNoFreeBuilderSkipNamesTheTier(t *testing.T) {
	item := Item{Number: 42, Kind: KindTask, State: "open", Title: "ready work"}

	cases := []struct {
		name  string
		rigs  []RigRef
		free  []string
		build []string
		busy  []string
		want  string
	}{
		{
			name: "a cold standby with on-unavailable",
			rigs: []RigRef{
				{Name: "primary", Tier: 0, Spill: SpillOnFull},
				{Name: "standby", Tier: 1, Spill: SpillOnUnavailable},
			},
			free:  []string{"dev-build-2@standby"},
			build: []string{"dev-build-1@primary", "dev-build-2@standby"},
			busy:  []string{"dev-build-1@primary"},
			want:  "tier 1 (spill=on-unavailable: every earlier tier has a running seat on standby)",
		},
		{
			name: "a never tier",
			rigs: []RigRef{
				{Name: "primary", Tier: 0, Spill: SpillOnFull},
				{Name: "standby", Tier: 1, Spill: SpillNever},
			},
			free:  []string{"dev-build-2@standby"},
			build: []string{"dev-build-2@standby"},
			want:  "tier 1 (spill=never: an earlier tier exists on standby)",
		},
		{
			name:  "every tier empty",
			rigs:  []RigRef{{Name: "primary", Tier: 0, Spill: SpillOnFull}},
			free:  nil,
			build: []string{"dev-build-1@primary"},
			busy:  []string{"dev-build-1@primary"},
			want:  "tier 0 (spill=on-full: no free seat on this rig on primary)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows := make([]Row, 0, len(tc.busy))
			for _, seat := range tc.busy {
				rows = append(rows, Row{State: "working", Destination: seat})
			}
			s := Snapshot{
				Project:  Project{Key: "app", Forge: ForgeGitHub, Enabled: true, Rigs: tc.rigs},
				Items:    []Item{item},
				Builders: tc.build,
				Rows:     rows,
				ForgeOK:  true,
			}
			result := Decide(s)
			if len(result.Dispatches) != 0 {
				t.Fatalf("dispatches = %+v, want none", result.Dispatches)
			}
			var found bool
			for _, skip := range result.Skips {
				if skip.Reason != ReasonNoFreeBuilder {
					continue
				}
				found = true
				if skip.Detail != tc.want {
					t.Fatalf("skip detail =\n  %q\nwant\n  %q", skip.Detail, tc.want)
				}
			}
			if !found {
				t.Fatalf("skips = %+v, want one %s", result.Skips, ReasonNoFreeBuilder)
			}
		})
	}
}

// A flat list (no tiers) still skips with a detail rather than the bare reason it used to carry.
func TestNoFreeBuilderFlatListSkipHasDetail(t *testing.T) {
	s := Snapshot{
		Project: Project{Key: "app", Forge: ForgeGitHub, Enabled: true},
		Items:   []Item{{Number: 7, Kind: KindTask, State: "open", Title: "work"}},
		ForgeOK: true,
	}
	result := Decide(s)
	if len(result.Skips) != 1 {
		t.Fatalf("skips = %+v, want one", result.Skips)
	}
	if result.Skips[0].Detail == "" {
		t.Fatalf("skip detail is empty; the operator cannot tell why nothing was dispatched")
	}
	if !strings.Contains(result.Skips[0].Detail, "seat") {
		t.Fatalf("skip detail = %q, want it to say there is no seat", result.Skips[0].Detail)
	}
}
