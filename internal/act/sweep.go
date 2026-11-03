package act

import (
	"sort"
	"strings"

	"github.com/dinhtrung/fleet-console/internal/forge"
)

// Sweep decides whether an open epic should be closed by a tick that merged
// nothing. Rollup answers the same question for the item a merge just finished;
// this answers it for an epic the tick merely observed, whose last sub-issue may
// have closed by hand, as a dropped duplicate, or in an earlier tick whose close
// never landed. Neither function covers the other: Rollup needs a merged item to
// reach its parent pointer, and a tick with no merges has none.
//
// `epic` is the epic's own mirror row — the sweep's candidate, carrying the
// number and the state the mirror reported. `items` is what a read of that epic's
// sub-issues returned, open and closed both, because the tick's own snapshot is
// `state=open` only and cannot tell "no sub-issues" from "all closed"
// (internal/forge/github.go:163).
//
// It is pure: no forge, no store, no clock. The same row and read always give the
// same verdict, which is what lets the caller act on it and retry safely.
//
// The checks run in the order an operator reads the reasons. A row that is not an
// epic is not this step's subject. An epic already closed needs nothing, and
// saying so on every tick would bury the ticks that did something — so that skip
// and the non-epic one are Quiet. Only then is the children question asked.
func Sweep(epic forge.Item, items []forge.Item) Decision {
	if epic.Kind != forge.KindEpic {
		return Decision{Kind: RollupSkip, Epic: epic.Number, Reason: "not an epic", Quiet: true}
	}
	epicNumber := epic.Number
	if !strings.EqualFold(epic.State, forge.StateOpen) {
		// Idempotence lives here: a sweep runs every tick, so "already closed" is
		// the ordinary second sighting, not an anomaly.
		return Decision{Kind: RollupSkip, Epic: epicNumber, Reason: "epic already closed", Quiet: true}
	}

	var open []int
	children := 0
	for _, item := range items {
		if item.Parent == nil || *item.Parent != epicNumber {
			continue
		}
		children++
		if strings.EqualFold(item.State, forge.StateOpen) {
			open = append(open, item.Number)
		}
	}
	sort.Ints(open)

	if children == 0 {
		// An empty read is indistinguishable from "all closed", and closing an epic
		// on a short read would close it over work nobody looked at. The reason names
		// the read rather than the snapshot: this guard fires on the sweep's own
		// per-epic read, not on the tick's item list.
		return Decision{Kind: RollupSkip, Epic: epicNumber, Reason: "no sub-issues in the read"}
	}
	if len(open) > 0 {
		// Quiet: an epic in progress would otherwise put a skip line in the journal on
		// every tick until its last child closes, which is how a signal becomes noise.
		// The reason still names the open children, for a caller that asks.
		return Decision{
			Kind: RollupSkip, Epic: epicNumber, Children: children, Open: open, Quiet: true,
			Reason: plural(len(open)) + " still open (" + issueList(open) + ")",
		}
	}
	return Decision{Kind: RollupClose, Epic: epicNumber, Children: children}
}
