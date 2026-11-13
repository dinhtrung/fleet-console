package web

import (
	"fmt"
	"time"

	"github.com/dinhtrung/fleet-console/internal/config"
)

// How old each mirror read is (#394). The store's mirror_meta keeps the last
// successful forge and rig read, and no board screen read it: a stalled mirror
// and a forge with nothing to report looked identical. The phrase is built
// here, server-side, so every client shows the same words and the same
// thresholds.
//
// A read is fresh until two tick intervals have passed, quiet between two and
// five, and stalled beyond five. A project the mirror has never read says so in
// words — "0s" would dress a never-read project up as a very fresh one.
const (
	mirrorQuietAfter   = 2
	mirrorStalledAfter = 5
)

// mirrorRead is one "last forge read <age>" line on a project header.
type mirrorRead struct {
	// Label is the whole phrase the header renders, e.g.
	// "last forge read 4m · stalled".
	Label string `json:"label"`
	// State is never, ok, quiet or stalled.
	State string `json:"state"`
	// At is the read's stamp, empty when the project has never read.
	At string `json:"at,omitempty"`
}

// mirrorReadAt builds one line from a read's stamp. now is the request's clock
// and tick the mirror's poll cadence; a zero tick falls back to the config
// default so a caller that never learned the cadence cannot make every read
// look stalled.
func mirrorReadAt(now time.Time, at *time.Time, tick time.Duration, source string) mirrorRead {
	label := "last " + source + " read"
	if at == nil || at.IsZero() {
		return mirrorRead{Label: label + " never", State: "never"}
	}
	if tick <= 0 {
		tick = config.DefaultTickInterval
	}
	age := ageBetween(now, *at)
	elapsed := now.Sub(*at)
	state := "ok"
	switch {
	case elapsed > mirrorStalledAfter*tick:
		state = "stalled"
	case elapsed > mirrorQuietAfter*tick:
		state = "quiet"
	}
	label += " " + age
	if state != "ok" {
		label += " · " + state
	}
	return mirrorRead{Label: label, State: state, At: at.UTC().Format(time.RFC3339)}
}

// ageBetween formats the distance between two instants the way the board's age
// column does. The mirror-read line and the item age share it, so "4m" means
// the same thing in both places.
func ageBetween(now, at time.Time) string {
	d := now.Sub(at)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
