package act

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dinhtrung/fleet-console/internal/config"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// operatorDestination is the one resolution of where an operator question
// goes (verdict-relay, design D4): the project's verdict_relay.seat when its
// relay mode is operator, human@kernel otherwise — and always human@kernel
// when the project never opted in or the seat is empty. The decision case and
// the relay raise both answer through this helper, so a project cannot route
// its questions and its relays to different seats by accident.
func operatorDestination(mode, seat string) string {
	seat = strings.TrimSpace(seat)
	if mode == config.RelayModeOperator && seat != "" {
		return seat
	}
	return "human@kernel"
}

// RaiseVerdictRelay raises the one row that asks a seat to turn a finished
// review into a verdict note (verdict-relay, design D2/D9). The plan named
// the finished review (decide's relay arm); this reads the review's notes
// through the forge the gate already reads, writes the brief carrying them
// verbatim with the exact posting command, and creates one row to the
// configured seat. It writes nothing on the forge and merges nothing: the
// note the relay posts is judged by the merge gate's own four facts on a
// later tick.
//
// The event ledger dedupes per (item, head) — the key carries a terminator,
// so head=aaaa111 never matches head=aaaa1110's event — and a moved head is
// a new fact that relays again.
func (a *Actions) RaiseVerdictRelay(ctx context.Context, project store.ProjectRow, item, changeRequest int, text, kind, headSHA, seat, worktreeRoot string) (*RaiseResult, error) {
	if a.Store == nil || a.Rig == nil {
		return nil, fmt.Errorf("raise verdict relay: store and rig are required")
	}
	seat = strings.TrimSpace(seat)
	if seat == "" {
		return nil, fmt.Errorf("raise verdict relay: the project has no verdict_relay.seat")
	}
	subject := fmt.Sprintf("#%d", item)
	sha := strings.ToLower(headSHA)
	key := fmt.Sprintf("head=%s;", sha)
	exists, err := a.Store.HasEvent(ctx, kind, subject, key)
	if err != nil {
		return nil, err
	}
	if exists {
		return &RaiseResult{NoOp: true}, nil
	}

	notes, err := a.Forge.CRNotes(ctx, changeRequest)
	if err != nil {
		return nil, fmt.Errorf("raise verdict relay: read notes: %w", err)
	}

	dir := worktreeRoot
	if dir == "" {
		dir = os.TempDir()
	} else {
		dir = filepath.Join(dir, "briefs")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("raise verdict relay: brief dir: %w", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("relay-%s-%d-%s.md", slug(project.Key), item, head8(sha)))
	if err := os.WriteFile(path, []byte(relayBrief(project, item, changeRequest, sha, seat, notes)), 0o644); err != nil {
		return nil, fmt.Errorf("raise verdict relay: write brief: %w", err)
	}
	summary := text
	if len(summary) > 120 {
		summary = summary[:120]
	}
	row, err := a.Rig.QueueCreate(ctx, rig.CreateRequest{
		Destination: seat, Summary: summary, BodyFile: path, Source: "human@kernel", EvidenceRef: path,
	})
	if err != nil {
		_ = noteRefusal(ctx, a.Store, item, err)
		return nil, err
	}
	eventID, err := a.event(ctx, "act", kind, subject, fmt.Sprintf("head=%s; row %s", sha, row.ID))
	if err != nil {
		return nil, err
	}
	return &RaiseResult{QItemID: row.ID, Events: []int64{eventID}}, nil
}

// head8 is the brief filename's short head, matching the brief-name rule.
func head8(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// relayVerdictCommand is the relay's posting command: the same two forms
// verdictCommand builds, with the note the relay must write — the verdict
// line and the required `via:` second line (design D3). The body carries a
// literal newline inside the quoted argument, so the note lands as two lines
// and capture reads the marker.
func relayVerdictCommand(project store.ProjectRow, changeRequest int, sha, seat string) string {
	body := fmt.Sprintf("ACCEPT %s\nvia: %s", sha, seat)
	if strings.Contains(strings.ToLower(project.ForgeKind), "gitlab") {
		return fmt.Sprintf("glab api -X POST 'projects/%s/merge_requests/%d/notes' -F 'body=%s'",
			project.ForgeProject, changeRequest, body)
	}
	return fmt.Sprintf("gh pr comment %d -R %s --body '%s'", changeRequest, project.ForgeProject, body)
}

// relayBrief renders the brief itself (design D9): the review verbatim with
// note ids, the full head sha, the item and change request numbers, the exact
// posting command with the `via:` line substituted, the INCONCLUSIVE
// instruction, and the forbidden list. The relay reads no state itself —
// everything it needs is on this page.
func relayBrief(project store.ProjectRow, item, changeRequest int, sha, seat string, notes []forge.Comment) string {
	var review strings.Builder
	if len(notes) == 0 {
		review.WriteString("The change request carries no notes.\n")
	} else {
		for _, note := range notes {
			fmt.Fprintf(&review, "[note %s]\n%s\n---\n", note.ID, note.Body)
		}
	}
	return fmt.Sprintf(`# %s #%d - verdict relay

The review of change request #%d ended without a verdict. Your job is the record only: post the verdict note exactly as written below, or say the review does not decide.

Head under review: %s
Change request: #%d
Item: #%d

## The review, verbatim

%s
## Post the verdict note with

    %s

The note's second line, `+"`via: %s`"+`, is required: it marks the verdict as relayed by this seat, and the fleet stores the marker with the verdict.

## If the review does not decide

Prose that hedges, no notes at all, or a note about a different head is INCONCLUSIVE: answer this row `+"`denied`"+` or `+"`no-follow-on`"+` with one sentence naming what you read, and write nothing on the forge. The operator question a later tick raises is the escalation — that is by design.

## Forbidden here

Merging, closing, editing code, and writing on the queue other than answering this row are forbidden. The relay writes one note; the merge gate — not you — owns the merge.
`,
		project.Key, item, changeRequest, sha, changeRequest, item,
		review.String(),
		relayVerdictCommand(project, changeRequest, sha, seat),
		seat,
	)
}
