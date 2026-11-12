package rig

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// humanSeatPattern is the daemon's own shape for a human seat address:
// "human" or "human-<suffix>" at the kernel or the host. It is the guard's
// only predicate — anything else, including any destination that merely
// starts with "human", is a seat like any other.
var humanSeatPattern = regexp.MustCompile(`^human(?:-[A-Za-z0-9._-]+)?@(kernel|host)$`)

// IsHumanSeat reports whether a session address names a human seat. A create
// aimed at one is judged by the person, so the row must carry an evidence_ref
// the person can open later (convention C3).
func IsHumanSeat(session string) bool {
	return humanSeatPattern.MatchString(session)
}

// ErrMissingEvidenceRef is the local refusal of a human-routed queue create
// without evidence_ref. It is raised before any argv is built or any command
// runs, because the daemon's own 400 arrives as text with no caller context.
var ErrMissingEvidenceRef = errors.New("human-routed queue create requires evidence_ref")

// guardHumanCreate refuses, before building argv, a queue create that names a
// human destination without the evidence_ref the human judges the work by.
func guardHumanCreate(req CreateRequest) error {
	if !IsHumanSeat(req.Destination) {
		return nil
	}
	if strings.TrimSpace(req.EvidenceRef) != "" {
		return nil
	}
	return fmt.Errorf("queue create: destination %s is human-routed and needs evidence_ref (convention C3): pass the brief path: %w",
		req.Destination, ErrMissingEvidenceRef)
}
