package rig

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// schemaJSON lists every key the supported rig version is known to emit. A key
// outside these sets is a new release's field: tolerated, logged once per
// version. Generate it from a recorded fixture when Floor moves.
//
//go:embed schema/0.7.0.json
var schemaJSON []byte

type knownSets struct {
	queueRow map[string]struct{}
	pickup   map[string]struct{}
	seat     map[string]struct{}
}

var known = loadKnownSets()

func loadKnownSets() knownSets {
	var raw struct {
		QueueRow []string `json:"queue_row"`
		Pickup   []string `json:"pickup"`
		Seat     []string `json:"seat"`
	}
	if err := json.Unmarshal(schemaJSON, &raw); err != nil {
		panic(fmt.Sprintf("rig: embedded schema is invalid: %v", err))
	}
	toSet := func(keys []string) map[string]struct{} {
		set := make(map[string]struct{}, len(keys))
		for _, key := range keys {
			set[key] = struct{}{}
		}
		return set
	}
	return knownSets{
		queueRow: toSet(raw.QueueRow),
		pickup:   toSet(raw.Pickup),
		seat:     toSet(raw.Seat),
	}
}
