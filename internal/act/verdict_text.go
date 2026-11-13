package act

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/dinhtrung/fleet-console/internal/forge"
)

// The text scan is the second reader of a reviewer's note. The first one (the
// anchored `verdictNote` grammar) reads the note's own first line exactly. A
// model that wraps its verdict — `QA ACCEPT <sha>`, `🎯 **ACCEPT** <sha>`,
// `[ACCEPT](url) <sha>`, `Verdict: ACCEPT <sha>` on a line of its own — slips
// past it, and the merge gate then reports "no ACCEPT verdict" while the note
// plainly says the opposite.
//
// So the gate reads the note's first verdictWindow characters as text, line by
// line, and looks for a verdict word that *opens* a line once decoration, a
// link, an icon and a short label are stripped. Opening the line is the rule
// that keeps prose prose: "Not ready to ACCEPT <sha> until CI is green" is a
// sentence about a verdict, not a verdict, and merging on it would merge a
// change the reviewer just refused to accept (#264).
//
// What the scan cannot read it does not guess: a note with no sha, both
// polarities, or a negated verdict becomes a question for the operator seat.
const verdictWindow = 1024

var (
	// linkTarget rewrites `[ACCEPT](https://…)` to its text: a link that
	// carries a sha in its URL would otherwise attribute the verdict to the
	// wrong commit.
	linkTarget = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	// labelPrefix is the short label a model likes to put in front of its
	// verdict. The list is closed on purpose: an open "any word" rule reads
	// "Ready to accept" as a verdict.
	labelPrefix = regexp.MustCompile(`(?i)^(qa|verdict|review|result|decision|kết quả|đánh giá)\b[\s:—\-|*]*`)
	// tokenAtStart is the verdict itself, at the start of what is left of the line.
	tokenAtStart = regexp.MustCompile(`(?i)^(accept|reject)\b`)
	// negatedAtStart is a label-stripped line that argues with itself.
	negatedAtStart = regexp.MustCompile(`(?i)^(not|cannot|can ?not|can't|won't|will not|never|do not|don't)\s+(accept|approve|merge|land|reject)\b`)
	// hexToken is a sha as a reviewer writes it: 4 to 40 hex characters. The
	// gate captures on 7 or more (forge.SameSHA's rule) and on 4 to 6 only when
	// they are a prefix of the head under review.
	hexToken = regexp.MustCompile(`(?i)\b[0-9a-f]{4,40}\b`)
	// markdownFence and htmlComment blank the parts of a note a model uses for
	// examples rather than for its verdict.
	markdownFence = regexp.MustCompile("(?s)```.*?```")
	htmlComment   = regexp.MustCompile(`(?s)<!--.*?-->`)
	emphasis      = regexp.MustCompile(`\*\*|__|~~`)
)

// verdictScan is what the text scan found: a decision (with the sha it read),
// the line it read it from, or the reason it could not decide.
type verdictScan struct {
	Decision string // "accept", "reject", or "" when the note states no verdict
	SHA      string // lowercased; empty when the note named no commit
	Reason   string // non-empty when the note reads like a verdict but is not one
	Line     string // the line the verdict word was read from, for the question
}

// scanVerdictText reads a note's first verdictWindow characters and answers
// whether they state a verdict, and about which commit.
//
// Rules, in order: a line that opens with a verdict word (after decoration, a
// link, icons and one short label) states a verdict. Two such lines with
// opposite words argue with themselves. A line that opens with a negation
// refuses to be a verdict. A single line names its commit by sha; the sha may
// sit after the word or anywhere in the window, because models put it on the
// next line as often as beside it. A sha of 4 to 6 characters counts only when
// it is a prefix of the head — otherwise it is too short to be a claim about
// any particular commit, and the note is skipped rather than guessed at.
func scanVerdictText(body, headSHA string) verdictScan {
	text := verdictText(body)
	decisions := map[string]bool{}
	line, sha := "", ""
	for _, raw := range strings.Split(text, "\n") {
		normal := normalizeVerdictLine(raw)
		if normal == "" {
			continue
		}
		if negatedAtStart.MatchString(normal) {
			return verdictScan{Reason: fmt.Sprintf("the line negates the verdict: %q", strings.TrimSpace(raw)), Line: strings.TrimSpace(raw)}
		}
		match := tokenAtStart.FindStringSubmatchIndex(normal)
		if match == nil {
			continue
		}
		decision := strings.ToLower(normal[match[2]:match[3]])
		decisions[decision] = true
		if line == "" {
			line, sha = strings.TrimSpace(raw), shaNear(normal[match[3]:], text)
		}
	}
	switch len(decisions) {
	case 0:
		return verdictScan{}
	case 1:
	default:
		return verdictScan{Reason: "the note states both ACCEPT and REJECT", Line: line}
	}
	decision := "accept"
	if decisions["reject"] {
		decision = "reject"
	}
	if sha == "" {
		return verdictScan{Decision: decision, Line: line}
	}
	if len(sha) < 7 && headSHA != "" && !strings.HasPrefix(strings.ToLower(headSHA), sha) {
		// Too short to name a commit, and not the head: no verdict, no question.
		return verdictScan{}
	}
	if len(sha) < 7 {
		sha = strings.ToLower(headSHA)
	}
	return verdictScan{Decision: decision, SHA: sha, Line: line}
}

// shaNear finds the sha a verdict line names: the first one after the word,
// then anywhere in the window (models put the sha on the following line).
func shaNear(after, text string) string {
	if sha := hexToken.FindString(after); sha != "" {
		return strings.ToLower(sha)
	}
	return strings.ToLower(hexToken.FindString(text))
}

// normalizeVerdictLine strips what a model puts in front of a verdict: list
// decoration, a link, emphasis, icons, and one short label. What remains starts
// with the verdict word — or does not, and the line is prose.
func normalizeVerdictLine(line string) string {
	line = strings.TrimSpace(line)
	line = linkTarget.ReplaceAllString(line, "$1")
	line = emphasis.ReplaceAllString(line, "")
	for {
		trimmed := strings.TrimLeftFunc(line, func(r rune) bool {
			return !unicode.IsLetter(r)
		})
		trimmed = strings.TrimSpace(trimmed)
		labelled := labelPrefix.ReplaceAllString(trimmed, "")
		if labelled == trimmed {
			return trimmed
		}
		line = labelled
	}
}

// verdictText is the note as the scan reads it: fences and HTML comments
// blanked, one line per line, and no more than verdictWindow characters. The
// cut is on a rune boundary so a sha or a word at the edge is never split.
func verdictText(body string) string {
	text := markdownFence.ReplaceAllString(body, " ")
	text = htmlComment.ReplaceAllString(text, " ")
	if utf8.RuneCountInString(text) <= verdictWindow {
		return text
	}
	return string([]rune(text)[:verdictWindow])
}

// UnresolvedVerdictError is a note that states a verdict the gate cannot
// attribute: no sha, both polarities, or a negation. It is not a refusal — the
// gate has no fact to act on, so the tick asks the operator seat once per
// (item, head, note) instead of staying silent.
type UnresolvedVerdictError struct {
	NoteID  string
	Head    string
	Reason  string
	Excerpt string // the line the scan read, quoted in the question
}

func (e *UnresolvedVerdictError) Error() string {
	return fmt.Sprintf("note %s reads like a verdict but cannot be attributed: %s (%q)", e.NoteID, e.Reason, e.Excerpt)
}

// sameHead answers whether a scanned sha names the head under review.
func sameHead(sha, head string) bool {
	if head == "" {
		return true
	}
	return forge.SameSHA(sha, head)
}
