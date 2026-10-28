package rig

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// QueueRow is one queue item from `rig queue list -A -a --json`.
type QueueRow struct {
	ID          string `json:"id"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	State       string `json:"state"`
	PickupState string `json:"pickup_state"`
	Summary     string `json:"summary,omitempty"`
}

// Seat is one node from `rig ps --nodes --full --json`.
type Seat struct {
	NodeID    string `json:"node_id"`
	RigID     string `json:"rig_id"`
	Rig       string `json:"rig"`
	Session   string `json:"session"`
	LogicalID string `json:"logical_id"`
	Lifecycle string `json:"lifecycle"`
}

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
	Summary     string `json:"summary"`
}

type seatWire struct {
	NodeID    string `json:"nodeId"`
	RigID     string `json:"rigId"`
	Rig       string `json:"rigName"`
	Session   string `json:"canonicalSessionName"`
	LogicalID string `json:"logicalId"`
	Lifecycle string `json:"lifecycleState"`
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
			PickupState: wire.Pickup.State,
			Summary:     wire.Summary,
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
		seat := Seat{
			NodeID:    wire.NodeID,
			RigID:     wire.RigID,
			Rig:       wire.Rig,
			Session:   wire.Session,
			LogicalID: wire.LogicalID,
			Lifecycle: wire.Lifecycle,
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
