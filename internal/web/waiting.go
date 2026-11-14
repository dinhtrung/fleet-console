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
//   - the age comes from the ledger's own raise (`attention.raised`, #427) when
//     it is there, so a question that has stood since before the last read is
//     not reported as new. A standing raise is the newest raise newer than the
//     newest resolution for the same seat; with no ledger answer the age falls
//     back to the snapshot's stamp, which is the earliest instant this app can
//     honestly claim to have seen it.
//   - the link is the daemon's own UI address, read once per panel and only
//     when something is waiting.
func waitingPanel(ctx context.Context, st *store.Store, projectID int64, rigs *rig.Adapter, now time.Time) WaitingPanel {
	panel := WaitingPanel{Seats: []WaitingSeat{}}
	status, err := st.MirrorStatus(ctx, projectID)
	if err != nil {
		panel.Reason = err.Error()
		return panel
	}
	payload, snapshotAt, err := st.GetSnapshot(ctx, projectID, "rig_seats")
	if err != nil {
		panel.Reason = err.Error()
		return panel
	}
	if status.RigStale {
		panel.Reason = strings.TrimSpace(status.RigError)
		if panel.Reason == "" {
			panel.Reason = "the rig read failed"
		}
		return panel
	}
	if len(payload) == 0 {
		panel.Reason = "the rig has not been read yet"
		return panel
	}
	var seats []rig.Seat
	if err := json.Unmarshal(payload, &seats); err != nil {
		panel.Reason = err.Error()
		return panel
	}
	seenAt := snapshotAt
	panel.Known = true
	for _, seat := range seats {
		for _, attention := range seat.Attention {
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
	if len(panel.Seats) > 0 && rigs != nil {
		if report, err := rigs.Status(ctx); err == nil {
			panel.UIURL = report.UIURL()
		}
	}
	return panel
}

// standingSince answers when the ledger first saw this seat's current question
// (#427's `attention.raised`, newest raise newer than the newest resolution).
// ok is false when the ledger holds no such raise — the caller then falls back
// to its own observation instead of inventing a history.
func standingSince(ctx context.Context, st *store.Store, seat string) (time.Time, bool) {
	raised, err := st.Events(ctx, store.EventFilter{Kind: "attention.raised", SubjectPrefix: seat, Limit: 1})
	if err != nil || len(raised.Events) == 0 {
		return time.Time{}, false
	}
	newest := raised.Events[0]
	// The subject is a prefix match; keep the exact seat only, so a raise for
	// dev-builder-1@rig cannot age dev-builder-10@rig.
	if newest.Subject != seat {
		return time.Time{}, false
	}
	resolved, err := st.Events(ctx, store.EventFilter{Kind: "attention.resolved", SubjectPrefix: seat, Limit: 1})
	if err == nil && len(resolved.Events) > 0 && resolved.Events[0].ID > newest.ID {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, newest.At)
	if err != nil {
		return time.Time{}, false
	}
	return at, true
}
