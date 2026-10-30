package forge

import "context"

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
	Accepted      map[string]struct{}
	Writes        []Write
	// NotesAsked and CRNotesAsked record the numbers each reader was asked
	// about, in order. A verdict lives on the change request, so CRNotesAsked is
	// where a test proves the gate read the right conversation.
	NotesAsked   []int
	CRNotesAsked []int
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
	return append([]Comment(nil), f.NotesValue...), nil
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

// SetMarker records a marker write.
func (f *Fake) SetMarker(_ context.Context, number int, present bool) error {
	if f.WriteErr != nil {
		return f.WriteErr
	}
	value := "off"
	if present {
		value = "on"
	}
	f.Writes = append(f.Writes, Write{Op: "marker", Number: number, Value: value})
	return nil
}

// Comment records a comment write.
func (f *Fake) Comment(_ context.Context, number int, body string) error {
	if f.WriteErr != nil {
		return f.WriteErr
	}
	f.Writes = append(f.Writes, Write{Op: "comment", Number: number, Value: body})
	return nil
}

// CloseItem records a close write.
func (f *Fake) CloseItem(_ context.Context, number int) error {
	if f.WriteErr != nil {
		return f.WriteErr
	}
	f.Writes = append(f.Writes, Write{Op: "close", Number: number})
	return nil
}

// Merge records a merge write.
func (f *Fake) Merge(_ context.Context, number int, sha string) error {
	if f.WriteErr != nil {
		return f.WriteErr
	}
	f.Writes = append(f.Writes, Write{Op: "merge", Number: number, Value: sha})
	return nil
}

var _ Forge = (*Fake)(nil)
var _ Forge = (*GitHub)(nil)
var _ Forge = (*GitLab)(nil)
