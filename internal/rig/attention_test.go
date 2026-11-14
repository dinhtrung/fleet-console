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

// TestParseSeatsReadsAttention (#425): a block exists when the rig's flag says
// so, and the question is the text the rig records for it — the activity's input
// reason, then the attention error, then the hook's reason. These are the live
// 0.7.13 shapes.
func TestParseSeatsReadsAttention(t *testing.T) {
	seats := parseSeatsFixture(t, `[
	  {"nodeId":"n1","rigName":"rig-a","canonicalSessionName":"dev-review@rig-a","lifecycleState":"attention_required",
	   "needsAttention": true, "arbitratedNeedsInput": null, "needsInputEvidence": null,
	   "activityState": {"needsInput": {"count": 0, "reason": null}},
	   "agentActivity": {"reason": "user_prompt_submit"},
	   "latestError": "Harness launch requires attention: Codex resume has not yet reached an interactive conversation"},
	  {"nodeId":"n2","rigName":"rig-a","canonicalSessionName":"dev-builder-omp@rig-a","lifecycleState":"attention_required",
	   "needsAttention": false,
	   "activityState": {"needsInput": {"count": 1, "reason": "  Allow the seat to start?  "}}},
	  {"nodeId":"n3","rigName":"rig-a","canonicalSessionName":"dev-builder-pi@rig-a","lifecycleState":"attention_required",
	   "arbitratedNeedsInput": true,
	   "latestError": "Approve the merge?"}
	]`)
	want := [][]SeatAttention{
		{{Seat: "dev-review@rig-a", Rig: "rig-a", Kind: AttentionKindAttention,
			Question: "Harness launch requires attention: Codex resume has not yet reached an interactive conversation"}},
		{{Seat: "dev-builder-omp@rig-a", Rig: "rig-a", Kind: AttentionKindInput, Question: "Allow the seat to start?"}},
		{{Seat: "dev-builder-pi@rig-a", Rig: "rig-a", Kind: AttentionKindArbitrated, Question: "Approve the merge?"}},
	}
	if len(seats) != len(want) {
		t.Fatalf("seats = %d, want %d", len(seats), len(want))
	}
	for index := range want {
		if len(seats[index].Attention) != len(want[index]) {
			t.Fatalf("seat %d attention = %+v, want %+v", index, seats[index].Attention, want[index])
		}
		for entry := range want[index] {
			if seats[index].Attention[entry] != want[index][entry] {
				t.Fatalf("seat %d attention[%d] = %+v, want %+v",
					index, entry, seats[index].Attention[entry], want[index][entry])
			}
		}
	}
}

// TestParseSeatsAttentionIsAdditive (#425): the live shape of a **working** seat
// — flags false, and `needsInputEvidence` present because the rig records the
// evidence of an observed clear as well — must decode to no block at all. Reading
// that evidence as a block invented a human wait on every node.
func TestParseSeatsAttentionIsAdditive(t *testing.T) {
	seats := parseSeatsFixture(t, `[
	  {"nodeId":"n1","rigName":"rig-a","canonicalSessionName":"dev-builder-claude@rig-a","lifecycleState":"running",
	   "needsAttention": false, "arbitratedNeedsInput": false,
	   "needsInputEvidence": {"rung": "lifecycle-hooks", "observedAt": "2026-11-14T00:47:01.480Z"},
	   "activityState": {"needsInput": {"count": 0, "reason": null}},
	   "agentActivity": {"reason": "user_prompt_submit"}},
	  {"nodeId":"n2","rigName":"rig-a","canonicalSessionName":"dev-builder-pi@rig-a","lifecycleState":"running"}
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

// TestParseSeatsAttentionWithoutText (#425): a flag set with no text anywhere is
// still a block — the seat is waiting, and the board says the rig gave no
// question rather than hiding the seat.
func TestParseSeatsAttentionWithoutText(t *testing.T) {
	seats := parseSeatsFixture(t, `[
	  {"nodeId":"n1","rigName":"rig-a","canonicalSessionName":"dev-builder-claude@rig-a","lifecycleState":"attention_required",
	   "needsAttention": true,
	   "activityState": {"needsInput": {"count": 0, "reason": null}},
	   "agentActivity": {"reason": ""},
	   "latestError": null}
	]`)
	attention := seats[0].Attention
	if len(attention) != 1 {
		t.Fatalf("attention = %+v, want one entry", attention)
	}
	if attention[0].Kind != AttentionKindAttention || attention[0].Question != "" {
		t.Fatalf("attention[0] = %+v, want an attention block with no question", attention[0])
	}
}

// TestAttentionKeysAreKnown (#425): the compat probe warned "unknown key" for
// these because the supported schema did not list them. Modelling the decode and
// the schema together is what stops the warning.
func TestAttentionKeysAreKnown(t *testing.T) {
	for _, key := range []string{"needsAttention", "arbitratedNeedsInput", "needsInputEvidence"} {
		if _, ok := known.seat[key]; !ok {
			t.Fatalf("%q is not in the supported seat schema; the probe will warn", key)
		}
	}
}
