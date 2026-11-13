package rig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// QueueRow is one queue item from `rig queue list -A -a --json`.
type QueueRow struct {
	ID          string `json:"id"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	State       string `json:"state"`
	// BlockedOn names what a blocked row waits on: a human seat, another qitem,
	// or a typed gate. `rig queue list` carries it, and it is the only field that
	// distinguishes a row parked on a person from one parked on a peer's work.
	BlockedOn   string `json:"blocked_on"`
	PickupState string `json:"pickup_state"`
	Summary     string `json:"summary,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
	LastNudgeAt string `json:"last_nudge_at,omitempty"`
	Body        string `json:"body,omitempty"`
}

// Seat is one node from `rig ps --nodes -A --full --json` (or a targeted `--rig`).
type Seat struct {
	NodeID       string `json:"node_id"`
	RigID        string `json:"rig_id"`
	Rig          string `json:"rig"`
	Session      string `json:"session"`
	LogicalID    string `json:"logical_id"`
	Lifecycle    string `json:"lifecycle"`
	Runtime      string `json:"runtime"`
	Model        string `json:"model"`
	Profile      string `json:"profile"`
	Permission   string `json:"permission"`
	LastActivity string `json:"last_activity"`
}

// Running reports whether this seat's lifecycle is the running state, which is
// the only fact about whether its rig is up (openspec rig-lifecycle: "The
// running seats are the only fact about whether a rig is up"). A stopped rig
// keeps its node rows, and those rows report a lifecycle that is not `running`
// — exited, recoverable, attention_required — so counting rows instead of
// running rows makes a stopped rig read as a live fleet (#403).
func (s Seat) Running() bool {
	return s.Lifecycle == LifecycleRunning
}

// LifecycleRunning is the seat lifecycle the rig adapter reports for a seat
// that is up and serving.
const LifecycleRunning = "running"

// FieldError names the command and the field that failed its required check.
// Field is the domain name; Path is the wire name when they differ.
type FieldError struct {
	Command string
	Field   string
	Path    string
}

func (e *FieldError) Error() string {
	if e.Path != "" && e.Path != e.Field {
		return fmt.Sprintf("%s: required field %q (%s) missing or empty", e.Command, e.Field, e.Path)
	}
	return fmt.Sprintf("%s: required field %q missing or empty", e.Command, e.Field)
}

type queueRowWire struct {
	Pickup struct {
		State    string `json:"state"`
		Evidence string `json:"evidence"`
	} `json:"pickup"`
	ID          string `json:"qitemId"`
	Source      string `json:"sourceSession"`
	Destination string `json:"destinationSession"`
	State       string `json:"state"`
	BlockedOn   string `json:"blockedOn"`
	Summary     string `json:"summary"`
	CreatedAt   string `json:"tsCreated"`
	LastNudge   string `json:"lastNudgeAttempt"`
	Body        string `json:"body"`
}

type seatWire struct {
	NodeID           string `json:"nodeId"`
	RigID            string `json:"rigId"`
	Rig              string `json:"rigName"`
	Session          string `json:"canonicalSessionName"`
	LogicalID        string `json:"logicalId"`
	Lifecycle        string `json:"lifecycleState"`
	Runtime          string `json:"runtime"`
	Model            string `json:"model"`
	Profile          string `json:"profile"`
	PermissionPolicy string `json:"permissionPolicy"`
	LastActivity     string `json:"lastActivityAt"`
}

// ParseQueueList decodes the queue list and enforces the required fields.
// unknown, when non-nil, sees every key the supported schema does not know.
func ParseQueueList(data []byte, unknown func(key string)) ([]QueueRow, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("queue list: %w", err)
	}
	rows := make([]QueueRow, 0, len(raw))
	for i, item := range raw {
		var top map[string]json.RawMessage
		if err := json.Unmarshal(item, &top); err != nil {
			return nil, fmt.Errorf("queue list: row %d: %w", i, err)
		}
		for key := range top {
			if _, ok := known.queueRow[key]; !ok && unknown != nil {
				unknown(key)
			}
		}
		var pickup map[string]json.RawMessage
		if rawPickup, ok := top["pickup"]; ok {
			if err := json.Unmarshal(rawPickup, &pickup); err == nil {
				for key := range pickup {
					if _, ok := known.pickup[key]; !ok && unknown != nil {
						unknown(key)
					}
				}
			}
		}

		var wire queueRowWire
		if err := json.Unmarshal(item, &wire); err != nil {
			return nil, fmt.Errorf("queue list: row %d: %w", i, err)
		}
		row := QueueRow{
			ID:          wire.ID,
			Source:      wire.Source,
			Destination: wire.Destination,
			State:       wire.State,
			BlockedOn:   wire.BlockedOn,
			PickupState: wire.Pickup.State,
			Summary:     wire.Summary,
			CreatedAt:   wire.CreatedAt,
			LastNudgeAt: wire.LastNudge,
			Body:        wire.Body,
		}
		for _, check := range []struct {
			field, path, value string
		}{
			{"qitem_id", "qitemId", row.ID},
			{"destination_session", "destinationSession", row.Destination},
			{"state", "state", row.State},
			{"pickup_state", "pickup.state", row.PickupState},
		} {
			if check.value == "" {
				return nil, &FieldError{Command: "queue list", Field: check.field, Path: check.path}
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// ParseSeats decodes the node list and enforces the seat identity fields.
func ParseSeats(data []byte, unknown func(key string)) ([]Seat, error) {
	var entries []json.RawMessage
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &entries); err != nil {
			return nil, fmt.Errorf("ps --nodes: %w", err)
		}
	} else {
		var payload struct {
			Entries []json.RawMessage `json:"entries"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return nil, fmt.Errorf("ps --nodes: %w", err)
		}
		if payload.Entries == nil {
			return nil, &FieldError{Command: "ps --nodes", Field: "entries", Path: "entries"}
		}
		entries = payload.Entries
	}
	seats := make([]Seat, 0, len(entries))
	for i, item := range entries {
		var top map[string]json.RawMessage
		if err := json.Unmarshal(item, &top); err != nil {
			return nil, fmt.Errorf("ps --nodes: entry %d: %w", i, err)
		}
		for key := range top {
			if _, ok := known.seat[key]; !ok && unknown != nil {
				unknown(key)
			}
		}

		var wire seatWire
		if err := json.Unmarshal(item, &wire); err != nil {
			return nil, fmt.Errorf("ps --nodes: entry %d: %w", i, err)
		}
		permission := wire.PermissionPolicy
		if permission == "" {
			permission = wire.Profile
		}
		seat := Seat{
			NodeID:       wire.NodeID,
			RigID:        wire.RigID,
			Rig:          wire.Rig,
			Session:      wire.Session,
			LogicalID:    wire.LogicalID,
			Lifecycle:    wire.Lifecycle,
			Runtime:      wire.Runtime,
			Model:        wire.Model,
			Profile:      wire.Profile,
			Permission:   permission,
			LastActivity: wire.LastActivity,
		}
		for _, check := range []struct {
			field, path, value string
		}{
			{"node_id", "nodeId", seat.NodeID},
			{"rig", "rigName", seat.Rig},
			{"session", "canonicalSessionName", seat.Session},
			{"lifecycle", "lifecycleState", seat.Lifecycle},
		} {
			if check.value == "" {
				return nil, &FieldError{Command: "ps --nodes", Field: check.field, Path: check.path}
			}
		}
		seats = append(seats, seat)
	}
	return seats, nil
}

// HumanDecision reports whether an address names a person rather than a seat:
// an operator or advisor (`human@`), or an external party.
func HumanDecision(address string) bool {
	return strings.Contains(address, "human@") || strings.HasSuffix(address, "@external")
}

// WaitsOnHuman reports whether a queue row is an obligation on a person, given
// its state, destination and blocker. It is the one rule behind
// /api/decisions, the mirror's row filter and the quiet predicate, so those
// three can never disagree about what the operator owes.
//
// Three shapes, all real:
//
//   - pending, addressed to a person — an obligation the moment it exists
//     (internal/act/adopt.go creates this for an ambiguous dispatch);
//   - blocked, whose blocker is a person, whatever its destination — the usual
//     decision shape: the person answers, an agent seat holds the row;
//   - blocked, addressed to a person, with no blocker.
//
// It takes the three fields rather than a QueueRow because the board resolves
// each row through QueueShow, which yields a QueueItem. A second copy of this
// rule is how the count and the notice drift apart (issue #143, and the review
// that caught the first fix missing two shapes).
func WaitsOnHuman(state, destination, blockedOn string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "pending":
		return HumanDecision(destination)
	case "blocked":
		return HumanDecision(destination) || HumanDecision(blockedOn)
	default:
		return false
	}
}
