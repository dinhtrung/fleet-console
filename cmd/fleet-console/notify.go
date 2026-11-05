// Progress notices on the phone.
//
// Everything here is a projection of state the store already holds, sent once
// per state (design.md §5). Three triggers, one client:
//
//   - after a merge, inside the applied tick — the only "code done" moment;
//   - `--notify-report`, on demand, which mirrors nothing;
//   - an optional digest, at most once per `notify_interval` and only when the
//     rendered state changed.
//
// Missing credentials are a configuration, not an error: one boot line, and
// every trigger becomes a no-op.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/dinhtrung/fleet-console/internal/act"
	"github.com/dinhtrung/fleet-console/internal/alert"
	"github.com/dinhtrung/fleet-console/internal/config"
	"github.com/dinhtrung/fleet-console/internal/decisions"
	"github.com/dinhtrung/fleet-console/internal/mirror"
	"github.com/dinhtrung/fleet-console/internal/progress"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// errNotifyNotConfigured is the --notify-report answer when the environment
// carries no credentials. It exits 3, distinct from a transport failure (1) and
// from a delivered message (0), so a cron wrapper can tell "not set up here"
// from "Telegram is down".
var errNotifyNotConfigured = errors.New("TELEGRAM_BOT_TOKEN/TELEGRAM_CHAT_ID not set; nothing to send")

const (
	envTelegramToken  = "TELEGRAM_BOT_TOKEN"
	envTelegramChatID = "TELEGRAM_CHAT_ID"
	// envTelegramAPIBase points the Bot API calls somewhere other than
	// api.telegram.org. A self-hosted Bot API server uses it, and so does the
	// test that proves --notify-report without a live token; unset means the
	// public API.
	envTelegramAPIBase = "TELEGRAM_API_BASE"
)

// notifier is the wired pair: one Telegram client, used for both kinds of
// message. Sharing it is deliberate (design.md §6) — a client that exists for
// progress but is never used for the decision it was written for is a lie in
// the docs.
type notifier struct {
	sender    alert.Telegram
	progress  *alert.Notifier
	escalator *alert.Escalator
	// notes posts a decision's question on its item when decision_notes is on.
	// The Forge is filled in per decision, from the project's read.
	notes         *decisions.Noter
	decisionNotes bool
	log           io.Writer
}

// configured reports whether a message could be sent at all.
func (n *notifier) configured() bool {
	return n != nil && n.progress != nil && n.progress.Configured()
}

// newNotifier builds the client from the environment. A missing token or chat
// id is not a failure: it prints the one boot line and returns a notifier whose
// configured() is false, so every trigger is a no-op.
func newNotifier(db *store.Store, eff *config.Effective, logw io.Writer, boardURL string) *notifier {
	sender := alert.Telegram{
		Token:   strings.TrimSpace(os.Getenv(envTelegramToken)),
		ChatID:  strings.TrimSpace(os.Getenv(envTelegramChatID)),
		BaseURL: strings.TrimSpace(os.Getenv(envTelegramAPIBase)),
	}
	n := &notifier{sender: sender, log: logw}
	if sender.Token == "" || sender.ChatID == "" {
		fmt.Fprintf(logw, "notify: %s/%s not set; completion notices are off\n", envTelegramToken, envTelegramChatID)
		return n
	}
	n.progress = &alert.Notifier{
		Store:  db,
		Sender: sender,
		Logf: func(format string, args ...any) {
			fmt.Fprintf(logw, "notify: %s\n", fmt.Sprintf(format, args...))
		},
	}
	n.escalator = &alert.Escalator{Store: db, Sender: sender, BoardURL: boardURL}
	// The decision note's ledger is the event table; the forge is resolved per
	// call, because the notifier is built before the projects are read.
	n.notes = &decisions.Noter{Store: &decisionStore{db: db}, Logf: func(format string, args ...any) {
		fmt.Fprintf(logw, "notify: %s\n", fmt.Sprintf(format, args...))
	}}
	n.decisionNotes = eff != nil && eff.DecisionNotes
	return n
}

// decisionStore adapts *store.Store to the noter's narrow ledger interface.
type decisionStore struct {
	db *store.Store
}

func (s *decisionStore) HasEvent(ctx context.Context, kind, subject, contains string) (bool, error) {
	return s.db.HasEvent(ctx, kind, subject, contains)
}

func (s *decisionStore) AppendEvent(ctx context.Context, e decisions.Event) (int64, error) {
	return s.db.AppendEvent(ctx, store.Event{
		Actor: e.Actor, Kind: e.Kind, Subject: e.Subject, Detail: e.Detail,
	})
}

// escalate puts one raised decision on the phone. A failure is logged and
// swallowed: the decision row is the durable record, and nothing in the tick may
// wait on Telegram (design.md §5).
// noteDecision posts the decision's question on its item when decision_notes is
// on. A nil noter means the feature is off and nothing happens (design D8). A
// failure is one log line: the tick must not fail because a comment did not land,
// and the escalation still goes out.
func (n *notifier) noteDecision(ctx context.Context, in act.DecisionRaised, note decisions.NoteContext, write decisions.Forge) {
	if n == nil || n.notes == nil || !n.decisionNotes {
		return
	}
	n.notes.Forge = write
	d := decisions.Decision{
		ID: in.QItemID, Question: in.Text, Summary: in.Text, Item: in.Item,
	}
	result, err := n.notes.Note(ctx, d, note)
	switch {
	case err != nil:
		fmt.Fprintf(n.log, "notify: decision.note %s: %v\n", in.QItemID, err)
	case result.Posted:
		fmt.Fprintf(n.log, "notify: decision.note %s posted on #%d\n", in.QItemID, in.Item)
	case result.Skipped && result.Reason != "already noted":
		fmt.Fprintf(n.log, "notify: decision.note %s skipped: %s\n", in.QItemID, result.Reason)
	}
}

// escalate puts one raised decision on the phone. The message carries the item
// link and the reply line the note prints, so the operator copies the line rather
// than composing it. A nil reply line means the app could not name the item, and
// the message omits the line instead of inventing one.
func (n *notifier) escalate(ctx context.Context, project string, in act.DecisionRaised, note decisions.NoteContext) {
	if n == nil || n.escalator == nil {
		return
	}
	result, err := n.escalator.Escalate(ctx, alert.EscalationInput{
		DecisionID: in.QItemID,
		Project:    project,
		Item:       in.Item,
		Question:   in.Text,
		ItemURL:    note.ItemURL,
		ReplyLine:  decisions.ReplyLine(decisions.Decision{ID: in.QItemID, Item: in.Item}, note),
	})
	if err != nil {
		fmt.Fprintf(n.log, "notify: decision %s: %v\n", in.QItemID, err)
		return
	}
	if result.Sent {
		fmt.Fprintf(n.log, "notify: decision %s sent as message %s\n", result.DecisionID, result.MessageID)
	}
}

// sendProgress renders one snapshot and sends it when its state is new, on an
// unsolicited trigger: the digest interval or a merge inside the applied tick.
//
// The caller decides *when* a trigger applies; the notifier decides whether this
// particular state has been reported already, and whether it is worth a message
// at all. A quiet board is not news: the skip happens before rendering and before
// Notify, so nothing is recorded as the last reported state — the dedupe keeps
// pointing at the last real message, and the next non-quiet snapshot is sent even
// when its body matches one from before the quiet stretch (design D3).
func (n *notifier) sendProgress(ctx context.Context, snapshot progress.Snapshot, now time.Time) (alert.ProgressResult, error) {
	if !n.configured() {
		return alert.ProgressResult{}, alert.ErrNotConfigured
	}
	if snapshot.Quiet() {
		fmt.Fprintf(n.log, "notify: skipped, no open work: nothing to report\n")
		return alert.ProgressResult{}, nil
	}
	return n.sendNow(ctx, snapshot, now)
}

// sendNow renders and sends unconditionally. It is the on-demand path behind
// `--notify-report`, which the spec exempts from the quiet rule: an explicit
// operator request is not an unsolicited trigger.
func (n *notifier) sendNow(ctx context.Context, snapshot progress.Snapshot, now time.Time) (alert.ProgressResult, error) {
	if !n.configured() {
		return alert.ProgressResult{}, alert.ErrNotConfigured
	}
	body := progress.Render(snapshot, now)
	return n.progress.Notify(ctx, body)
}

// reportSnapshot builds the report from the store and the roster alone: no
// mirror pass, no forge read. That is what `--notify-report` promises, and it is
// why the buckets come out as the store sees them rather than as the last tick
// left them.
func reportSnapshot(ctx context.Context, db *store.Store, eff *config.Effective, adapter *rig.Adapter, project store.ProjectRow) progress.Snapshot {
	rows, err := db.DispatchesForProject(ctx, project.ID)
	if err != nil {
		rows = nil
	}
	roster := progress.Roster{}
	if adapter != nil {
		seats, err := adapter.Seats(ctx)
		roster.Seats, roster.Err = seats, err
	}
	// An empty TickResult is the honest input: this command mirrors nothing, so
	// there is no forge read to project, and Build renders that as data.
	return progress.Build(mirror.TickResult{}, rows, project, eff, roster)
}

// digestGate answers whether the optional digest may send now.
//
// `notify_interval` is a floor between messages, not a heartbeat: with 0 it is
// off, and inside the interval nothing is sent even when the state changed —
// the change is not lost, it is reported by the next trigger outside it. The
// hash check lives inside the notifier, so a gate that opens on unchanged state
// still sends nothing.
type digestGate struct {
	interval time.Duration
	last     time.Time
}

// allow reports whether a send is permitted at now, and records it if so.
func (g *digestGate) allow(now time.Time) bool {
	if g.interval <= 0 {
		return false
	}
	if !g.last.IsZero() && now.Sub(g.last) < g.interval {
		return false
	}
	g.last = now
	return true
}

// exitCode is a run() sentinel: the message says why, main turns it into
// os.Exit. Exit 3 means "not configured here", which a cron wrapper must be
// able to tell from a transport failure (1) and from success (0).
type exitCode int

// Error lets run() hand the code back through its error channel without the
// caller having to know which flags produce which codes.
func (c exitCode) Error() string { return fmt.Sprintf("exit %d", int(c)) }

// runNotifyReport runs the on-demand report and answers the process exit code:
// 0 delivered (or already current), 1 transport failure, 3 not configured.
func runNotifyReport(ctx context.Context, args notifyReportArgs) (int, error) {
	db, err := store.Open(ctx, args.eff.Database)
	if err != nil {
		return 1, err
	}
	defer db.Close()

	n := args.notifier
	if n == nil {
		n = newNotifier(db, args.eff, args.log, args.boardURL)
	}
	if !n.configured() {
		fmt.Fprintf(args.log, "notify: %v\n", errNotifyNotConfigured)
		return 3, nil
	}
	project, err := db.ProjectByKey(ctx, args.projectKey)
	if err != nil {
		return 1, err
	}
	if project == nil {
		return 1, fmt.Errorf("notify: project %q is not registered", args.projectKey)
	}
	now := args.now
	if now == nil {
		now = time.Now
	}
	snapshot := reportSnapshot(ctx, db, args.eff, args.adapter, *project)
	// The on-demand report is exempt from the quiet rule: an explicit operator
	// request is not an unsolicited trigger (spec: "The on-demand report is
	// exempt from the quiet rule").
	result, err := n.sendNow(ctx, snapshot, now())
	if err != nil {
		return 1, err
	}
	switch {
	case result.Duplicate:
		fmt.Fprintf(args.log, "notify: unchanged since message %s; nothing sent\n", result.MessageID)
	case result.Failed:
		// A send that failed after its retry is a transport failure for this
		// command: the exit code is the whole point of the flag.
		fmt.Fprintf(args.log, "notify: send failed: %s\n", result.Reason)
		return 1, nil
	default:
		fmt.Fprintf(args.log, "notify: sent message %s\n", result.MessageID)
	}
	return 0, nil
}

type notifyReportArgs struct {
	eff        *config.Effective
	projectKey string
	notifier   *notifier
	adapter    *rig.Adapter
	log        io.Writer
	boardURL   string
	now        func() time.Time
}

// boardURLFrom is the deep link base for a decision message: the listen address
// is loopback by contract, so the link is for the operator sitting at the box.
func boardURLFrom(listen string) string {
	addr := strings.TrimSpace(listen)
	if addr == "" {
		return ""
	}
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return addr
	}
	return "http://" + addr
}
