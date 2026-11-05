package main

import (
	"testing"

	"github.com/dinhtrung/fleet-console/internal/act"
	"github.com/dinhtrung/fleet-console/internal/config"
	"github.com/dinhtrung/fleet-console/internal/decisions"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/mirror"
	"github.com/dinhtrung/fleet-console/internal/rig"
)

// The producer, not just the renderer. The read a tick plans against was taken
// before the tick created this decision's row, so the row is absent from
// projectRead.Rows and the count must seed it by identity. Seeding 1 and then
// counting rows made a lone decision report 1 and count a pre-existing row
// twice, so "if open > 1" needed two old rows before the reply named the id.
func TestNoteContextCountsTheDecisionsOwnRow(t *testing.T) {
	raised := act.DecisionRaised{QItemID: "qitem-new", Item: 178, Text: "merge or hold?"}
	eff := &config.Effective{Listen: "127.0.0.1:8787"}

	t.Run("a lone decision is one open", func(t *testing.T) {
		nc := noteContextFor(raised, eff, mirror.ProjectResult{})
		if nc.OpenOnItem != 1 {
			t.Fatalf("OpenOnItem = %d, want 1", nc.OpenOnItem)
		}
		if got := decisions.ReplyLine(decisions.Decision{ID: "qitem-new"}, nc); got != "fleet: accept" {
			t.Fatalf("reply line = %q, want the unnamed form for one open decision", got)
		}
	})

	t.Run("one pre-existing row on the same item makes two", func(t *testing.T) {
		read := mirror.ProjectResult{
			Rows: []rig.QueueRow{{
				ID:          "qitem-old",
				Source:      "dev-build-1@fleet-console",
				Destination: "human@kernel",
				State:       "pending",
				Summary:     "#178 review: merge or hold?",
			}},
		}
		nc := noteContextFor(raised, eff, read)
		if nc.OpenOnItem != 2 {
			t.Fatalf("OpenOnItem = %d, want 2: one pre-existing row plus this decision", nc.OpenOnItem)
		}
		if got := decisions.ReplyLine(decisions.Decision{ID: "qitem-new"}, nc); got != "fleet: accept qitem-new" {
			t.Fatalf("reply line = %q, want the id named", got)
		}
	})

	t.Run("the read already holding this row does not double count it", func(t *testing.T) {
		read := mirror.ProjectResult{
			Rows: []rig.QueueRow{{
				ID:          "qitem-new",
				Source:      "dev-build-1@fleet-console",
				Destination: "human@kernel",
				State:       "pending",
				Summary:     "#178 review: merge or hold?",
			}},
		}
		nc := noteContextFor(raised, eff, read)
		if nc.OpenOnItem != 1 {
			t.Fatalf("OpenOnItem = %d, want 1: the row is the decision, not a second one", nc.OpenOnItem)
		}
	})

	t.Run("a row waiting on a peer is not a human decision", func(t *testing.T) {
		read := mirror.ProjectResult{
			Rows: []rig.QueueRow{
				{ID: "qitem-old", Destination: "dev-review@fleet-console", State: "pending", Summary: "#178 review"},
				{ID: "qitem-peer", Destination: "dev-build-2@fleet-console", State: "blocked", BlockedOn: "qitem-old", Summary: "#178 follow-up"},
			},
		}
		nc := noteContextFor(raised, eff, read)
		if nc.OpenOnItem != 1 {
			t.Fatalf("OpenOnItem = %d, want 1: peer-blocked rows are not obligations on a person", nc.OpenOnItem)
		}
	})

	t.Run("another item's human row does not count", func(t *testing.T) {
		read := mirror.ProjectResult{
			Rows: []rig.QueueRow{{ID: "qitem-other", Destination: "human@kernel", State: "pending", Summary: "#999 review"}},
		}
		nc := noteContextFor(raised, eff, read)
		if nc.OpenOnItem != 1 {
			t.Fatalf("OpenOnItem = %d, want 1: #999's row is not #178's", nc.OpenOnItem)
		}
	})

	t.Run("the item URL comes from the read", func(t *testing.T) {
		read := mirror.ProjectResult{
			Items: []forge.Item{{Number: 178, URL: "https://github.com/o/r/issues/178"}},
		}
		nc := noteContextFor(raised, eff, read)
		if nc.ItemURL != "https://github.com/o/r/issues/178" {
			t.Fatalf("ItemURL = %q", nc.ItemURL)
		}
		if want := "http://127.0.0.1:8787/decisions#qitem-new"; nc.BoardURL != want {
			t.Fatalf("BoardURL = %q, want %q", nc.BoardURL, want)
		}
	})
}
