package forge

import "testing"

// The body of #452, verbatim. It explains the convention and quotes it in an
// inline code span; read as prose it pairs that change request with item 450,
// which it never touched.
const quotedConventionBody = "The rule came from #314 (never infer an item from a title). But by that point the " +
	"app has already read `Closes #450` from the body — an explicit, machine-checked statement.\n"

func TestParseClosesIgnoresAQuotedKeyword(t *testing.T) {
	if got := ParseCloses(quotedConventionBody); got != 0 {
		t.Fatalf("closes = %d, want 0: the keyword is quoted in a code span, not stated", got)
	}
}

func TestParseClosesReadsStatements(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{"bare line", "Closes #12\n", 12},
		{"mid-sentence", "This closes #12.", 12},
		{"lower case", "closes #12", 12},
		{"past tense", "Closed #12", 12},
		{"fixes", "Fixes #7", 7},
		{"fixed", "fixed #8", 8},
		{"resolves", "Resolves #9", 9},
		{"after prose", "## Summary\n\nSome prose about the change.\n\nCloses #34\n", 34},
		{"beside a quote", "We write `Closes #12` in bodies.\n\nCloses #34\n", 34},
		{"inline code elsewhere", "Run `go test ./...` first.\n\nCloses #5\n", 5},
		{"link text", "Closes #450 (see #452)", 450},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseCloses(tc.body); got != tc.want {
				t.Fatalf("closes = %d, want %d for %q", got, tc.want, tc.body)
			}
		})
	}
}

func TestParseClosesIgnoresFencedBlocks(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{
			name: "block only",
			body: "Example:\n\n```\nCloses #99\n```\n",
			want: 0,
		},
		{
			name: "block with a language",
			body: "```text\nFixes #99\n```\n",
			want: 0,
		},
		{
			name: "tilde block",
			body: "~~~\nResolves #99\n~~~\n",
			want: 0,
		},
		{
			name: "block then a real statement",
			body: "The gate reads this form:\n\n```\nCloses #99\n```\n\nAnd this one states it: Closes #12\n",
			want: 12,
		},
		{
			name: "unterminated block quotes the rest",
			body: "Example:\n\n```\nCloses #99\n",
			want: 0,
		},
		{
			name: "indented fence",
			body: "   ```\n   Closes #99\n   ```\n",
			want: 0,
		},
		{
			name: "four spaces is not a fence to us",
			body: "    ```\nCloses #99\n",
			want: 99,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseCloses(tc.body); got != tc.want {
				t.Fatalf("closes = %d, want %d for %q", got, tc.want, tc.body)
			}
		})
	}
}

func TestParseClosesIgnoresLongerInlineRuns(t *testing.T) {
	// A span delimited by two backticks may quote a single backtick inside it,
	// so the run length has to be respected rather than scanning to the next one.
	body := "Write ``a ` Closes #99`` by hand.\n\nCloses #12\n"
	if got := ParseCloses(body); got != 12 {
		t.Fatalf("closes = %d, want 12: the double-backtick span quotes #99", got)
	}
	if got := ParseCloses("Write ``a ` Closes #99`` by hand.\n"); got != 0 {
		t.Fatalf("closes = %d, want 0 when the span is the only mention", got)
	}
}

func TestParseClosesUnterminatedSpanQuotesTheLine(t *testing.T) {
	// An odd backtick is a typo, not a statement of intent to close item 99.
	if got := ParseCloses("we quote `Closes #99 in prose\n"); got != 0 {
		t.Fatalf("closes = %d, want 0: an unclosed span quotes the line", got)
	}
	if got := ParseCloses("we quote `Closes #99 in prose\n\nCloses #12\n"); got != 12 {
		t.Fatalf("closes = %d, want 12: the next line is prose again", got)
	}
}
