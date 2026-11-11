package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/act"
)

// TestBlockedMergePagesOncePerKey covers #252: a blocked merge pages through
// the quiet rule — an operator obligation is not idleness — exactly once per
// (item, reason, head sha). The same stall on the next tick is silent; a new
// head is a new key and pages again.
func TestBlockedMergePagesOncePerKey(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

	eff := notifyFixture(t)
	bot := newFakeBot(t)
	var logw bytes.Buffer
	n := notifierFor(t, eff, bot, &logw)

	// The board is quiet — no epic, no decision, nothing in flight. That is
	// exactly the state that hid the stall before: the quiet gate swallows the
	// progress path, and the blocked merge must page anyway.
	snapshot := quietSnapshotForTest(t, eff)
	if !snapshot.Quiet() {
		t.Fatalf("fixture is not quiet: %+v", snapshot.Counts)
	}
	if _, err := n.sendProgress(ctx, snapshot, at); err != nil {
		t.Fatalf("sendProgress = %v", err)
	}
	if got := bot.count(); got != 0 {
		t.Fatalf("progress sends on a quiet board = %d, want 0", got)
	}

	conflict := act.BlockedMerge{
		Item: 433, CR: 660, SHA: "abc1234",
		Reason: "change request #660 is not mergeable",
		Action: "rebase onto the default branch and re-accept",
	}

	// First tick at this head: one message, naming the item, the change
	// request, the reason and the action expected.
	n.sendBlocked(ctx, "tsim", conflict)
	if got := bot.count(); got != 1 {
		t.Fatalf("sends = %d, want 1", got)
	}
	if !strings.Contains(bot.sent[0], "#433") || !strings.Contains(bot.sent[0], "#660") ||
		!strings.Contains(bot.sent[0], "not mergeable") || !strings.Contains(bot.sent[0], "rebase") {
		t.Fatalf("message = %q, want item, CR, reason and action", bot.sent[0])
	}

	// The same stall on the next tick: silent.
	n.sendBlocked(ctx, "tsim", conflict)
	if got := bot.count(); got != 1 {
		t.Fatalf("sends after the repeat = %d, want still 1", got)
	}

	// A new head is a new key: it pages again.
	rebased := conflict
	rebased.SHA = "def5678"
	n.sendBlocked(ctx, "tsim", rebased)
	if got := bot.count(); got != 2 {
		t.Fatalf("sends after the rebase = %d, want 2", got)
	}
	if !strings.Contains(bot.sent[1], "def5678") {
		t.Fatalf("second message = %q, want the new head", bot.sent[1])
	}

	// The rate limit is per (item, reason, head sha): a different item with the
	// same reason and sha is its own page.
	other := conflict
	other.Item = 434
	n.sendBlocked(ctx, "tsim", other)
	if got := bot.count(); got != 3 {
		t.Fatalf("sends for the second item = %d, want 3", got)
	}

	// A different project with the same item number is its own page too.
	n.sendBlocked(ctx, "other", conflict)
	if got := bot.count(); got != 4 {
		t.Fatalf("sends for the other project = %d, want 4", got)
	}
}

// TestBlockedMergeUnconfiguredIsSilent: without credentials the page is a
// no-op, one boot line, and nothing errors (design.md §5).
func TestBlockedMergeUnconfiguredIsSilent(t *testing.T) {
	eff := notifyFixture(t)
	bot := newFakeBot(t)
	var logw bytes.Buffer
	t.Setenv(envTelegramToken, "")
	t.Setenv(envTelegramChatID, "")
	n := newNotifierForTest(t, eff, &logw, bot.owner.URL)

	n.sendBlocked(context.Background(), "tsim", act.BlockedMerge{
		Item: 433, CR: 660, SHA: "abc1234",
		Reason: "change request #660 is not mergeable",
		Action: "rebase onto the default branch and re-accept",
	})
	if got := bot.count(); got != 0 {
		t.Fatalf("sends = %d, want none", got)
	}
}
