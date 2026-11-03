package act

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/decide"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// sweepFixture is the tick with no merges and open epics whose sub-issues the
// fake forge can answer with states the tick's open-only snapshot cannot hold.
// It reuses the rollup fixture's store, project and stubs, so the sweep's tick
// tests run through the same RunTick path as the merge tests.
type sweepFixture struct {
	st      *store.Store
	project store.ProjectRow
	fake    *forge.Fake
	in      TickInput
}

// newSweepFixture builds a tick that merges nothing: no change requests, no
// verdicts, no dispatch intents. The snapshot holds the open epics and their open
// children only — the shape the forge's open list produces — while SubIssuesValue
// carries the closed children the sweep needs.
//
// epics lists the open epic numbers; children maps each epic to the children the
// read returns. A child with state closed is absent from the snapshot and present
// in the read, which is the whole point.
func newSweepFixture(t *testing.T, epics []int, children map[int][]forge.Item) *sweepFixture {
	t.Helper()
	ctx := context.Background()
	st := newStore(t)
	pid := newProject(t, st)

	// The snapshot: open epics and their open children, exactly what an
	// `issues?state=open` read yields. A closed child never appears here.
	var items []forge.Item
	for _, epicNumber := range epics {
		items = append(items, forge.Item{
			Number: epicNumber, Kind: forge.KindEpic, State: forge.StateOpen,
			Title: fmt.Sprintf("Epic %d", epicNumber),
		})
		for _, child := range children[epicNumber] {
			if strings.EqualFold(child.State, forge.StateOpen) {
				items = append(items, child)
			}
		}
	}

	fake := forge.NewFake(forge.KindGitHub)
	fake.ItemsValue = items
	fake.SubIssuesValue = children

	records := make([]store.ItemRecord, 0, len(items))
	for _, item := range items {
		records = append(records, store.ItemRecord{
			Number: item.Number, Kind: string(item.Kind), State: item.State,
			Title: item.Title, Parent: item.Parent, URL: "u", UpdatedAt: time.Now().UTC(),
		})
	}
	if err := st.ReplaceItems(ctx, pid, records); err != nil {
		t.Fatalf("items: %v", err)
	}

	project := store.ProjectRow{
		ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r",
		Rig: "rig", ConcurrencyCap: 2, Enabled: true,
	}
	return &sweepFixture{
		st: st, project: project, fake: fake,
		in: TickInput{
			Store: st, Project: project, Forge: fake, Rig: &queueStub{}, Git: &fakeGit{},
			Gate: openGate(), Apply: true, Items: items, CRs: nil,
			Plan: decide.Plan{}, WorktreeRoot: t.TempDir(),
		},
	}
}

// childOf builds a sub-issue row under an epic, in the state given.
func childOf(epic, number int, state string) forge.Item {
	parent := epic
	return forge.Item{
		Number: number, Kind: forge.KindTask, State: state,
		Title: fmt.Sprintf("Child %d", number), Parent: &parent,
	}
}

// epicWrites returns the writes the tick made against one number, in order.
func epicWrites(fake *forge.Fake, number int) []string {
	var ops []string
	for _, write := range fake.Writes {
		if write.Number == number {
			ops = append(ops, write.Op)
		}
	}
	return ops
}

// epicEvents returns the epic.closed events recorded for one epic, in order.
func epicEvents(t *testing.T, st *store.Store, epic int) []store.EventRow {
	t.Helper()
	events, err := st.RecentEvents(context.Background(), 200)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	var found []store.EventRow
	for _, event := range events {
		if event.Kind == "epic.closed" && event.Subject == fmt.Sprintf("#%d", epic) {
			found = append(found, event)
		}
	}
	return found
}

// TestSweepClosesACompletedEpicOnATickThatMergedNothing is #124's first
// acceptance: a tick with zero merges closes one completed epic and leaves an
// incomplete one alone. The completed epic's last child closed by hand, so there
// is no merge to hang the rollup on.
func TestSweepClosesACompletedEpicOnATickThatMergedNothing(t *testing.T) {
	f := newSweepFixture(t,
		[]int{64, 70},
		map[int][]forge.Item{
			// Every child closed, and closed children are absent from the snapshot.
			64: {childOf(64, 62, forge.StateClosed), childOf(64, 63, forge.StateClosed)},
			// One child still open: this epic must be left alone, quietly.
			70: {childOf(70, 68, forge.StateClosed), childOf(70, 69, forge.StateOpen)},
		},
	)

	report, err := RunTick(context.Background(), f.in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Merges) != 0 {
		t.Fatalf("merges = %d, want zero: this is the tick the sweep exists for", len(report.Merges))
	}

	// #64: comment then close, exactly once each.
	if ops := epicWrites(f.fake, 64); len(ops) != 2 || ops[0] != "comment" || ops[1] != "close" {
		t.Fatalf("writes to #64 = %v, want [comment close]", ops)
	}
	// #70: nothing at all.
	if ops := epicWrites(f.fake, 70); len(ops) != 0 {
		t.Fatalf("wrote to the incomplete epic #70: %v", ops)
	}
	// The report names the close and its child count.
	if len(report.Epics) != 1 || report.Epics[0].Epic != 64 || report.Epics[0].Children != 2 {
		t.Fatalf("report.Epics = %+v, want one close of #64 with 2 children", report.Epics)
	}
	// The event names the sweep as the source and carries the child count.
	events := epicEvents(t, f.st, 64)
	if len(events) != 1 {
		t.Fatalf("epic.closed events for #64 = %d, want 1", len(events))
	}
	if !strings.Contains(events[0].Detail, "sweep: 2 sub-issues") {
		t.Fatalf("event detail = %q, want it to name the sweep and the count", events[0].Detail)
	}
	if len(epicEvents(t, f.st, 70)) != 0 {
		t.Fatalf("an event was recorded for the incomplete epic #70")
	}
	// The comment opens by naming the sweep, so a reader knows a tick closed it.
	for _, write := range f.fake.Writes {
		if write.Op == "comment" && write.Number == 64 {
			if !strings.HasPrefix(write.Value, "Sweep:") {
				t.Fatalf("comment = %q, want it to open by naming the sweep", write.Value)
			}
			if !strings.Contains(write.Value, "2 sub-issues") {
				t.Fatalf("comment = %q, want the child count", write.Value)
			}
		}
	}
	// The open-child epic produced no skip line: its skip is quiet.
	for _, line := range report.Skipped {
		if strings.Contains(line, "#70") {
			t.Fatalf("an incomplete epic produced a skip line: %q", line)
		}
	}
}

// TestSweepLeavesAnEpicWhoseReadIsEmpty is the zero-children guard against the
// sweep's own read: an empty read is indistinguishable from "all closed", and
// closing over it would close an epic over work nobody looked at. This one is
// journaled, because silence would hide a short read.
func TestSweepLeavesAnEpicWhoseReadIsEmpty(t *testing.T) {
	f := newSweepFixture(t, []int{64}, map[int][]forge.Item{64: nil})

	report, err := RunTick(context.Background(), f.in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if ops := epicWrites(f.fake, 64); len(ops) != 0 {
		t.Fatalf("closed an epic whose read returned nothing: %v", ops)
	}
	if len(report.Epics) != 0 {
		t.Fatalf("report.Epics = %+v, want none", report.Epics)
	}
	want := "#64 sweep: no sub-issues in the read"
	var found bool
	for _, line := range report.Skipped {
		if line == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("skipped = %v, want %q", report.Skipped, want)
	}
}

// TestSweepCommentErrorLeavesTheEpicOpenAndRetries is the failure path: a failing
// comment becomes a skip line, the tick still succeeds, and the epic stays open so
// the next tick tries again without any dedupe record.
func TestSweepCommentErrorLeavesTheEpicOpenAndRetries(t *testing.T) {
	f := newSweepFixture(t, []int{64}, map[int][]forge.Item{
		64: {childOf(64, 62, forge.StateClosed), childOf(64, 63, forge.StateClosed)},
	})
	commentErr := errors.New("comment refused")
	f.fake.WriteErr = commentErr

	report, err := RunTick(context.Background(), f.in)
	if err != nil {
		t.Fatalf("tick failed on a failed sweep comment, want success: %v", err)
	}
	if len(report.Epics) != 0 {
		t.Fatalf("report.Epics = %+v, want none when the comment failed", report.Epics)
	}
	var skipFound bool
	for _, line := range report.Skipped {
		if strings.Contains(line, "#64 sweep: comment:") && strings.Contains(line, "comment refused") {
			skipFound = true
		}
	}
	if !skipFound {
		t.Fatalf("skipped = %v, want a #64 sweep comment line naming the error", report.Skipped)
	}
	// The close must not have run after the comment failed.
	if ops := epicWrites(f.fake, 64); len(ops) != 0 {
		t.Fatalf("writes to #64 = %v, want none: the comment failed first", ops)
	}
	if len(epicEvents(t, f.st, 64)) != 0 {
		t.Fatalf("an epic.closed event was recorded for a close that never happened")
	}

	// Next tick: the error clears and the same conditions close it. The retry is
	// "the epic is still open", so nothing else has to be reset.
	f.fake.WriteErr = nil
	retry, err := RunTick(context.Background(), f.in)
	if err != nil {
		t.Fatalf("retry tick: %v", err)
	}
	if len(retry.Epics) != 1 || retry.Epics[0].Epic != 64 {
		t.Fatalf("retry closed nothing: report.Epics = %+v, skipped = %v", retry.Epics, retry.Skipped)
	}
	if ops := epicWrites(f.fake, 64); len(ops) != 2 || ops[0] != "comment" || ops[1] != "close" {
		t.Fatalf("retry writes to #64 = %v, want [comment close]", ops)
	}
}

// TestSweepClosesTwoCompletedEpicsInOneTick is the fourth acceptance: two
// completed epics produce two closes and two events in a single tick.
func TestSweepClosesTwoCompletedEpicsInOneTick(t *testing.T) {
	f := newSweepFixture(t, []int{64, 70}, map[int][]forge.Item{
		64: {childOf(64, 62, forge.StateClosed)},
		70: {childOf(70, 68, forge.StateClosed), childOf(70, 69, forge.StateClosed)},
	})

	report, err := RunTick(context.Background(), f.in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Epics) != 2 {
		t.Fatalf("report.Epics = %+v, want two closes", report.Epics)
	}
	closes := 0
	for _, write := range f.fake.Writes {
		if write.Op == "close" {
			closes++
		}
	}
	if closes != 2 {
		t.Fatalf("closes = %d, want 2 (%+v)", closes, f.fake.Writes)
	}
	for _, epic := range []int{64, 70} {
		events := epicEvents(t, f.st, epic)
		if len(events) != 1 {
			t.Fatalf("epic.closed events for #%d = %d, want 1", epic, len(events))
		}
		if !strings.Contains(events[0].Detail, "sweep:") {
			t.Fatalf("event detail for #%d = %q, want the sweep named", epic, events[0].Detail)
		}
	}
}

// TestSweepBoundsOneReadPerOpenEpicPerTick proves task 3.5's bound and its
// visibility: one sub-issue read per open epic, counted on the report.
func TestSweepBoundsOneReadPerOpenEpicPerTick(t *testing.T) {
	f := newSweepFixture(t, []int{64, 70, 80}, map[int][]forge.Item{
		64: {childOf(64, 62, forge.StateClosed)},
		70: {childOf(70, 68, forge.StateOpen)},
		80: {childOf(80, 78, forge.StateClosed)},
	})

	report, err := RunTick(context.Background(), f.in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(f.fake.SubIssuesAsked) != 3 {
		t.Fatalf("SubIssues reads = %v, want exactly one per open epic (3)", f.fake.SubIssuesAsked)
	}
	if report.SweepReads != 3 {
		t.Fatalf("report.SweepReads = %d, want 3 so the cost is visible", report.SweepReads)
	}
	// No epic is read twice, so the count is bounded by the number of open epics.
	seen := map[int]int{}
	for _, number := range f.fake.SubIssuesAsked {
		seen[number]++
	}
	for number, count := range seen {
		if count != 1 {
			t.Fatalf("#%d was read %d times, want 1", number, count)
		}
	}
}

// TestSweepSkipsAnEpicTheRollupAlreadyClosed proves the shared `closed` set: an
// epic the merge path closed in this tick is not read or closed again by the
// sweep, so a tick that merges the last child writes one comment and one close.
func TestSweepSkipsAnEpicTheRollupAlreadyClosed(t *testing.T) {
	f := newRollupFixture(t, forge.StateOpen, [2]string{forge.StateClosed, forge.StateClosed})
	// The sweep's read would also say close; the merge path got there first.
	f.fake.SubIssuesValue = map[int][]forge.Item{
		64: {childOf(64, 62, forge.StateClosed), childOf(64, 63, forge.StateClosed)},
	}

	report, err := RunTick(context.Background(), f.in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(report.Merges) != 2 {
		t.Fatalf("merges = %d (%v), want two so the rollup runs first", len(report.Merges), report.Skipped)
	}
	closes := 0
	for _, write := range f.fake.Writes {
		if write.Number == 64 && write.Op == "close" {
			closes++
		}
	}
	if closes != 1 {
		t.Fatalf("closes of #64 = %d, want exactly 1 across rollup and sweep (%+v)", closes, f.fake.Writes)
	}
	if len(report.Epics) != 1 {
		t.Fatalf("report.Epics = %+v, want one close", report.Epics)
	}
	if len(f.fake.SubIssuesAsked) != 0 {
		t.Fatalf("the sweep read #64 after the rollup closed it: %v", f.fake.SubIssuesAsked)
	}
}

// TestSweepReadErrorLeavesTheEpicOpen is the read failure path: ErrStale means the
// forge could not be read reliably, and a sweep that treated it as "no open
// children" would close epics over work nobody looked at.
func TestSweepReadErrorLeavesTheEpicOpen(t *testing.T) {
	f := newSweepFixture(t, []int{64}, map[int][]forge.Item{
		64: {childOf(64, 62, forge.StateClosed)},
	})
	f.fake.SubIssuesErr = forge.ErrStale

	report, err := RunTick(context.Background(), f.in)
	if err != nil {
		t.Fatalf("tick failed on a failed sweep read, want success: %v", err)
	}
	if ops := epicWrites(f.fake, 64); len(ops) != 0 {
		t.Fatalf("wrote to #64 on a failed read: %v", ops)
	}
	// The line must name the error, not just the epic. Asserting only the `#64 sweep:`
	// prefix would let a `children = nil` fall-through pass: both branches print a
	// line, but one says "the read did not come back" and the other blames the
	// zero-children guard, "the read came back short". That is the opposite
	// diagnosis, and it is the confusion sweep_tick.go's own comment warns against.
	var found bool
	for _, line := range report.Skipped {
		if strings.Contains(line, "#64 sweep:") && strings.Contains(line, "forge read is stale") {
			found = true
		}
	}
	if !found {
		t.Fatalf("skipped = %v, want a #64 sweep line naming the read error %q", report.Skipped, "forge read is stale")
	}
	// And the zero-children wording must not appear for a failed read.
	for _, line := range report.Skipped {
		if strings.Contains(line, "no sub-issues in the read") {
			t.Fatalf("a failed read was reported as the zero-children guard: %q", line)
		}
	}
}

// TestSweepCloseErrorLeavesTheEpicOpen is the other half of the failure path the
// brief names: "a failing comment or close". The close error leaves the epic open
// and the tick succeeding, so the next tick retries. The comment has already been
// posted at that point, which is harmless — the epic is still open, so the retry
// posts again and the operator sees one close.
func TestSweepCloseErrorLeavesTheEpicOpen(t *testing.T) {
	f := newSweepFixture(t, []int{64}, map[int][]forge.Item{
		64: {childOf(64, 62, forge.StateClosed), childOf(64, 63, forge.StateClosed)},
	})
	f.fake.CloseErr = errors.New("close refused")

	report, err := RunTick(context.Background(), f.in)
	if err != nil {
		t.Fatalf("tick failed on a failed sweep close, want success: %v", err)
	}
	if len(report.Epics) != 0 {
		t.Fatalf("report.Epics = %+v, want none when the close failed", report.Epics)
	}
	var skipFound bool
	for _, line := range report.Skipped {
		if strings.Contains(line, "#64 sweep: close:") && strings.Contains(line, "close refused") {
			skipFound = true
		}
	}
	if !skipFound {
		t.Fatalf("skipped = %v, want a #64 sweep close line naming the error", report.Skipped)
	}
	// The close is the write that failed: it is absent from the recorded writes,
	// while the comment before it was posted. No epic.closed event may exist.
	if ops := epicWrites(f.fake, 64); len(ops) != 1 || ops[0] != "comment" {
		t.Fatalf("writes to #64 = %v, want [comment] only", ops)
	}
	if len(epicEvents(t, f.st, 64)) != 0 {
		t.Fatalf("an epic.closed event was recorded for a close that never happened")
	}

	// Next tick, the close succeeds and the epic closes. The comment is NOT posted
	// again: the first tick's comment is on the epic, so the retry skips it and the
	// epic carries exactly one sweep comment no matter how long the close was broken.
	f.fake.CloseErr = nil
	retry, err := RunTick(context.Background(), f.in)
	if err != nil {
		t.Fatalf("retry tick: %v", err)
	}
	if len(retry.Epics) != 1 || retry.Epics[0].Epic != 64 {
		t.Fatalf("retry closed nothing: report.Epics = %+v, skipped = %v", retry.Epics, retry.Skipped)
	}
	if ops := epicWrites(f.fake, 64); len(ops) != 2 || ops[0] != "comment" || ops[1] != "close" {
		t.Fatalf("writes to #64 after retry = %v, want [comment close]: the retry must not post a second comment", ops)
	}
}

// TestSweepDoesNotAccumulateCommentsWhileTheCloseKeepsFailing is finding 3 from
// the #128 review. The comment posts before the close, so a close that keeps
// failing would otherwise post one comment per tick, growing without bound. Three
// failing ticks must leave exactly one comment; the third tick's success adds the
// close and nothing else.
func TestSweepDoesNotAccumulateCommentsWhileTheCloseKeepsFailing(t *testing.T) {
	f := newSweepFixture(t, []int{64}, map[int][]forge.Item{
		64: {childOf(64, 62, forge.StateClosed), childOf(64, 63, forge.StateClosed)},
	})
	f.fake.CloseErr = errors.New("close refused")

	comments := func() int {
		count := 0
		for _, write := range f.fake.Writes {
			if write.Number == 64 && write.Op == "comment" {
				count++
			}
		}
		return count
	}

	for tick := 1; tick <= 3; tick++ {
		if _, err := RunTick(context.Background(), f.in); err != nil {
			t.Fatalf("tick %d: %v", tick, err)
		}
		if got := comments(); got != 1 {
			t.Fatalf("after %d failing close ticks, comments on #64 = %d, want exactly 1", tick, got)
		}
	}
	if len(epicEvents(t, f.st, 64)) != 0 {
		t.Fatalf("an epic.closed event was recorded while the close was failing")
	}

	// The close recovers: the epic closes and still carries one comment.
	f.fake.CloseErr = nil
	report, err := RunTick(context.Background(), f.in)
	if err != nil {
		t.Fatalf("recovery tick: %v", err)
	}
	if len(report.Epics) != 1 || report.Epics[0].Epic != 64 {
		t.Fatalf("recovery tick closed nothing: %+v", report.Epics)
	}
	if got := comments(); got != 1 {
		t.Fatalf("comments on #64 = %d after recovery, want exactly 1", got)
	}
	if len(epicEvents(t, f.st, 64)) != 1 {
		t.Fatalf("epic.closed events for #64 = %d, want 1", len(epicEvents(t, f.st, 64)))
	}
}

// notesFailingFake fails only the Notes read. forge.Fake.ReadErr fails Items and
// SubIssues too, so a tick built on ReadErr never reaches the sweep's notes read —
// it stops at the sub-issue read first. Overriding one method is what puts the
// failure at the arm sweep_tick.go:67 guards, with the sub-issue read succeeding.
type notesFailingFake struct {
	*forge.Fake
	err error
}

func (f *notesFailingFake) Notes(ctx context.Context, number int) ([]forge.Comment, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.Fake.Notes(ctx, number)
}

// TestSweepNotesReadErrorLeavesTheEpicOpen covers the one branch of the sweep's
// comment dedupe no test reached: the notes read failing. The branch is
// load-bearing. Reading "the notes read failed" as "no sweep comment yet" posts
// another comment on every tick the close stays broken — the 1-2-3 regrowth the
// comment bound exists to prevent — and the suite stayed green without this test.
//
// The close is broken for the whole failure window so a swallowed error would show
// up as a second and third comment rather than as an early close. The epic must
// stay open, unwritten and uncommented; when both recover, exactly one comment and
// one close land.
func TestSweepNotesReadErrorLeavesTheEpicOpen(t *testing.T) {
	f := newSweepFixture(t, []int{64}, map[int][]forge.Item{
		64: {childOf(64, 62, forge.StateClosed), childOf(64, 63, forge.StateClosed)},
	})
	notesErr := errors.New("notes read refused")
	closeErr := errors.New("close refused")
	fake := &notesFailingFake{Fake: f.fake, err: notesErr}
	f.fake.CloseErr = closeErr
	f.in.Forge = fake

	comments := func() int {
		count := 0
		for _, write := range f.fake.Writes {
			if write.Number == 64 && write.Op == "comment" {
				count++
			}
		}
		return count
	}

	for tick := 1; tick <= 3; tick++ {
		report, err := RunTick(context.Background(), f.in)
		if err != nil {
			t.Fatalf("tick %d failed on a failed notes read, want success: %v", tick, err)
		}
		if len(report.Epics) != 0 {
			t.Fatalf("tick %d: report.Epics = %+v, want none when the notes read failed", tick, report.Epics)
		}
		// The write count is asserted before the wording: the swallowed error writes a
		// comment and blames the close, so "nothing was written" is the diagnosis that
		// names what actually went wrong.
		if got := comments(); got != 0 {
			t.Fatalf("tick %d: comments on #64 = %d on a failed notes read, want 0", tick, got)
		}
		if ops := epicWrites(f.fake, 64); len(ops) != 0 {
			t.Fatalf("tick %d: writes to #64 = %v on a failed notes read, want none", tick, ops)
		}
		var found bool
		for _, line := range report.Skipped {
			if strings.Contains(line, "#64 sweep: read notes:") && strings.Contains(line, "notes read refused") {
				found = true
			}
		}
		if !found {
			t.Fatalf("tick %d: skipped = %v, want a #64 sweep read notes line naming the error %q", tick, report.Skipped, "notes read refused")
		}
	}
	if len(epicEvents(t, f.st, 64)) != 0 {
		t.Fatalf("an epic.closed event was recorded while the notes read was failing")
	}

	// Both recover: one comment, one close, one event. The dedupe starts clean
	// because the failed read never posted a comment to find.
	fake.err = nil
	f.fake.CloseErr = nil
	report, err := RunTick(context.Background(), f.in)
	if err != nil {
		t.Fatalf("recovery tick: %v", err)
	}
	if len(report.Epics) != 1 || report.Epics[0].Epic != 64 {
		t.Fatalf("recovery tick closed nothing: %+v, skipped = %v", report.Epics, report.Skipped)
	}
	if got := comments(); got != 1 {
		t.Fatalf("comments on #64 = %d after recovery, want exactly 1", got)
	}
	if ops := epicWrites(f.fake, 64); len(ops) != 2 || ops[0] != "comment" || ops[1] != "close" {
		t.Fatalf("recovery writes to #64 = %v, want [comment close]", ops)
	}
	if len(epicEvents(t, f.st, 64)) != 1 {
		t.Fatalf("epic.closed events for #64 = %d, want 1", len(epicEvents(t, f.st, 64)))
	}
}
