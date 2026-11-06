package mirror

import "testing"

// Coalescing lives in the sender: one slot means a second wake while the first is
// still pending reports false, and once the loop consumes the first the next wake
// reports true again. This is the whole reason the channel has capacity 1.
func TestWakerCoalesces(t *testing.T) {
	w := NewWaker()

	if !w.Wake() {
		t.Fatal("first wake = false, want true: the slot is empty")
	}
	if w.Wake() {
		t.Fatal("second wake = true, want false: a wake is already pending")
	}
	// Still false with the same pending wake, so the coalescing is not a one-shot.
	if w.Wake() {
		t.Fatal("third wake = true, want false: still one pending")
	}

	// The loop's receive: consume the one pending wake.
	<-w.Channel()

	if !w.Wake() {
		t.Fatal("wake after consumption = false, want true: the slot is free again")
	}
	<-w.Channel()

	// And it keeps working across cycles, so this is not an accidental single reuse.
	for i := 0; i < 3; i++ {
		if !w.Wake() {
			t.Fatalf("cycle %d: wake = false, want true", i+1)
		}
		if w.Wake() {
			t.Fatalf("cycle %d: duplicate wake = true, want false", i+1)
		}
		<-w.Channel()
	}
}

// A nil Waker is what a caller without a receiver gets. It must not panic and must
// not claim a wake it cannot deliver: Loop selects on a nil channel, which blocks
// forever and so leaves the timer arm in charge.
func TestNilWakerIsInert(t *testing.T) {
	var w *Waker
	if w.Wake() {
		t.Fatal("nil waker reported a wake, want false")
	}
	if ch := w.Channel(); ch != nil {
		t.Fatalf("nil waker channel = %v, want nil", ch)
	}
}
