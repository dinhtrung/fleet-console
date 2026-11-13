package forge

import (
	"context"
	"strings"
	"testing"
)

// envRunner is a test double that can carry an environment, the way CLIRunner
// does. Run records the argv; Env records what an adapter laid on the runner.
type envRunner struct {
	calls []string
	Env   []string
}

func (r *envRunner) Run(_ context.Context, argv ...string) (string, string, error) {
	r.calls = append(r.calls, strings.Join(argv, " "))
	return "{}", "", nil
}

func (r *envRunner) WithEnv(env []string) Runner {
	return &envRunner{
		Env:   append(append([]string(nil), r.Env...), env...),
		calls: r.calls,
	}
}

// TestGitLabHostEnv pins the adapter's side of forge-host D2: a self-hosted
// host lands as GITLAB_HOST on this adapter's runner, the default and empty
// hosts set nothing, and no route ever changes argv.
func TestGitLabHostEnv(t *testing.T) {
	ctx := context.Background()

	// A self-hosted adapter: env set, argv exactly the historical string.
	selfHosted := NewGitLab("horisen/mnp", "gitlab.horisen.org", &envRunner{}, &Budget{Max: 10})
	if err := selfHosted.Comment(ctx, 1, "hello"); err != nil {
		t.Fatalf("Comment: %v", err)
	}
	runner := selfHosted.cli.runner.(*envRunner)
	if len(runner.Env) != 1 || runner.Env[0] != "GITLAB_HOST=gitlab.horisen.org" {
		t.Fatalf("env = %v, want [GITLAB_HOST=gitlab.horisen.org]", runner.Env)
	}
	wantArgv := "glab api -X POST projects/horisen%2Fmnp/issues/1/notes -f body=hello"
	if got := runner.calls[0]; got != wantArgv {
		t.Fatalf("argv = %q, want the flag-free form %q", got, wantArgv)
	}

	// The default host: glab already answers there, so nothing is set and the
	// handed-in runner is used as is.
	defaulted := NewGitLab("horisen/mnp", "gitlab.com", &envRunner{}, nil)
	if defaulted.cli.runner.(*envRunner).Env != nil {
		t.Fatalf("default host set env %v, want none", defaulted.cli.runner.(*envRunner).Env)
	}
	missing := NewGitLab("horisen/mnp", "", &envRunner{}, nil)
	if missing.cli.runner.(*envRunner).Env != nil {
		t.Fatalf("missing host set env %v, want none", missing.cli.runner.(*envRunner).Env)
	}

	// A runner that cannot carry an environment is never the place a host
	// lands; the adapter must use it as handed in rather than fail.
	plain := NewGitLab("horisen/mnp", "gitlab.horisen.org", &stubRunner{}, nil)
	if err := plain.Comment(ctx, 1, "hello"); err != nil {
		t.Fatalf("Comment on a plain runner: %v", err)
	}
}

// TestTwoHostsOneProcess is the coexistence proof (forge-host D4): two
// adapters built in one process each record their own GITLAB_HOST and neither
// sees the other's, and a shared base runner is never mutated into carrying a
// process-wide host.
func TestTwoHostsOneProcess(t *testing.T) {
	one := NewGitLab("horisen/one", "gitlab.horisen.org", &envRunner{}, nil)
	two := NewGitLab("horisen/two", "gitlab.example.net", &envRunner{}, nil)

	if got := one.cli.runner.(*envRunner).Env; len(got) != 1 || got[0] != "GITLAB_HOST=gitlab.horisen.org" {
		t.Fatalf("first adapter env = %v, want its own host", got)
	}
	if got := two.cli.runner.(*envRunner).Env; len(got) != 1 || got[0] != "GITLAB_HOST=gitlab.example.net" {
		t.Fatalf("second adapter env = %v, want its own host", got)
	}

	// The same through the concrete CLIRunner, shared by value the way the
	// tick shares one forgeRunner across projects: each copy carries its own
	// host, the default adapters carry none, and the base stays empty.
	base := CLIRunner{Binaries: map[string]string{"glab": "/usr/bin/glab"}}
	selfHosted := NewGitLab("horisen/one", "gitlab.horisen.org", base, nil)
	defaulted := NewGitLab("horisen/two", "", base, nil)
	withDefault := NewGitLab("horisen/three", "gitlab.com", base, nil)

	copied, ok := selfHosted.cli.runner.(CLIRunner)
	if !ok {
		t.Fatalf("self-hosted adapter runner = %T, want a CLIRunner copy", selfHosted.cli.runner)
	}
	if len(copied.Env) != 1 || copied.Env[0] != "GITLAB_HOST=gitlab.horisen.org" {
		t.Fatalf("self-hosted env = %v, want its own host", copied.Env)
	}
	if got := copied.Binaries["glab"]; got != "/usr/bin/glab" {
		t.Fatalf("copy lost the binaries map: %q", got)
	}
	for name, adapter := range map[string]*GitLab{"default": defaulted, "gitlab.com": withDefault} {
		cr, ok := adapter.cli.runner.(CLIRunner)
		if !ok {
			t.Fatalf("%s adapter runner = %T, want a CLIRunner", name, adapter.cli.runner)
		}
		if len(cr.Env) != 0 {
			t.Fatalf("%s adapter env = %v, want none", name, cr.Env)
		}
	}
	if base.Env != nil {
		t.Fatalf("shared base env = %v, want untouched", base.Env)
	}
}
