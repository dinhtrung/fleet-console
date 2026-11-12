package rig

import "testing"

// TestIsHumanSeat: the daemon's own seat shape, and nothing wider. A suffix
// after "human-" goes to a named person; anything else that starts with
// "human" is an ordinary seat.
func TestIsHumanSeat(t *testing.T) {
	cases := []struct {
		session string
		want    bool
	}{
		{"human@kernel", true},
		{"human@host", true},
		{"human-alice@kernel", true},
		{"human-bot.x@host", true},
		{"humanity@kernel", false},
		{"human@rig", false},
		{"human@kernel2", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsHumanSeat(tc.session); got != tc.want {
			t.Errorf("IsHumanSeat(%q) = %v, want %v", tc.session, got, tc.want)
		}
	}
}
