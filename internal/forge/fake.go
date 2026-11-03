package forge

import (
	"context"
	"fmt"
)

// Fake is the in-memory Forge the decide-loop tests run against. It records every
// write so a test can assert the loop's intents without a forge.
type Fake struct {
	KindValue     Kind
	ItemsValue    []Item
	RequestsValue []ChangeRequest
	NotesValue    []Comment
	ReadErr       error
	ItemsErr      error
	RequestsErr   error
	WriteErr      error
	// Per-method write errors, for tests that need one forge write to fail and
	// the others to succeed — a merge that lands and whose post-merge close
	// fails cannot be expressed with a single WriteErr.
	CloseErr  error
	MarkerErr error
	ReadyErr  error
	MergeErr  error
	Accepted  map[string]struct{}
	Writes    []Write
	// NotesAsked and CRNotesAsked record the numbers each reader was asked
	// about, in order. A verdict lives on the change request, so CRNotesAsked is
	// where a test proves the gate read the right conversation.
	NotesAsked   []int
	CRNotesAsked []int
	// SubIssuesValue is the read SubIssues answers with, keyed by epic number. An
	// epic with no entry reads as no children, which is the empty-read case the
	// sweep must not close over. SubIssuesErr fails the read for every epic and
	// proves the failure path leaves the epic open; SubIssuesAsked records the
	// epics the tick read, so a test can assert the read count is bounded to one
	// per open epic per tick.
	SubIssuesValue map[int][]Item
	SubIssuesErr   error
	SubIssuesAsked []int
	// posted holds the comments this fake wrote, so Notes returns them the way a
	// real forge would. It is unexported: a test asserts through Writes or through
	// a caller's behaviour, not by reaching for this.
	posted []postedNote
}

// postedNote is one comment the fake wrote, keyed by the item it landed on.
type postedNote struct {
	Number int
	Body   string
}

// Write is one recorded mutation.
type Write struct {
	Op     string // marker | comment | close | merge
	Number int
	Value  string
}

// NewFake returns a fake bound to a kind with no data.
func NewFake(kind Kind) *Fake { return &Fake{KindValue: kind} }

// Kind reports the fake's forge.
func (f *Fake) Kind() Kind { return f.KindValue }

// Items returns the configured items or the configured read error.
func (f *Fake) Items(context.Context, Timeout) ([]Item, error) {
	if f.ItemsErr != nil {
		return nil, f.ItemsErr
	}
	if f.ReadErr != nil {
		return nil, f.ReadErr
	}
	return append([]Item(nil), f.ItemsValue...), nil
}

// ChangeRequests returns the configured change requests or the configured read error.
func (f *Fake) ChangeRequests(context.Context) ([]ChangeRequest, error) {
	if f.RequestsErr != nil {
		return nil, f.RequestsErr
	}
	if f.ReadErr != nil {
		return nil, f.ReadErr
	}
	return append([]ChangeRequest(nil), f.RequestsValue...), nil
}

// ChangeRequestsFor records the accepted head SHAs and returns the configured
// requests; the fake is already metadata-only.
func (f *Fake) ChangeRequestsFor(ctx context.Context, accepted map[string]struct{}) ([]ChangeRequest, error) {
	f.Accepted = accepted
	return f.ChangeRequests(ctx)
}

// Notes returns the configured reviewer notes, recording the number it was asked
// about so a test can assert the caller passed the item's number.
func (f *Fake) Notes(_ context.Context, number int) ([]Comment, error) {
	f.NotesAsked = append(f.NotesAsked, number)
	if f.ReadErr != nil {
		return nil, f.ReadErr
	}
	notes := append([]Comment(nil), f.NotesValue...)
	for _, note := range f.posted {
		if note.Number == number {
			notes = append(notes, Comment{ID: fmt.Sprint(len(notes)), Body: note.Body})
		}
	}
	return notes, nil
}

// CRNotes returns the same configured notes, recording the number it was asked
// about. A verdict is posted on the change request, so the number a caller
// passes here is the one that decides whether the gate can see the verdict.
func (f *Fake) CRNotes(_ context.Context, number int) ([]Comment, error) {
	f.CRNotesAsked = append(f.CRNotesAsked, number)
	if f.ReadErr != nil {
		return nil, f.ReadErr
	}
	return append([]Comment(nil), f.NotesValue...), nil
}

// SubIssues returns the configured read for one epic, recording the number it
// was asked about. It answers with the same slice it was given, so a caller that
// mutates the result cannot corrupt a later read — the sweep only counts.
func (f *Fake) SubIssues(_ context.Context, number int) ([]Item, error) {
	f.SubIssuesAsked = append(f.SubIssuesAsked, number)
	if f.SubIssuesErr != nil {
		return nil, f.SubIssuesErr
	}
	if f.ReadErr != nil {
		return nil, f.ReadErr
	}
	return append([]Item(nil), f.SubIssuesValue[number]...), nil
}

// SetMarker records a marker write.
// Ready records a readiness write and clears the draft flag on a matching request.
func (f *Fake) Ready(_ context.Context, number int) error {
	if f.WriteErr != nil {
		return f.WriteErr
	}
	if f.ReadyErr != nil {
		return f.ReadyErr
	}
	f.Writes = append(f.Writes, Write{Op: "ready", Number: number})
	for i := range f.RequestsValue {
		if f.RequestsValue[i].Number == number {
			f.RequestsValue[i].Draft = false
		}
	}
	return nil
}

func (f *Fake) SetMarker(_ context.Context, number int, present bool) error {
	if f.WriteErr != nil {
		return f.WriteErr
	}
	if f.MarkerErr != nil {
		return f.MarkerErr
	}
	value := "off"
	if present {
		value = "on"
	}
	f.Writes = append(f.Writes, Write{Op: "marker", Number: number, Value: value})
	return nil
}

// Comment records a comment write and folds it into the item's notes, so a
// caller that reads the item back sees what it posted — the same round trip a
// real forge gives. Without this a de-duplicating caller could not be tested: the
// write would be recorded but never readable.
func (f *Fake) Comment(_ context.Context, number int, body string) error {
	if f.WriteErr != nil {
		return f.WriteErr
	}
	f.Writes = append(f.Writes, Write{Op: "comment", Number: number, Value: body})
	f.posted = append(f.posted, postedNote{Number: number, Body: body})
	return nil
}

// CloseItem records a close write.
func (f *Fake) CloseItem(_ context.Context, number int) error {
	if f.WriteErr != nil {
		return f.WriteErr
	}
	if f.CloseErr != nil {
		return f.CloseErr
	}
	f.Writes = append(f.Writes, Write{Op: "close", Number: number})
	return nil
}

// Merge records a merge write.
func (f *Fake) Merge(_ context.Context, number int, sha string) error {
	if f.WriteErr != nil {
		return f.WriteErr
	}
	if f.MergeErr != nil {
		return f.MergeErr
	}
	f.Writes = append(f.Writes, Write{Op: "merge", Number: number, Value: sha})
	return nil
}

var _ Forge = (*Fake)(nil)
var _ Forge = (*GitHub)(nil)
var _ Forge = (*GitLab)(nil)
