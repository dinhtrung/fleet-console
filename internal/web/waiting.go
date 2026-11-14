package web

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// WaitingSeat is one seat on a project's rigs that has stopped on a human
// (#429). The board shows the wait and links to the UI where a person answers
// it; it never answers for them, so there is no control here and no verb behind
// one (design D5).
type WaitingSeat struct {
	Seat     string `json:"seat"`
	Rig      string `json:"rig"`
	Kind     string `json:"kind"`
	Question string `json:"question"`
	// Asking says the rig reported no question text. The seat is still listed:
	// a silent wait is a wait, and hiding it would make an unreadable prompt
	// look like an idle fleet.
	Asking bool `json:"asking"`
	// Since is how long the app has seen this question, "" when it cannot say
	// (a question first observed without a raising event on the ledger). It is a
	// duration the way every age on the board is one, not a timestamp.
	Since string `json:"since,omitempty"`
	// SinceAt is the instant the wait was first seen, RFC3339, "" with Since.
	SinceAt string `json:"since_at,omitempty"`
}

// WaitingPanel is the project's "waiting on a human" section.
type WaitingPanel struct {
	// Known is false when the rig read failed: an unread rig must never render
	// as "nobody is waiting" (the spec's third scenario). Reason carries the
	// read's own words so the surface can say why it does not know.
	Known  bool   `json:"known"`
	Reason string `json:"reason,omitempty"`
	// UIURL is where the OpenRig UI answers, "" when the daemon did not say.
	// It is one link for the whole panel: the panel's job is to point at the
	// place a person answers the prompt, not to deep-link a route the rig does
	// not publish.
	UIURL string        `json:"ui_url,omitempty"`
	Seats []WaitingSeat `json:"seats"`
}

// waitingPanel builds one project's waiting section from facts the app already
// read:
//
//   - the seats are the mirror's last successful `rig_seats` snapshot, so an
//     /api/projects request makes no rig call of its own (design D7: the list
//     renders on every visit, and a per-project CLI read would put the board's
//     own traffic on the daemon);
//   - the read's health is the status block's rig_stale flag and the snapshot's
//     presence — a missing snapshot is a rig the app has never read, which is
//     unknown, not clear;
//   - a seat counts as waiting when its lifecycle is `attention_required`, or
//     when its node reports any attention block. The lifecycle is the minimum
//     fact every rig reports (#425's decode is additive, so an older rig may
//     send no blocks at all) and the blocks are the detail: a lifecycle-only
//     seat is listed saying the rig gave no question. Reading only the blocks
//     dropped such a seat and rendered "nobody is waiting" over a stopped fleet;
//   - one obligation is one row: #427's fingerprint is seat + question, so two
//     blocks with the same text on one seat are the same wait, not two;
//   - the age comes from the ledger's own raise (`attention.raised`, #427) when
//     it is there, so a question that has stood since before the last read is
//     not reported as new. A standing raise is the newest raise newer than the
//     newest resolution for the same seat; with no ledger answer the age falls
//     back to the snapshot's stamp, which is the earliest instant this app can
//     honestly claim to have seen it;
//   - the link is the daemon's own UI address, passed in by the caller so one
//     /api/projects request asks `rig status` once however many projects wait.
func waitingPanel(ctx context.Context, st *store.Store, projectID int64, uiURL string, now time.Time) WaitingPanel {
	panel := WaitingPanel{Seats: []WaitingSeat{}, UIURL: uiURL}
	status, err := st.MirrorStatus(ctx, projectID)
	if err != nil {
		panel.UIURL = ""
		panel.Reason = err.Error()
		return panel
	}
	payload, snapshotAt, err := st.GetSnapshot(ctx, projectID, "rig_seats")
	if err != nil {
		panel.UIURL = ""
		panel.Reason = err.Error()
		return panel
	}
	if status.RigStale {
		panel.UIURL = ""
		panel.Reason = strings.TrimSpace(status.RigError)
		if panel.Reason == "" {
			panel.Reason = "the rig read failed"
		}
		return panel
	}
	if len(payload) == 0 {
		panel.UIURL = ""
		panel.Reason = "the rig has not been read yet"
		return panel
	}
	var seats []rig.Seat
	if err := json.Unmarshal(payload, &seats); err != nil {
		panel.UIURL = ""
		panel.Reason = err.Error()
		return panel
	}
	seenAt := snapshotAt
	panel.Known = true
	// One obligation is one row: #427 fingerprints a wait by seat + question, so
	// the kind is detail on the same wait rather than a second obligation.
	emitted := make(map[string]bool)
	for _, seat := range seats {
		if !seat.WaitingOnHuman() {
			continue
		}
		blocks := seat.Attention
		if len(blocks) == 0 {
			// The lifecycle is the only fact this rig reports: a real wait with
			// no text to show.
			blocks = []rig.SeatAttention{{Kind: rig.AttentionKindAttention}}
		}
		for _, attention := range blocks {
			entry := WaitingSeat{
				Seat: attention.Seat, Rig: attention.Rig, Kind: attention.Kind,
				Question: strings.TrimSpace(attention.Question),
			}
			if entry.Seat == "" {
				entry.Seat = seat.Session
			}
			if entry.Rig == "" {
				entry.Rig = seat.Rig
			}
			entry.Asking = entry.Question == ""
			fingerprint := entry.Seat + "\x00" + entry.Question
			if emitted[fingerprint] {
				continue
			}
			emitted[fingerprint] = true
			since, ok := standingSince(ctx, st, entry.Seat)
			if !ok {
				since = seenAt
			}
			if !since.IsZero() {
				entry.Since = ageBetween(now, since)
				entry.SinceAt = since.UTC().Format(time.RFC3339)
			}
			panel.Seats = append(panel.Seats, entry)
		}
	}
	if len(panel.Seats) == 0 {
		panel.UIURL = ""
	}
	return panel
}

// standingSince answers when the ledger first saw this seat's current question
// (#427's `attention.raised`, newest raise newer than the newest resolution).
// ok is false when the ledger holds no such raise — the caller then falls back
// to its own observation instead of inventing a history.
//
// The subject filter is a prefix match in SQL, so the scan keeps looking past a
// different seat whose name starts the same way (`dev-builder-1@rig` and
// `dev-builder-10@rig`): taking only the first row would drop a real age on a
// collision and report the read's stamp instead.
func standingSince(ctx context.Context, st *store.Store, seat string) (time.Time, bool) {
	const scan = 20
	raised, err := st.Events(ctx, store.EventFilter{Kind: "attention.raised", SubjectPrefix: seat, Limit: scan})
	if err != nil {
		return time.Time{}, false
	}
	var newest *store.EventRow
	for index := range raised.Events {
		if raised.Events[index].Subject == seat {
			newest = &raised.Events[index]
			break
		}
	}
	if newest == nil {
		return time.Time{}, false
	}
	resolved, err := st.Events(ctx, store.EventFilter{Kind: "attention.resolved", SubjectPrefix: seat, Limit: scan})
	if err == nil {
		for index := range resolved.Events {
			if resolved.Events[index].Subject == seat && resolved.Events[index].ID > newest.ID {
				return time.Time{}, false
			}
		}
	}
	at, err := time.Parse(time.RFC3339Nano, newest.At)
	if err != nil {
		return time.Time{}, false
	}
	return at, true
}

// waitingUIURL answers the daemon's UI address, reading `rig status` only when
// the caller does not already have one. The caller keeps the answer for the
// rest of the request, so N waiting projects cost one CLI read, not N.
func waitingUIURL(ctx context.Context, rigs *rig.Adapter, known string) string {
	if known != "" || rigs == nil {
		return known
	}
	report, err := rigs.Status(ctx)
	if err != nil {
		return ""
	}
	return report.UIURL()
}
