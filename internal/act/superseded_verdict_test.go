package act

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// supersededFixture is one project, one open change request at `head`, and one
// reviewer note. The git seam answers ancestry from `ancestors`, so a test says
// which commits the repository would call ancestors of the head — the check the
// change adds is the repository's, never a string comparison (D1).
type supersededFixture struct {
	ctx    context.Context
	st     *store.Store
	queue  *queueStub
	git    *fakeGit
	fake   *forge.Fake
	in     TickInput
	item   forge.Item
	change forge.ChangeRequest
}

func newSupersededFixture(t *testing.T, note string, ancestors map[string]bool, ancestorErr error) *supersededFixture {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	st := newStore(t)
	pid := newProject(t, st)
	item := forge.Item{Number: 5, Kind: forge.KindTask, State: forge.StateOpen, Title: "Five"}
	change := forge.ChangeRequest{
		Number: 20, HeadSHA: supersededHead, Branch: "feature/5-five", Mergeable: true,
		Body: "Closes #5", DocsOnly: true, State: "open",
	}
	fake := forge.NewFake(forge.KindGitHub)
	fake.ItemsValue = []forge.Item{item}
	fake.RequestsValue = []forge.ChangeRequest{change}
	if note != "" {
		fake.NotesValue = []forge.Comment{{ID: "n1", Body: note, CreatedAt: "2026-11-13T11:00:00Z"}}
	}
	git := &fakeGit{ancestors: ancestors, ancestorErr: ancestorErr}
	queue := &queueStub{created: &rig.CreateRequest{}}
	project := store.ProjectRow{
		ID: pid, Key: "app", ForgeKind: "github", ForgeProject: "o/r",
		Rig: "rig", ConcurrencyCap: 2, Enabled: true,
	}
	return &supersededFixture{
		ctx: ctx, st: st, queue: queue, git: git, fake: fake, item: item, change: change,
		in: TickInput{
			Store: st, Project: project, Forge: fake, Rig: queue, Git: git,
			Gate: openGate(), Apply: true, Items: fake.ItemsValue, CRs: fake.RequestsValue,
			Now: func() time.Time { return now }, WorktreeRoot: t.TempDir(),
		},
	}
}

const supersededHead = "aaaa1111111111111111111111111111111a"
const ancestorSHA = "bbbb2222222222222222222222222222222b"
const foreignSHA = "cccc3333333333333333333333333333333c"

func (f *supersededFixture) tick(t *testing.T) TickReport {
	t.Helper()
	report, err := RunTick(f.ctx, f.in)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	return report
}

// countSkip counts the lines carrying the fragment.
func countSkip(report TickReport, fragment string) int {
	count := 0
	for _, line := range report.Skipped {
		if strings.Contains(line, fragment) {
			count++
		}
	}
	return count
}

func (f *supersededFixture) questionEvents(t *testing.T) int {
	t.Helper()
	events, err := f.st.RecentEvents(f.ctx, 50)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	questions := 0
	for _, event := range events {
		if event.Kind == "decision.verdict_question" {
			questions++
		}
	}
	return questions
}

// A REJECT on an ancestor of the head is the reviewer's question, not the
// operator's (superseded-verdict, D1/D3): no question row, no merge, and the
// journal still says why the merge waits — once per note.
func TestSupersededRejectAsksNobodyAndMergesNothing(t *testing.T) {
	f := newSupersededFixture(t, "REJECT "+ancestorSHA,
		map[string]bool{ancestorSHA + " " + supersededHead: true}, nil)
	report := f.tick(t)

	if len(report.Merges) != 0 {
		t.Fatalf("merges = %+v, want none: no verdict names the head", report.Merges)
	}
	if f.queue.createCalls != 0 {
		t.Fatalf("queue creates = %d, want 0: the operator is not asked", f.queue.createCalls)
	}
	if got := f.questionEvents(t); got != 0 {
		t.Fatalf("verdict questions = %d, want 0", got)
	}
	if !hasSkip(report, "superseded") {
		t.Fatalf("skipped = %v, want the superseded line", report.Skipped)
	}
	if len(f.git.ancestorChecks) == 0 {
		t.Fatalf("git was never asked: the classification must come from the repository")
	}

	// A second tick over the same note is silence: the ledger holds the fact.
	second := f.tick(t)
	if lines := countSkip(second, "superseded"); lines != 0 {
		t.Fatalf("second tick skipped = %v, want no repeat of the superseded line", second.Skipped)
	}
}

// An ACCEPT on an ancestor is the same class: nobody is asked, nothing merges.
func TestSupersededAcceptAsksNobodyAndMergesNothing(t *testing.T) {
	f := newSupersededFixture(t, "ACCEPT "+ancestorSHA,
		map[string]bool{ancestorSHA + " " + supersededHead: true}, nil)
	report := f.tick(t)

	if len(report.Merges) != 0 {
		t.Fatalf("merges = %+v, want none: an accept of an older commit decides nothing at the head", report.Merges)
	}
	if f.queue.createCalls != 0 {
		t.Fatalf("queue creates = %d, want 0: the operator is not asked", f.queue.createCalls)
	}
	if got := f.questionEvents(t); got != 0 {
		t.Fatalf("verdict questions = %d, want 0", got)
	}
	if !hasSkip(report, "superseded") {
		t.Fatalf("skipped = %v, want the superseded line", report.Skipped)
	}
}

// A sha that is not in the head's history is today's question, unchanged: the
// operator decides, because that note may be about anything (D1/D2).
func TestForeignVerdictStillAsksTheOperator(t *testing.T) {
	f := newSupersededFixture(t, "REJECT "+foreignSHA, map[string]bool{}, nil)
	report := f.tick(t)

	if f.queue.createCalls != 1 {
		t.Fatalf("queue creates = %d, want 1 question row; skipped = %v", f.queue.createCalls, report.Skipped)
	}
	if got := f.questionEvents(t); got != 1 {
		t.Fatalf("verdict questions = %d, want 1", got)
	}
	if len(report.Merges) != 0 {
		t.Fatalf("merges = %+v, want none", report.Merges)
	}
}

// The git check failing is doubt, and doubt asks (D2): the change removes a
// false alarm, never a question whose answer is unknown.
func TestSupersededCheckFailureAsksTheOperator(t *testing.T) {
	f := newSupersededFixture(t, "REJECT "+ancestorSHA, nil, errors.New("fatal: bad object "+ancestorSHA))
	report := f.tick(t)

	if f.queue.createCalls != 1 {
		t.Fatalf("queue creates = %d, want 1 question row; skipped = %v", f.queue.createCalls, report.Skipped)
	}
	if got := f.questionEvents(t); got != 1 {
		t.Fatalf("verdict questions = %d, want 1", got)
	}
	if hasSkip(report, "superseded") {
		t.Fatalf("skipped = %v, want no superseded claim on a failed check", report.Skipped)
	}
}

// The refusal is unchanged (D3): a later ACCEPT naming the head merges, because
// the superseded note never stored a verdict for it.
func TestAcceptAtTheHeadStillMerges(t *testing.T) {
	f := newSupersededFixture(t, "ACCEPT "+supersededHead, map[string]bool{}, nil)
	report := f.tick(t)

	if len(report.Merges) != 1 || !report.Merges[0].Merged {
		t.Fatalf("merges = %+v, want one; skipped = %v", report.Merges, report.Skipped)
	}
	if f.queue.createCalls != 0 {
		t.Fatalf("queue creates = %d, want 0", f.queue.createCalls)
	}
}
