package forge

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestWritePermissionRefusalsAreClassified pins #258's contract at the adapter:
// a write the credential may not perform surfaces as a PermissionError carrying
// the operation, the forge and the adapter's refusal sentence byte-for-byte,
// while a transient condition (5xx, rate limit, timeout) never reads as one.
func TestWritePermissionRefusalsAreClassified(t *testing.T) {
	cases := []struct {
		name      string
		forge     string // "github" | "gitlab" — which adapter makes the call
		stderr    string
		runErr    string
		permit    bool
		operation string // expected on a hit
	}{
		{
			name:      "github 403 body is a permission refusal",
			forge:     "github",
			stderr:    "gh: Resource not accessible by personal access token (HTTP 403)",
			runErr:    "exit status 1",
			permit:    true,
			operation: "merge",
		},
		{
			name:      "github fine-grained 404 on a write is a permission refusal",
			forge:     "github",
			stderr:    "gh: Not Found (HTTP 404)",
			runErr:    "exit status 1",
			permit:    true,
			operation: "merge",
		},
		{
			name:   "github push-access sentence is a permission refusal",
			forge:  "github",
			stderr: "gh: remote: You must have push access to this repository",
			runErr: "exit status 1",
			permit: true,
		},
		{
			name:   "gitlab 403 forbidden is a permission refusal",
			forge:  "gitlab",
			stderr: "glab: error: 403 Forbidden",
			runErr: "exit status 1",
			permit: true,
		},
		{
			name:   "gitlab insufficient scope is a permission refusal",
			forge:  "gitlab",
			stderr: "glab: error: insufficient_scope: OAuth scope does not allow this operation",
			runErr: "exit status 1",
			permit: true,
		},
		{
			name:   "gitlab 401 on a write is a permission refusal",
			forge:  "gitlab",
			stderr: "glab: error: 401 Unauthorized",
			runErr: "exit status 1",
			permit: true,
		},
		{
			name:   "5xx is stale, not permission",
			forge:  "github",
			stderr: "HTTP 502: Bad Gateway",
			runErr: "exit status 1",
			permit: false,
		},
		{
			name:   "rate limit 403 is stale, not permission",
			forge:  "github",
			stderr: "gh: API rate limit exceeded for user ID 1. (HTTP 403)",
			runErr: "exit status 1",
			permit: false,
		},
		{
			name:   "context timeout is not permission",
			forge:  "github",
			stderr: "",
			runErr: "gh pr merge 9 --squash --match-head-commit abc -R dinhtrung/fleet-console: timed out after 1s",
			permit: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &stubRunner{respond: func(int, []string) (string, string, error) {
				return "", tc.stderr, errors.New(tc.runErr)
			}}
			var err error
			switch tc.forge {
			case "gitlab":
				err = NewGitLab("group/project", runner, &Budget{Max: 100}).
					Merge(context.Background(), 9, "abc")
			default:
				err = NewGitHub("dinhtrung/fleet-console", runner, &Budget{Max: 100}).
					Merge(context.Background(), 9, "abc")
			}
			if !tc.permit {
				if IsPermission(err) {
					t.Fatalf("error = %v, want no permission classification", err)
				}
				return
			}
			if !IsPermission(err) {
				t.Fatalf("error = %v (%T), want IsPermission", err, err)
			}
			if !errors.Is(err, ErrPermission) {
				t.Fatalf("error = %v, want errors.Is ErrPermission", err)
			}
			var permitted *PermissionError
			if !errors.As(err, &permitted) {
				t.Fatalf("error = %T, want *PermissionError", err)
			}
			if tc.operation != "" && permitted.Operation != tc.operation {
				t.Fatalf("operation = %q, want %q", permitted.Operation, tc.operation)
			}
			if permitted.Forge != tc.forge {
				t.Fatalf("forge = %q, want %q", permitted.Forge, tc.forge)
			}
			// The adapter's sentence survives byte-for-byte: the operator alert
			// quotes Detail verbatim.
			if permitted.Detail != tc.stderr {
				t.Fatalf("detail = %q, want the refusal sentence %q", permitted.Detail, tc.stderr)
			}
			for _, part := range []string{permitted.Forge, permitted.Operation, permitted.Detail} {
				if part != "" && !strings.Contains(err.Error(), part) {
					t.Fatalf("error = %q, want it to name %q", err.Error(), part)
				}
			}
		})
	}
}

// TestReadForbiddenIsNotPermission pins the boundary: the read path keeps its
// established words (ErrForbidden on a 403, stale on a 401), so the mirror's
// ForgeForbidden line is untouched by the write classification (#258).
func TestReadForbiddenIsNotPermission(t *testing.T) {
	runner := &stubRunner{respond: func(int, []string) (string, string, error) {
		return "", "glab: error: 403 Forbidden", errors.New("exit status 1")
	}}
	adapter := NewGitLab("group/project", runner, &Budget{Max: 100})
	if _, err := adapter.Items(context.Background(), Timeout{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("read 403 = %v, want ErrForbidden", err)
	}
	runner = &stubRunner{respond: func(int, []string) (string, string, error) {
		return "", "glab: error: HTTP 401 Unauthorized", errors.New("exit status 1")
	}}
	adapter = NewGitLab("group/project", runner, &Budget{Max: 100})
	_, readErr := adapter.Items(context.Background(), Timeout{})
	if !errors.Is(readErr, ErrStale) || IsPermission(readErr) {
		t.Fatalf("read 401 = %v, want stale and not permission", readErr)
	}
}
