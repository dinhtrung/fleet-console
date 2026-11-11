package act

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// A note is a verdict when its **first non-empty line** starts with the token and
// a sha — after stripDecoration has removed the markdown a model may have
// wrapped it in. Both anchors still matter:
//
//   - anchored at the start, so "Not ready to ACCEPT <sha> until CI is green" is
//     a sentence and not a verdict;
//   - not anchored at the end, so "REJECT <sha> (fails acceptance)" and
//     "ACCEPT <sha> (PR #78, #66)" still count — which is how a reviewer writes.
//
// The earlier pattern searched anywhere in the body, so prose that merely
// mentioned a token became a verdict.
var (
	verdictNote  = regexp.MustCompile(`(?i)^(ACCEPT|REJECT)\s+([0-9a-f]{7,40})\b`)
	verdictToken = regexp.MustCompile(`(?i)^(ACCEPT|REJECT)\b`)
	newline      = regexp.MustCompile(`\r?\n`)
)

// verdictDecoration is the cutset stripDecoration removes from the start of a
// line, in any order: heading marks, quote marks, list bullets, emphasis and
// code-span marks, and the whitespace around them. `**` comes out as two `*`,
// `__` as two `_`.
const verdictDecoration = "#>-*+`_ \t"

// verdictSeparator matches one separator — a colon, an em dash, or a dash —
// with the whitespace around it, immediately after the token: `ACCEPT: <sha>`
// and `ACCEPT — <sha>` say the same thing as `ACCEPT <sha>`, which is the one
// shape the note regex reads.
var verdictSeparator = regexp.MustCompile(`(?i)^((?:ACCEPT|REJECT)\b)\s*[:—-]\s*`)

// stripDecoration removes the markdown a model may wrap its verdict in, so the
// parser reads what the reviewer meant rather than how they wrapped it
// (epic #263, design D1). Three strips, in order, each at most once:
//
//   - the decoration marks themselves, from the start, in any order;
//   - one label closed by a separator — `## Verdict: ACCEPT <sha>` is a
//     heading around the verdict, so the `Verdict:` label goes with the marks.
//     Tried only when the token is not already at the start, so `ACCEPT: <sha>`
//     loses the separator after the token below, not the token with it;
//   - one separator immediately after the token, rewritten as the single
//     space the regexes expect.
//
// Everything else is left alone, deliberately. A sentence that merely mentions
// the token (`Not ready to ACCEPT <sha> until CI is green`) carries no
// separator before it, so it survives the strip and the anchored regexes still
// read it as prose; `ACCEPTED` is not `ACCEPT`; and a dash inside a word ends
// the label strip rather than eating half a sentence.
func stripDecoration(line string) string {
	s := strings.TrimLeft(line, verdictDecoration)
	if !verdictToken.MatchString(s) {
		if i := strings.IndexAny(s, ":—-"); i > 0 {
			s = strings.TrimLeft(s[i:], verdictDecoration+":—-")
		}
	}
	return verdictSeparator.ReplaceAllString(s, "$1 ")
}

// ErrUnattributableVerdict is returned when the newest verdict-shaped note cannot
// be tied to the head under review. The merge must not proceed: falling through
// to an older ACCEPT would merge on a decision the reviewer has since withdrawn.
var ErrUnattributableVerdict = errors.New("act: a verdict note cannot be attributed to this head")

// UnattributableVerdictError names the note that blocked the merge and why.
type UnattributableVerdictError struct {
	NoteID string
	Reason string
	Line   string
}

func (e *UnattributableVerdictError) Error() string {
	return fmt.Sprintf("%s: note %s: %s (%q)", ErrUnattributableVerdict, e.NoteID, e.Reason, firstLine(e.Line))
}

func (e *UnattributableVerdictError) Is(target error) bool { return target == ErrUnattributableVerdict }

// CapturedVerdict is the verdict stored from a reviewer note.
type CapturedVerdict struct {
	VerdictID int64
	Item      int // the item the verdict is about, not the change request number
	SHA       string
	Decision  string
	Source    string
	NoteID    string
}

// CaptureVerdict stores the newest note that decides the current head.
//
// Notes are read newest first. The first verdict-shaped note decides:
//
//   - ACCEPT naming the head is the verdict;
//   - REJECT naming the head is the verdict;
//   - anything else that claims to be a verdict is refused, not skipped. An
//     ACCEPT for a superseded head is harmless and scanning continues; a REJECT
//     that cannot be tied to this head, or a verdict whose sha does not parse,
//     stops the merge and names the note.
//
// Refusing matters because of what the alternative does: skipping an
// unattributable REJECT falls through to an older ACCEPT and merges a change the
// reviewer said no to. The caller reports the refusal and retries next tick; a
// reviewer who posts a parseable verdict unblocks it.
func (a *Actions) CaptureVerdict(ctx context.Context, dispatchID int64, item int, headSHA string, notes []forge.Comment) (*CapturedVerdict, error) {
	sorted := append([]forge.Comment(nil), notes...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].CreatedAt != sorted[j].CreatedAt {
			return sorted[i].CreatedAt > sorted[j].CreatedAt
		}
		return sorted[i].ID > sorted[j].ID
	})
	for _, note := range sorted {
		line := firstLine(note.Body)
		normal := stripDecoration(line)
		if !verdictToken.MatchString(normal) {
			continue
		}
		match := verdictNote.FindStringSubmatch(normal)
		if match == nil {
			// It claims to be a verdict and does not parse: a truncated or
			// mistyped sha, or the token with nothing after it. The error
			// quotes the line the reviewer wrote, not the normalised one.
			return nil, &UnattributableVerdictError{
				NoteID: note.ID, Line: line,
				Reason: "the first line starts with a verdict token but its sha does not parse",
			}
		}
		decision := strings.ToLower(match[1])
		sha := strings.ToLower(match[2])
		if headSHA != "" && sha != headSHA {
			if decision == "accept" {
				// An accept for a superseded head says nothing about this one.
				continue
			}
			return nil, &UnattributableVerdictError{
				NoteID: note.ID, Line: line,
				Reason: fmt.Sprintf("a rejection names %s, which is not the head under review (%s)", sha, headSHA),
			}
		}
		return a.storeVerdict(ctx, dispatchID, item, note.ID, sha, decision)
	}
	return nil, nil
}

// storeVerdict records the decision once. A tick re-reads the same notes, so the
// same verdict must not be added twice.
func (a *Actions) storeVerdict(ctx context.Context, dispatchID int64, item int, noteID, sha, decision string) (*CapturedVerdict, error) {
	// A verdict with no dispatch belongs to the item of one project: adoption's
	// case. Reading it back unscoped would find another project's row for the same
	// item number and mistake it for this one.
	existing, err := a.verdictsFor(ctx, dispatchID, item)
	if err != nil {
		return nil, err
	}
	for _, verdict := range existing {
		if verdict.SHA == sha && verdict.Decision == decision {
			return nil, nil
		}
	}
	verdictID, err := a.Store.AddVerdict(ctx, store.NewVerdict{
		DispatchID: dispatchID, ProjectID: a.ProjectID, ItemNumber: item,
		SHA: sha, Decision: decision, Source: "note " + noteID, At: a.now(),
	})
	if err != nil {
		return nil, err
	}
	if _, err := a.event(ctx, "act", "verdict.capture", fmt.Sprintf("#%d", item),
		fmt.Sprintf("%s %s from note %s", decision, sha, noteID)); err != nil {
		return nil, err
	}
	return &CapturedVerdict{
		VerdictID: verdictID, Item: item, SHA: sha, Decision: decision,
		Source: "note " + noteID, NoteID: noteID,
	}, nil
}

// verdictsFor reads the verdicts that already exist for this change, keyed the
// way the merge will read them: by dispatch when there is one, by item when the
// change request was adopted without a dispatch.
func (a *Actions) verdictsFor(ctx context.Context, dispatchID int64, item int) ([]store.VerdictRow, error) {
	if dispatchID != 0 {
		return a.Store.Verdicts(ctx, dispatchID)
	}
	return a.Store.VerdictsForItem(ctx, a.ProjectID, item)
}

// CaptureFromForge reads the change request's notes and stores the verdict.
//
// The two numbers are separate arguments because they are separate numbers: a
// pull request has its own number, and it is not the number of the issue it
// closes. A verdict is posted on the change request, so reading the item's
// conversation finds nothing, stores nothing, and leaves the merge gate waiting
// on an acceptance the reviewer did write. Passing changeRequest == 0 means the
// caller has no change request and the read is skipped.
func (a *Actions) CaptureFromForge(ctx context.Context, dispatchID int64, item, changeRequest int, headSHA string) (*CapturedVerdict, error) {
	if changeRequest <= 0 {
		return nil, fmt.Errorf("act: verdict capture needs a change request number, got %d", changeRequest)
	}
	notes, err := a.Forge.CRNotes(ctx, changeRequest)
	if err != nil {
		return nil, err
	}
	return a.CaptureVerdict(ctx, dispatchID, item, headSHA, notes)
}

// firstLine is the first non-empty line of a note: a verdict is the note's own
// statement, not something buried in an explanation.
func firstLine(body string) string {
	for _, line := range newline.Split(body, -1) {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
