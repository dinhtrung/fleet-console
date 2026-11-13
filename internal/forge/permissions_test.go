package forge

import "testing"

// TestFixPermission pins the per-operation table (#329): the merge is filed
// under Contents, not Pull requests, and an operation the table does not know
// reads empty so the caller keeps its generic sentence.
func TestFixPermission(t *testing.T) {
	tests := []struct {
		operation string
		want      string
	}{
		{"merge", "Contents: write"},
		{"ready", "Pull requests: write"},
		{"create_pr", "Pull requests: write"},
		{"comment", "Issues: write"},
		{"close", "Issues: write"},
		{"label", "Issues: write"},
		{"unknown-op", ""},
		{"", ""},
	}
	for _, tc := range tests {
		if got := FixPermission(tc.operation); got != tc.want {
			t.Errorf("FixPermission(%q) = %q, want %q", tc.operation, got, tc.want)
		}
	}
}
