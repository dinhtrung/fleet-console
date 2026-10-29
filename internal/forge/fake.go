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
func (f *Fake) Items(context.Context) ([]Item, error) {
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

// Notes returns the configured reviewer notes.
func (f *Fake) Notes(context.Context, int) ([]Comment, error) {
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
