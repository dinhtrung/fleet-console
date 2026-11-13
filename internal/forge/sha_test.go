package forge

import "testing"

// TestSameSHA pins the one rule the repo compares commit ids by (epic #316):
// case-insensitive, either side a prefix of the other, both at least git's
// 7-char minimum abbreviation. The old private copies in act and decide were
// this same rule; the table below is their union.
func TestSameSHA(t *testing.T) {
	const fullHead = "3f2a1b9c4d5e6f708192a3b4c5d6e7f8091a2b3c"
	cases := []struct {
		name string
		note string
		head string
		want bool
	}{
		{"identical full shas", fullHead, fullHead, true},
		{"note is a 7-char prefix of a 40-char head", "3f2a1b9", fullHead, true},
		{"note is a mid-length prefix", "3f2a1b9c4d5e", fullHead, true},
		{"upper case matches", "7F76634", "7f76634def5678901234abcdef56789012abcd", true},
		{"head abbreviated, note full", fullHead[:9], fullHead, true},
		{"a 6-char sha is not an abbreviation", "3f2a1b", fullHead, false},
		{"a 6-char head cannot match", "3f2a1b9", "3f2a1b", false},
		{"another commit's sha", "ffffffff", "3f2a1b9c4d5e6f708192a3b4c5d6e7f8091a2b3c", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SameSHA(tc.note, tc.head); got != tc.want {
				t.Fatalf("SameSHA(%q, %q) = %t, want %t", tc.note, tc.head, got, tc.want)
			}
		})
	}
}
