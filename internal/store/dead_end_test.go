package store

import (
	"context"
	"strconv"
	"testing"
	"time"
)

// TestDeadEndReleasesReadsTheOperatorsAnswer pins the ledger fold behind
// "answer the question with release" (#526): the raise names the head and the
// row it created, the answer on that row says release, and the read returns
// the (item, head) pair with the answer's time. An answer meaning "hold" is
// not a release, and a subject naming an item another project mirrors is out
// of scope — the same granularity every event read in this package accepts.
func TestDeadEndReleasesReadsTheOperatorsAnswer(t *testing.T) {
	ctx := context.Background()
	st := upsertStore(t)
	at := time.Date(2026, 11, 18, 4, 0, 0, 0, time.UTC)

	alpha, err := st.CreateProject(ctx, ProjectInput{
		Key: "alpha", Name: "alpha", ForgeKind: "github", ForgeProject: "o/alpha",
		Rig: "rig", ConcurrencyCap: 1, Enabled: true,
	})
	if err != nil {
		t.Fatalf("alpha: %v", err)
	}
	beta, err := st.CreateProject(ctx, ProjectInput{
		Key: "beta", Name: "beta", ForgeKind: "github", ForgeProject: "o/beta",
		Rig: "rig", ConcurrencyCap: 1, Enabled: true,
	})
	if err != nil {
		t.Fatalf("beta: %v", err)
	}
	if err := st.ReplaceItems(ctx, alpha, []ItemRecord{
		{Number: 7, Kind: "task", State: "open", Title: "Seven", URL: "u", UpdatedAt: at},
		{Number: 8, Kind: "task", State: "open", Title: "Eight", URL: "u", UpdatedAt: at},
		{Number: 9, Kind: "task", State: "open", Title: "Nine", URL: "u", UpdatedAt: at},
	}); err != nil {
		t.Fatalf("alpha items: %v", err)
	}
	if err := st.ReplaceItems(ctx, beta, []ItemRecord{
		{Number: 17, Kind: "task", State: "open", Title: "Seventeen", URL: "u", UpdatedAt: at},
	}); err != nil {
		t.Fatalf("beta items: %v", err)
	}

	appendEvent := func(kind, subject, detail string) {
		t.Helper()
		if _, err := st.AppendEvent(ctx, Event{Actor: "act", Kind: kind, Subject: subject, Detail: detail}); err != nil {
			t.Fatalf("event %s %s: %v", kind, subject, err)
		}
	}
	raise := func(item int, head, row string) {
		appendEvent("decision.dead_end", "#"+strconv.Itoa(item),
			"alpha #"+strconv.Itoa(item)+": the last dispatch ended without a change request; head="+head+"; row "+row)
	}
	// Released: the operator answered the raise's row with "release".
	raise(7, "abc1234", "qitem-1")
	appendEvent("decision.answer", "qitem-1", "release")
	// Held: an answer that is not a release changes nothing.
	raise(8, "def5678", "qitem-2")
	appendEvent("decision.answer", "qitem-2", "hold it for now")
	// Answered "onhold" is not a release either; the word is the signal.
	raise(9, "9876543", "qitem-3")
	appendEvent("decision.answer", "qitem-3", "onhold until the deploy")
	// Beta's number: alpha's read filters it out.
	raise(17, "abc1234", "qitem-4")
	appendEvent("decision.answer", "qitem-4", "release")

	got, err := st.DeadEndReleases(ctx, alpha)
	if err != nil {
		t.Fatalf("dead end releases: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("releases = %+v, want only item 7", got)
	}
	if got[0].Item != 7 || got[0].Head != "abc1234" || got[0].At.IsZero() {
		t.Fatalf("release = %+v, want item 7 at head abc1234 with the answer's time", got[0])
	}

	betaGot, err := st.DeadEndReleases(ctx, beta)
	if err != nil {
		t.Fatalf("beta dead end releases: %v", err)
	}
	if len(betaGot) != 1 || betaGot[0].Item != 17 {
		t.Fatalf("beta releases = %+v, want beta's own item only", betaGot)
	}
}
