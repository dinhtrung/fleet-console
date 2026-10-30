package rig

import (
	"strings"
	"testing"
)

// TestKnownRuntime: exactly four values, case-insensitive, canonical lowercase.
func TestKnownRuntime(t *testing.T) {
	accepted := map[string]string{
		"pi": RuntimePi, "omp": RuntimeOMP, "codex": RuntimeCodex, "claude": RuntimeClaude,
		"PI": RuntimePi, "Omp": RuntimeOMP, "Codex": RuntimeCodex, "CLAUDE": RuntimeClaude,
		"  omp  ": RuntimeOMP,
	}
	for input, want := range accepted {
		got, ok := KnownRuntime(input)
		if !ok || got != want {
			t.Fatalf("KnownRuntime(%q) = %q, %v; want %q, true", input, got, ok, want)
		}
	}
	for _, bad := range []string{"gpt", "claud", "claude-2", "openai", "", "  "} {
		if got, ok := KnownRuntime(bad); ok {
			t.Fatalf("KnownRuntime(%q) = %q, true; want refused", bad, got)
		}
	}
	if len(Runtimes) != 4 {
		t.Fatalf("Runtimes has %d values, want 4", len(Runtimes))
	}
}

// TestRenderRigRefusesUnknownRuntime is the belt-and-braces half: even a row
// that never went through config load cannot render a runtime nobody can
// authenticate.
func TestRenderRigRefusesUnknownRuntime(t *testing.T) {
	base := Project{
		Key: "app", Rig: "rig-a", Builders: 1, ProviderEnv: "X_API_KEY",
		CulturePath: "/tmp/CULTURE.md", BuilderModel: "m",
		WorktreeRoot: "/tmp/wt", ReviewerCwd: "/tmp/review",
	}
	for _, bad := range []string{"gpt", "claud", "claude-2"} {
		p := base
		p.Runtime = bad
		_, _, err := RenderRig(p)
		if err == nil {
			t.Fatalf("RenderRig accepted runtime %q", bad)
		}
		if !strings.Contains(err.Error(), "is not one of pi, omp, codex, claude") {
			t.Fatalf("error = %v, want it to name the four values", err)
		}
	}
}

// TestRenderRigDefaultsAndCanonicalises: absent means omp, and any case renders
// the canonical value.
func TestRenderRigDefaultsAndCanonicalises(t *testing.T) {
	base := Project{
		Key: "app", Rig: "rig-a", Builders: 1, ProviderEnv: "X_API_KEY",
		CulturePath: "/tmp/CULTURE.md", BuilderModel: "m",
		WorktreeRoot: "/tmp/wt", ReviewerCwd: "/tmp/review",
	}
	for input, want := range map[string]string{"": RuntimeOMP, "CLAUDE": RuntimeClaude, "Pi": RuntimePi} {
		p := base
		p.Runtime = input
		spec, _, err := RenderRig(p)
		if err != nil {
			t.Fatalf("RenderRig(%q): %v", input, err)
		}
		// The runtime lands on every member; the reviewer is the last one.
		for _, pod := range spec.Pods {
			for _, member := range pod.Members {
				if member.Runtime != want {
					t.Fatalf("RenderRig(%q) member %s runtime = %q, want %q", input, member.ID, member.Runtime, want)
				}
			}
		}
	}
}
