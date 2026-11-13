package store

import (
	"context"
	"testing"
	"time"
)

// TestMalformedVerdictsScopeAndNewestPerItem checks the three things the read
// must get right (epic #345 T1): the scope — a subject the project does not
// mirror comes back in nobody's read — the newest event per item (the fact is
// once per note, and the newest spelling is the one a surface shows), and the
// detail vocabulary parsing, including a note line that itself contains the
// `;` separator.
//
// The scoping test uses distinct item numbers per project on purpose: a
// subject names a number, not a project, so where two projects mirror the
// same number their events cannot be told apart — the granularity every
// other event read in this package already accepts (EventsForItem,
// HasEvent). The read's doc comment names that limit.
func TestMalformedVerdictsScopeAndNewestPerItem(t *testing.T) {
	ctx := context.Background()
	st := upsertStore(t)
	at := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)

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
	}); err != nil {
		t.Fatalf("alpha items: %v", err)
	}
	if err := st.ReplaceItems(ctx, beta, []ItemRecord{
		{Number: 17, Kind: "task", State: "open", Title: "Seventeen", URL: "u", UpdatedAt: at},
	}); err != nil {
		t.Fatalf("beta items: %v", err)
	}

	appendMalformed := func(subject, detail string) {
		t.Helper()
		if _, err := st.AppendEvent(ctx, Event{
			Actor: "act", Kind: "verdict.malformed", Subject: subject, Detail: detail,
		}); err != nil {
			t.Fatalf("event %s: %v", subject, err)
		}
	}
	// Alpha item 7: two ticks over the same note; the newer one wins.
	appendMalformed("#7", "note=2; head=old0; line=## QA Verdict: ACCEPT ✅")
	appendMalformed("#7", "note=2; head=ab1c234; line=## QA Verdict: ACCEPT ✅")
	// Alpha item 8: a line whose own text carries the `; line=` separator.
	appendMalformed("#8", "note=5; head=ab1c234; line=REJECT ; line=broken")
	// Beta item 17: a number alpha does not mirror — excluded from alpha.
	appendMalformed("#17", "note=9; head=be7a111; line=ACCEPT")
	// A subject naming an item nobody mirrors is in no project's read.
	appendMalformed("#99", "note=4; head=ab1c234; line=ACCEPT")

	got, err := st.MalformedVerdicts(ctx, alpha)
	if err != nil {
		t.Fatalf("malformed verdicts: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("alpha map = %v, want items 7 and 8 only (beta's number and the unmirrored #99 excluded)", got)
	}
	if v := got[7]; v.NoteID != "2" || v.Head != "ab1c234" || v.Line != "## QA Verdict: ACCEPT ✅" {
		t.Fatalf("item 7 = %+v, want the newest spelling of note 2", v)
	}
	if got[7].FirstSeen == "" {
		t.Fatalf("item 7 FirstSeen is empty, want the event's at")
	}
	if v := got[8]; v.NoteID != "5" || v.Head != "ab1c234" || v.Line != "REJECT ; line=broken" {
		t.Fatalf("item 8 = %+v, want the line kept whole past the separator", v)
	}

	betaGot, err := st.MalformedVerdicts(ctx, beta)
	if err != nil {
		t.Fatalf("beta malformed verdicts: %v", err)
	}
	if len(betaGot) != 1 {
		t.Fatalf("beta map = %v, want only beta's own item 17", betaGot)
	}
	if v := betaGot[17]; v.Head != "be7a111" {
		t.Fatalf("beta item 17 = %+v, want beta's own head", v)
	}
}
