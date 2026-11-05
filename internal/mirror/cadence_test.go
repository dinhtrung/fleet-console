package mirror

import (
	"testing"
	"time"
)

// The spec's four scenarios, one table: quiet ticks double from tick_interval,
// the ceiling holds, a busy tick restores the base and clears the streak, and a
// second finding keeps it at the base.
func TestCadenceSequence(t *testing.T) {
	const base = time.Minute
	const max = 4 * time.Minute

	tests := []struct {
		name   string
		quiet  []bool
		want   []time.Duration
		streak int
	}{
		{
			name:   "four quiet ticks double, capped",
			quiet:  []bool{true, true, true, true},
			want:   []time.Duration{base, 2 * base, max, max},
			streak: 4,
		},
		{
			name:   "the ceiling holds on every later quiet tick",
			quiet:  []bool{true, true, true, true, true, true},
			want:   []time.Duration{base, 2 * base, max, max, max, max},
			streak: 6,
		},
		{
			name:   "one finding restores the cadence",
			quiet:  []bool{true, true, true, false},
			want:   []time.Duration{base, 2 * base, max, base},
			streak: 0,
		},
		{
			name:   "a second finding keeps it at the base",
			quiet:  []bool{true, true, false, false},
			want:   []time.Duration{base, 2 * base, base, base},
			streak: 0,
		},
		{
			name:   "a quiet tick after a reset starts the climb over",
			quiet:  []bool{true, true, false, true},
			want:   []time.Duration{base, 2 * base, base, base},
			streak: 1,
		},
		{
			name:   "max equal to base never doubles",
			quiet:  []bool{true, true, true},
			want:   []time.Duration{base, base, base},
			streak: 3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ceiling := max
			if tc.name == "max equal to base never doubles" {
				ceiling = base
			}
			c := Cadence{Base: base, Max: ceiling}
			if got := c.Streak(); got != 0 {
				t.Fatalf("fresh streak = %d, want 0", got)
			}
			for i, quiet := range tc.quiet {
				got := c.Next(quiet)
				if got != tc.want[i] {
					t.Fatalf("tick %d quiet=%v: wait = %v, want %v", i+1, quiet, got, tc.want[i])
				}
			}
			if got := c.Streak(); got != tc.streak {
				t.Fatalf("streak = %d, want %d", got, tc.streak)
			}
		})
	}
}

// A cadence with no ceiling stays at the base rather than overflowing. Config
// load refuses a real ceiling below the base, so this only guards the zero value.
func TestCadenceWithoutCeiling(t *testing.T) {
	const base = time.Minute
	c := Cadence{Base: base}
	for i := range 100 {
		if got := c.Next(true); got != base {
			t.Fatalf("tick %d: wait = %v, want %v", i+1, got, base)
		}
	}
}
