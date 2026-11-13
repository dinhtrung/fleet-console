package rig

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestErrDaemonDown: the classification rule from epic #320 §0, in one place.
// The daemon's own sentence and a refused local connection are the missing
// daemon; a timeout, a malformed JSON shape and an unknown error are not — the
// lifecycle may start a fleet over the first two and must never act on the rest.
func TestErrDaemonDown(t *testing.T) {
	// The measured sentence, exactly as the CLI prints it on this box.
	measured := "Error: Daemon not running.\n  This command needs a running daemon.\n" +
		"  Run 'rig up' (it auto-starts the daemon), or 'rig daemon start'.\n"

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "the daemon's own sentence", err: errors.New(measured), want: true},
		{name: "the second sentence alone", err: errors.New("rig: this command needs a running daemon."), want: true},
		{name: "a refused local connection", err: errors.New("rig ps: dial unix /tmp/openrig.sock: connect: connection refused"), want: true},
		{name: "ECONNREFUSED", err: errors.New("rig ps: ECONNREFUSED"), want: true},
		{name: "a timeout is not down", err: errors.New("rig ps: timed out after 20s"), want: false},
		{name: "context deadline is not down", err: context.DeadlineExceeded, want: false},
		{name: "malformed JSON is not down", err: errors.New("rig queue list: decode rig response: invalid character 'x' looking for beginning of value"), want: false},
		{name: "an unknown error is not down", err: errors.New("rig ps: exit status 1"), want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDaemonDown(tc.err.Error()); got != tc.want {
				t.Fatalf("isDaemonDown(%q) = %v, want %v", tc.err.Error(), got, tc.want)
			}
			classified := classifyDaemonDown(tc.err)
			if errors.Is(classified, ErrDaemonDown) != tc.want {
				t.Fatalf("classifyDaemonDown: errors.Is = %v, want %v", classified, tc.want)
			}
			if !tc.want && !errors.Is(classified, tc.err) {
				t.Fatalf("classifyDaemonDown changed a generic error: %v", classified)
			}
		})
	}
}

// TestEveryVerbInheritsDaemonDown: the classification lives on the adapter's
// error path — a read and a write both surface it as ErrDaemonDown, so no
// caller has to re-match the sentence.
func TestEveryVerbInheritsDaemonDown(t *testing.T) {
	measured := "Error: Daemon not running.\n  This command needs a running daemon.\n"
	adapter := NewAdapter(runnerFunc(func(_ context.Context, args ...string) ([]byte, error) {
		return nil, errors.New("rig " + strings.Join(args, " ") + ": " + measured)
	}))
	if _, err := adapter.QueueList(context.Background()); !errors.Is(err, ErrDaemonDown) {
		t.Fatalf("QueueList: %v, want ErrDaemonDown", err)
	}
	if _, err := adapter.QueueCreate(context.Background(), CreateRequest{
		Destination: "x@r", Summary: "s", BodyFile: "/tmp/b",
	}); !errors.Is(err, ErrDaemonDown) {
		t.Fatalf("QueueCreate: %v, want ErrDaemonDown", err)
	}
}
