package main

import (
	"testing"

	"github.com/dinhtrung/fleet-console/internal/config"
)

// The reviewer's finding: config leaves the list empty when decision_answerers is unset, and its own
// comment says empty means the owner. Nothing resolved it, so on a fresh install no login could
// answer and the spec's default-author-rule scenario could not pass.
func TestDecisionAnswerersDefaultsToTheSlugOwner(t *testing.T) {
	cases := []struct {
		name       string
		configured []string
		slug       string
		want       []string
	}{
		{
			name: "empty list defaults to the owner",
			slug: "dinhtrung/fleet-console",
			want: []string{"dinhtrung"},
		},
		{
			name:       "a configured list wins",
			configured: []string{"alice", "bob"},
			slug:       "dinhtrung/fleet-console",
			want:       []string{"alice", "bob"},
		},
		{
			name: "a GitLab-style group path takes the first segment",
			slug: "mygroup/sub/project",
			want: []string{"mygroup"},
		},
		{
			name: "a slug with no owner part defaults to nothing",
			slug: "nofork",
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eff := &config.Effective{DecisionAnswerers: tc.configured}
			got := decisionAnswerers(eff, tc.slug)
			if len(got) != len(tc.want) {
				t.Fatalf("answerers = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("answerers = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// A nil Effective must not panic, and must fall back to the slug owner the same way.
func TestDecisionAnswerersHandlesNilEffective(t *testing.T) {
	got := decisionAnswerers(nil, "dinhtrung/fleet-console")
	if len(got) != 1 || got[0] != "dinhtrung" {
		t.Fatalf("answerers = %v, want [dinhtrung]", got)
	}
}
