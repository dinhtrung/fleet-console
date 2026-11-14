package decide

import "testing"

// Task 3.2: the refusal names the human when a seat waits on one. Driven through Decide so this
// asserts the detail the operator reads, not a helper's return value (#426, design D3).
func TestNoFreeBuilderNamesTheSeatWaitingOnAHuman(t *testing.T) {
	s := Snapshot{
		Project: Project{Key: "app", Forge: ForgeGitHub, Enabled: true},
		Items:   []Item{{Number: 42, Kind: KindTask, State: "open", Title: "ready work"}},
		// The waiting seat is attention_required, so the rig reports it as not running and it never
		// reaches Builders: the fact arrives on its own list.
		Builders:       []string{"seat-1", "seat-2"},
		WaitingOnHuman: []string{"seat-3"},
		Rows: []Row{
			{State: "working", Destination: "seat-1"},
			{State: "working", Destination: "seat-2"},
		},
		ForgeOK: true,
	}
	result := Decide(s)
	if len(result.Dispatches) != 0 {
		t.Fatalf("dispatches = %+v, want none", result.Dispatches)
	}
	if len(result.Skips) != 1 {
		t.Fatalf("skips = %+v, want one", result.Skips)
	}
	if result.Skips[0].Reason != ReasonNoFreeBuilder {
		t.Fatalf("skip = %+v, want %s", result.Skips[0], ReasonNoFreeBuilder)
	}
	const want = "every admitted seat is taken (1 waiting on a human)"
	if result.Skips[0].Detail != want {
		t.Fatalf("skip detail = %q, want %q", result.Skips[0].Detail, want)
	}
}

// The string a busy fleet has always produced, kept byte for byte: the count is added only when
// there is one, so no detail ever grows a "(0 waiting on a human)".
func TestNoFreeBuilderDetailIsUnchangedWithNoSeatWaiting(t *testing.T) {
	s := Snapshot{
		Project:  Project{Key: "app", Forge: ForgeGitHub, Enabled: true},
		Items:    []Item{{Number: 42, Kind: KindTask, State: "open", Title: "ready work"}},
		Builders: []string{"seat-1", "seat-2"},
		Rows: []Row{
			{State: "working", Destination: "seat-1"},
			{State: "working", Destination: "seat-2"},
		},
		ForgeOK: true,
	}
	result := Decide(s)
	if len(result.Skips) != 1 {
		t.Fatalf("skips = %+v, want one", result.Skips)
	}
	const want = "every admitted seat is taken"
	if result.Skips[0].Detail != want {
		t.Fatalf("skip detail = %q, want %q", result.Skips[0].Detail, want)
	}
}

// A seat on a rig the project does not declare is not this project's business, so the detail does
// not count it — the same rule fillSeats applies to the seats it may use. The concurrency cap is
// the one configured path that reaches this line with a tier declared.
func TestNoFreeBuilderCountsOnlyAdmittedSeats(t *testing.T) {
	cases := []struct {
		name    string
		waiting []string
		want    string
	}{
		{
			name:    "a waiting seat on a declared rig is counted",
			waiting: []string{"seat-3@primary"},
			want:    "every admitted seat is taken (1 waiting on a human)",
		},
		{
			name:    "a waiting seat on an undeclared rig is not",
			waiting: []string{"seat-9@standby"},
			want:    "every admitted seat is taken",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := Snapshot{
				Project: Project{
					Key: "app", Forge: ForgeGitHub, Enabled: true, ConcurrencyCap: 2,
					Rigs: []RigRef{{Name: "primary", Tier: 0, Spill: SpillOnFull}},
				},
				Items: []Item{
					{Number: 41, Kind: KindTask, State: "open", Title: "one"},
					{Number: 42, Kind: KindTask, State: "open", Title: "two"},
					{Number: 43, Kind: KindTask, State: "open", Title: "three"},
				},
				Builders:       []string{"seat-1@primary", "seat-2@primary", "seat-3@primary"},
				WaitingOnHuman: tc.waiting,
				ForgeOK:        true,
			}
			result := Decide(s)
			if len(result.Dispatches) != 2 {
				t.Fatalf("dispatches = %+v, want two (the cap)", result.Dispatches)
			}
			if len(result.Skips) != 1 {
				t.Fatalf("skips = %+v, want one", result.Skips)
			}
			if result.Skips[0].Detail != tc.want {
				t.Fatalf("skip detail = %q, want %q", result.Skips[0].Detail, tc.want)
			}
		})
	}
}

// A tier refusal names its own trigger and keeps that wording: the human count is a second fact,
// and the spec's refusal form deliberately carries one parenthetical.
func TestTierRefusalKeepsItsWordingWhenASeatWaitsOnAHuman(t *testing.T) {
	s := Snapshot{
		Project: Project{
			Key: "app", Forge: ForgeGitHub, Enabled: true,
			Rigs: []RigRef{
				{Name: "primary", Tier: 0, Spill: SpillOnFull},
				{Name: "standby", Tier: 1, Spill: SpillNever},
			},
		},
		Items:          []Item{{Number: 42, Kind: KindTask, State: "open", Title: "ready work"}},
		Builders:       []string{"seat-1@primary", "seat-2@standby"},
		WaitingOnHuman: []string{"seat-3@primary"},
		Rows:           []Row{{State: "working", Destination: "seat-1@primary"}},
		ForgeOK:        true,
	}
	result := Decide(s)
	if len(result.Skips) != 1 {
		t.Fatalf("skips = %+v, want one", result.Skips)
	}
	const want = "tier 1 (spill=never: an earlier tier exists on standby)"
	if result.Skips[0].Detail != want {
		t.Fatalf("skip detail = %q, want %q", result.Skips[0].Detail, want)
	}
}
