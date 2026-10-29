package act

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/store"
)

var verdictNote = regexp.MustCompile(`(?i)\b(ACCEPT|REJECT)\s+([0-9a-f]{7,40})\b`)

// CapturedVerdict is the verdict stored from a reviewer note.
type CapturedVerdict struct {
	VerdictID int64
	SHA       string
	Decision  string
	Source    string
	NoteID    string
}

// CaptureVerdict stores the newest note that names the current head. A note for
// another sha is ignored, so an older accept for the head survives a newer note
// about an older head. Only an accept or a reject is stored.
func (a *Actions) CaptureVerdict(ctx context.Context, dispatchID int64, item int, headSHA string, notes []forge.Comment) (*CapturedVerdict, error) {
	sorted := append([]forge.Comment(nil), notes...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].CreatedAt != sorted[j].CreatedAt {
			return sorted[i].CreatedAt > sorted[j].CreatedAt
		}
		return sorted[i].ID > sorted[j].ID
	})
	for _, note := range sorted {
		match := verdictNote.FindStringSubmatch(note.Body)
		if match == nil {
			continue
		}
		sha := strings.ToLower(match[2])
		if headSHA != "" && sha != headSHA {
			continue
		}
		decision := strings.ToLower(match[1])
		// A tick re-reads the same notes; do not add the same verdict twice.
		existing, err := a.Store.Verdicts(ctx, dispatchID)
		if err != nil {
			return nil, err
		}
		for _, verdict := range existing {
			if verdict.SHA == sha && verdict.Decision == decision {
				return nil, nil
			}
		}
		verdictID, err := a.Store.AddVerdict(ctx, store.NewVerdict{
			DispatchID: dispatchID, SHA: sha, Decision: decision,
			Source: "note " + note.ID, At: a.now(),
		})
		if err != nil {
			return nil, err
		}
		if _, err := a.event(ctx, "act", "verdict.capture", fmt.Sprintf("#%d", item),
			fmt.Sprintf("%s %s from note %s", decision, sha, note.ID)); err != nil {
			return nil, err
		}
		return &CapturedVerdict{VerdictID: verdictID, SHA: sha, Decision: decision, Source: "note " + note.ID, NoteID: note.ID}, nil
	}
	return nil, nil
}

// CaptureFromForge reads the item's notes and stores the verdict.
func (a *Actions) CaptureFromForge(ctx context.Context, dispatchID int64, item int, headSHA string) (*CapturedVerdict, error) {
	notes, err := a.Forge.Notes(ctx, item)
	if err != nil {
		return nil, err
	}
	return a.CaptureVerdict(ctx, dispatchID, item, headSHA, notes)
}
