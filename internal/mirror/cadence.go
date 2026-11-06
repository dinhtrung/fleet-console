package mirror

import "time"

// Cadence is the poll backoff: the wait doubles after each quiet tick up to Max
// and returns to Base on the first busy one. Pure arithmetic — the loop owns the
// timers and the caller owns the busy verdict, so the sequence is testable
// without sleeping.
type Cadence struct {
	Base time.Duration
	Max  time.Duration

	wait   time.Duration
	streak int
}

// Next returns the wait to use after a tick that ended quiet or busy, and
// records the quiet streak the log line names.
//
// Four consecutive quiet ticks produce Base, 2*Base, 4*Base and 8*Base, capped
// at Max; a busy tick returns Base and clears the streak. The first quiet tick
// returns Base rather than doubling, because the wait it is replacing was
// already the normal interval.
//
// A non-positive Max means "no ceiling": the wait stays at Base, since a
// doubling sequence with no bound would overflow. Config load already refuses a
// ceiling below the base, so production always has Max >= Base > 0.
func (c *Cadence) Next(quiet bool) time.Duration {
	if !quiet {
		c.streak = 0
		c.wait = c.Base
		return c.Base
	}
	if c.Max <= 0 {
		c.wait = c.Base
		return c.wait
	}
	c.streak++
	if c.wait <= 0 || c.streak == 1 {
		c.wait = c.Base
		return c.wait
	}
	if next := c.wait * 2; next < c.wait {
		c.wait = c.Max // overflow guard; unreachable with a real ceiling
	} else if next > c.Max {
		c.wait = c.Max
	} else {
		c.wait = next
	}
	return c.wait
}

// Streak reports how many consecutive quiet ticks produced the last wait. It is
// zero after a busy tick and after construction.
func (c *Cadence) Streak() int {
	return c.streak
}
