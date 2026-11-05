package decide

import (
	"strings"
	"testing"
)

// The spec's fill order, one table. Each row states the rigs, the free seats and every running
// builder, then what the plan may use and why a later tier was refused.
func TestFillSeatsTiers(t *testing.T) {
	rigs := []RigRef{
		{Name: "primary", Tier: 0, Spill: SpillOnFull},
		{Name: "standby", Tier: 1, Spill: SpillOnFull},
	}
	coldStandby := []RigRef{
		{Name: "primary", Tier: 0, Spill: SpillOnFull},
		{Name: "standby", Tier: 1, Spill: SpillOnUnavailable},
	}
	neverSpill := []RigRef{
		{Name: "primary", Tier: 0, Spill: SpillOnFull},
		{Name: "standby", Tier: 1, Spill: SpillNever},
	}

	cases := []struct {
		name       string
		rigs       []RigRef
		free       []string
		builders   []string
		cap        int
		wantSeats  []string
		wantRefuse string
	}{
		{
			name:      "a free tier-0 seat wins and tier 1 is never reached",
			rigs:      rigs,
			free:      []string{"dev-build-1@primary", "dev-build-2@standby"},
			builders:  []string{"dev-build-1@primary", "dev-build-2@standby"},
			wantSeats: []string{"dev-build-1@primary"},
		},
		{
			name:      "tier 1 is used on on-full overflow",
			rigs:      rigs,
			free:      []string{"dev-build-2@standby"},
			builders:  []string{"dev-build-1@primary", "dev-build-2@standby"},
			wantSeats: []string{"dev-build-2@standby"},
		},
		{
			name:       "tier 1 is not used when on-unavailable and tier 0 is running",
			rigs:       coldStandby,
			free:       []string{"dev-build-2@standby"},
			builders:   []string{"dev-build-1@primary", "dev-build-2@standby"},
			wantRefuse: "spill=on-unavailable",
		},
		{
			name:      "tier 1 is used when tier 0 has zero seats",
			rigs:      coldStandby,
			free:      []string{"dev-build-2@standby"},
			builders:  []string{"dev-build-2@standby"},
			wantSeats: []string{"dev-build-2@standby"},
		},
		{
			name:       "a foreign seat is ignored",
			rigs:       rigs,
			free:       []string{"dev-build-9@elsewhere"},
			builders:   []string{"dev-build-1@primary", "dev-build-9@elsewhere"},
			wantRefuse: "tier 0",
		},
		{
			name:      "the cap counting as full allows tier 1",
			rigs:      rigs,
			free:      []string{"dev-build-2@standby"},
			builders:  []string{"dev-build-2@standby"},
			cap:       1,
			wantSeats: []string{"dev-build-2@standby"},
		},
		{
			name:       "never keeps the later tier cold",
			rigs:       neverSpill,
			free:       []string{"dev-build-2@standby"},
			builders:   []string{"dev-build-2@standby"},
			wantRefuse: "spill=never",
		},
		{
			name:      "no tier declared keeps the flat list",
			free:      []string{"dev-build-1@primary", "dev-build-2@standby"},
			builders:  []string{"dev-build-1@primary", "dev-build-2@standby"},
			wantSeats: []string{"dev-build-1@primary", "dev-build-2@standby"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, refusal := fillSeats(tc.rigs, tc.free, tc.builders, tc.cap)
			if len(got) != len(tc.wantSeats) {
				t.Fatalf("seats = %v, want %v (refusal %q)", got, tc.wantSeats, refusal.String())
			}
			for i := range got {
				if got[i] != tc.wantSeats[i] {
					t.Fatalf("seats = %v, want %v", got, tc.wantSeats)
				}
			}
			reason := refusal.String()
			if tc.wantRefuse == "" && reason != "" {
				t.Fatalf("refusal = %q, want none", reason)
			}
			if tc.wantRefuse != "" && !strings.Contains(reason, tc.wantRefuse) {
				t.Fatalf("refusal = %q, want it to name %q", reason, tc.wantRefuse)
			}
			if tc.wantRefuse != "" && !strings.HasPrefix(reason, "tier ") {
				t.Fatalf("refusal = %q, want the spec's `tier N (spill=…: …)` form", reason)
			}
		})
	}
}
