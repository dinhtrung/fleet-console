package forge

import "testing"

// TestParseClosesIgnoresCode (#455): a body that quotes the convention is not
// stating it. The observed case is #452's own sentence — the app had read
// `Closes #450` from prose *about* the rule and paired a merged change request
// with an unrelated open item. Code is not prose; line position is not the
// question, because "this closes #12." mid-sentence is an honest body.
func TestParseClosesIgnoresCode(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{"plain prose", "This closes #12.", 12},
		{"closing word at line start", "Closes #7", 7},
		{
			"the #452 sentence verbatim",
			"The rule came from #314 (never infer an item from a title). But by that point the app has " +
				"already read `Closes #450` from the body — an explicit, machine-checked statement.",
			0,
		},
		{"fenced block only", "Examples:\n```\nCloses #450\n```\nNothing else.", 0},
		{"tilde fence only", "~~~\nfixes #9\n~~~", 0},
		{"prose outside the fence still counts", "```\nCloses #1\n```\nThis closes #7.", 7},
		{"inline code then prose", "Use `Closes #450`; this closes #8.", 8},
		{"an unmatched backtick is literal text", "a ` b then Closes #7", 7},
		{"an unclosed fence runs to the end of the body", "text\n```\nCloses #3", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseCloses(tc.body); got != tc.want {
				t.Fatalf("ParseCloses(%q) = %d, want %d", tc.body, got, tc.want)
			}
		})
	}
}
