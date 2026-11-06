package mirror

import (
	"strings"
	"testing"

	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/rig"
)

// D7's verdict, one table. The cases that matter are the ones "has an intent"
// would get wrong: an item blocked on another item, an item on hold, an open
// change request awaiting a verdict, and any failed or stale read.
func TestBusy(t *testing.T) {
	tests := []struct {
		name string
		read TickResult
		want bool
		// reasonContains is the fact the reset line must name.
		reasonContains string
	}{
		{
			name: "an empty read is quiet",
			read: TickResult{Projects: map[string]ProjectResult{"app": {}}},
			want: false,
		},
		{
			name: "an open item is busy",
			read: TickResult{Projects: map[string]ProjectResult{"app": {
				Items: []forge.Item{{Number: 12, State: forge.StateOpen, Title: "one"}},
			}}},
			want:           true,
			reasonContains: "#12 open",
		},
		{
			name: "a closed item is quiet",
			read: TickResult{Projects: map[string]ProjectResult{"app": {
				Items: []forge.Item{{Number: 12, State: forge.StateClosed, Title: "one"}},
			}}},
			want: false,
		},
		{
			name: "an open change request is busy",
			read: TickResult{Projects: map[string]ProjectResult{"app": {
				CRs: []forge.ChangeRequest{{Number: 40, State: forge.StateOpen}},
			}}},
			want:           true,
			reasonContains: "change request #40 open",
		},
		{
			name: "a merged change request is quiet",
			read: TickResult{Projects: map[string]ProjectResult{"app": {
				CRs: []forge.ChangeRequest{{Number: 40, State: forge.StateClosed, Merged: true}},
			}}},
			want: false,
		},
		{
			name: "a non-terminal queue row is busy",
			read: TickResult{Projects: map[string]ProjectResult{"app": {
				Rows: []rig.QueueRow{{ID: "q1", State: "in-progress"}},
			}}},
			want:           true,
			reasonContains: "queue row q1 in-progress",
		},
		{
			name: "a terminal queue row is quiet",
			read: TickResult{Projects: map[string]ProjectResult{"app": {
				Rows: []rig.QueueRow{{ID: "q1", State: "handed-off"}},
			}}},
			want: false,
		},
		{
			// A stale read carries its error in practice; the error text is more
			// use to an operator than the word "stale", so it wins.
			name: "a stale forge read names its error",
			read: TickResult{Projects: map[string]ProjectResult{"app": {
				ForgeStale: true, ForgeError: "HTTP 500",
			}}},
			want:           true,
			reasonContains: "forge read failed: HTTP 500",
		},
		{
			name: "a stale forge read with no error still counts as busy",
			read: TickResult{Projects: map[string]ProjectResult{"app": {
				ForgeStale: true,
			}}},
			want:           true,
			reasonContains: "forge read stale",
		},
		{
			name: "a failed forge read is busy",
			read: TickResult{Projects: map[string]ProjectResult{"app": {
				ForgeError: "deadline exceeded",
			}}},
			want:           true,
			reasonContains: "forge read failed: deadline exceeded",
		},
		{
			name: "a forbidden forge read is busy",
			read: TickResult{Projects: map[string]ProjectResult{"app": {
				ForgeForbidden: true,
			}}},
			want:           true,
			reasonContains: "forge read forbidden",
		},
		{
			name: "a failed branch read is busy",
			read: TickResult{Projects: map[string]ProjectResult{"app": {
				BranchError: "no such remote",
			}}},
			want:           true,
			reasonContains: "branch read failed: no such remote",
		},
		{
			name: "a stale rig read is busy",
			read: TickResult{
				Projects: map[string]ProjectResult{"app": {}},
				RigStale: true,
			},
			want:           true,
			reasonContains: "rig read stale",
		},
		{
			name: "a failed rig read is busy",
			read: TickResult{
				Projects: map[string]ProjectResult{"app": {}},
				RigError: "daemon unreachable",
			},
			want:           true,
			reasonContains: "rig read failed: daemon unreachable",
		},
		{
			name: "one busy project among quiet ones is busy",
			read: TickResult{Projects: map[string]ProjectResult{
				"alpha": {},
				"beta":  {Items: []forge.Item{{Number: 3, State: forge.StateOpen}}},
				"gamma": {},
			}},
			want:           true,
			reasonContains: "beta: #3 open",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := Busy(tc.read)
			if got != tc.want {
				t.Fatalf("busy = %v (%q), want %v", got, reason, tc.want)
			}
			if tc.want && !strings.Contains(reason, tc.reasonContains) {
				t.Fatalf("reason = %q, want it to contain %q", reason, tc.reasonContains)
			}
			if !tc.want && reason != "" {
				t.Fatalf("quiet read named a reason: %q", reason)
			}
		})
	}
}

// The named reason must not depend on Go's map iteration order, or the reset log
// line would change run to run for no reason.
func TestBusyReasonIsDeterministic(t *testing.T) {
	read := TickResult{Projects: map[string]ProjectResult{
		"zeta":  {Items: []forge.Item{{Number: 9, State: forge.StateOpen}}},
		"alpha": {Items: []forge.Item{{Number: 4, State: forge.StateOpen}}},
		"mike":  {Items: []forge.Item{{Number: 7, State: forge.StateOpen}}},
	}}
	first, _ := Busy(read)
	if !first {
		t.Fatal("read should be busy")
	}
	for i := range 50 {
		_, reason := Busy(read)
		if !strings.HasPrefix(reason, "alpha:") {
			t.Fatalf("run %d named %q; want the lowest project key first", i+1, reason)
		}
	}
}
