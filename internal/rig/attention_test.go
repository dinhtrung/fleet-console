package rig

import (
	"testing"
)

func parseSeatsFixture(t *testing.T, payload string) []Seat {
	t.Helper()
	seats, err := ParseSeats([]byte(payload), nil)
	if err != nil {
		t.Fatalf("parse seats: %v", err)
	}
	return seats
}

// TestParseSeatsReadsAttention (#425): the three keys the rig already sends are
// modelled, one entry per block, in the order attention, input, arbitrated, with
// the text the rig gave.
func TestParseSeatsReadsAttention(t *testing.T) {
	seats := parseSeatsFixture(t, `[
	  {"nodeId":"n1","rigName":"rig-a","canonicalSessionName":"dev-builder-claude@rig-a","lifecycleState":"running",
	   "needsAttention": true,
	   "needsInputEvidence": {"rung":"lifecycle-hooks","question":"  Allow the seat to start?  "},
	   "arbitratedNeedsInput": "Approve the merge?"}
	]`)
	if len(seats) != 1 {
		t.Fatalf("seats = %d, want 1", len(seats))
	}
	attention := seats[0].Attention
	if len(attention) != 3 {
		t.Fatalf("attention = %+v, want three entries", attention)
	}
	want := []SeatAttention{
		{Seat: "dev-builder-claude@rig-a", Rig: "rig-a", Kind: AttentionKindAttention, Question: ""},
		{Seat: "dev-builder-claude@rig-a", Rig: "rig-a", Kind: AttentionKindInput, Question: "Allow the seat to start?"},
		{Seat: "dev-builder-claude@rig-a", Rig: "rig-a", Kind: AttentionKindArbitrated, Question: "Approve the merge?"},
	}
	for index := range want {
		if attention[index] != want[index] {
			t.Fatalf("attention[%d] = %+v, want %+v", index, attention[index], want[index])
		}
	}
}

// TestParseSeatsAttentionIsAdditive (#425): a rig that sends none of the keys
// decodes to an empty list and no error — the decode must not start refusing
// older rigs.
func TestParseSeatsAttentionIsAdditive(t *testing.T) {
	seats := parseSeatsFixture(t, `[
	  {"nodeId":"n1","rigName":"rig-a","canonicalSessionName":"dev-builder-claude@rig-a","lifecycleState":"running"},
	  {"nodeId":"n2","rigName":"rig-a","canonicalSessionName":"dev-review@rig-a","lifecycleState":"running",
	   "needsAttention": false, "arbitratedNeedsInput": false, "needsInputEvidence": null}
	]`)
	if len(seats) != 2 {
		t.Fatalf("seats = %d, want 2", len(seats))
	}
	for index, seat := range seats {
		if len(seat.Attention) != 0 {
			t.Fatalf("seat %d attention = %+v, want none", index, seat.Attention)
		}
	}
}

// TestParseSeatsAttentionWithoutText (#425): a key set with no text is still a
// block — the seat is waiting, and the board says the rig gave no question.
func TestParseSeatsAttentionWithoutText(t *testing.T) {
	seats := parseSeatsFixture(t, `[
	  {"nodeId":"n1","rigName":"rig-a","canonicalSessionName":"dev-builder-claude@rig-a","lifecycleState":"running",
	   "needsAttention": true,
	   "needsInputEvidence": {"rung":"lifecycle-hooks","observedAt":"2026-11-14T00:36:55.666Z"}}
	]`)
	attention := seats[0].Attention
	if len(attention) != 2 {
		t.Fatalf("attention = %+v, want two entries", attention)
	}
	for index, entry := range attention {
		if entry.Question != "" {
			t.Fatalf("attention[%d] question = %q, want empty", index, entry.Question)
		}
	}
}

// TestAttentionKeysAreKnown (#425): the compat probe warned "unknown key" for
// these three because the supported schema did not list them. Modelling the
// decode and the schema together is what stops the warning.
func TestAttentionKeysAreKnown(t *testing.T) {
	for _, key := range []string{"needsAttention", "arbitratedNeedsInput", "needsInputEvidence"} {
		if _, ok := known.seat[key]; !ok {
			t.Fatalf("%q is not in the supported seat schema; the probe will warn", key)
		}
	}
}
