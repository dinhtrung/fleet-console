package mirror

// Waker asks a running Loop to tick before its timer would fire. One wake runs
// one tick: a second Wake while the first is still pending is coalesced, because
// the tick it would trigger is already about to run and reading the forge twice in
// one second is the cost the wake exists to avoid (design D1).
//
// The channel has capacity 1 and the send never blocks, so an HTTP handler holding
// a Waker cannot be stalled by a loop that is mid-tick. A nil *Waker is safe: Wake
// returns false and Channel returns nil, so a caller with no receiver configured
// keeps working.
type Waker struct {
	wake chan struct{}
}

// NewWaker returns a Waker with one slot for a pending wake.
func NewWaker() *Waker {
	return &Waker{wake: make(chan struct{}, 1)}
}

// Channel is what Loop selects on. It is nil when the receiver is nil, which
// disables the wake arm.
func (w *Waker) Channel() <-chan struct{} {
	if w == nil {
		return nil
	}
	return w.wake
}

// Wake asks for one tick. It reports false when a wake is already pending: the
// request was coalesced into the one about to run, so the caller must not count it
// as a second tick. The send is non-blocking and never waits for the loop.
func (w *Waker) Wake() bool {
	if w == nil {
		return false
	}
	select {
	case w.wake <- struct{}{}:
		return true
	default:
		return false
	}
}
