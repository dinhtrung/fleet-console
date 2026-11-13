package rig

import (
	"errors"
	"fmt"
	"strings"
)

// ErrDaemonDown reports that no rig daemon answered the command. It is the one
// rig failure with a decision attached (epic #320, design D1): the lifecycle may
// start a fleet over it, so it must be typed and `errors.Is`-able. Everything
// else — a timeout, a bad JSON shape, an unknown error — stays a generic
// failure the lifecycle refuses to act on, because a 20-second timeout on a
// busy box would otherwise launch a second fleet over a live one.
var ErrDaemonDown = errors.New("rig daemon down")

// daemonDownSentences are the daemon's own words for "no daemon here", plus the
// two shapes a refused local connection takes. Measured on the box the service
// runs on:
//
//	Error: Daemon not running.
//	  This command needs a running daemon.
//	  Run 'rig up' (it auto-starts the daemon), or 'rig daemon start'.
//
// Matching is case-insensitive and on stderr text (which the runner embeds in
// the error), nothing else — a timeout is not a missing daemon and "unknown
// error" is not either.
var daemonDownSentences = []string{
	"daemon not running",
	"needs a running daemon",
	"connection refused",
	"econnrefused",
}

// isDaemonDown reports whether a failed rig call's text says the daemon is
// missing. It is the classification rule from epic #320 §0, in this one place.
func isDaemonDown(stderr string) bool {
	lower := strings.ToLower(stderr)
	for _, sentence := range daemonDownSentences {
		if strings.Contains(lower, sentence) {
			return true
		}
	}
	return false
}

// classifyDaemonDown wraps err as ErrDaemonDown when its text says the daemon
// is missing, so every verb on the adapter's error path inherits the fact with
// `errors.Is`. Other errors pass through unchanged.
func classifyDaemonDown(err error) error {
	if err == nil || !isDaemonDown(err.Error()) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrDaemonDown, err)
}
