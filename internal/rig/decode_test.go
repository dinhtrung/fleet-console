package rig

import "testing"

// TestParseQueueListCarriesResolution pins the one wire field a dead-end
// question quotes (#526): `rig queue list -a` carries the row's resolution,
// and the decoder must not drop it on the floor.
func TestParseQueueListCarriesResolution(t *testing.T) {
	row := []byte(`[{"qitemId":"qitem-1","destinationSession":"human@kernel","state":"done","resolution":"blocked: needs infra","pickup":{"state":"terminal"},"tsCreated":"2026-11-18T04:00:00Z"}]`)
	rows, err := ParseQueueList(row, nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want one", rows)
	}
	if rows[0].Resolution != "blocked: needs infra" {
		t.Fatalf("resolution = %q, want the row's closing text", rows[0].Resolution)
	}
}
