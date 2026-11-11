package rig

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestAsSessionCarriesTheIdentityIntoTheChild: the resolve verb refuses
// without OPENRIG_SESSION_NAME, so the operator runner must put it in the
// child's environment, on top of the parent's (#253). `env` echoes its own
// environment, so the output is the proof.
func TestAsSessionCarriesTheIdentityIntoTheChild(t *testing.T) {
	runner := ExecRunner{Path: "/usr/bin/env", Timeout: 5 * time.Second}.AsSession(OperatorSession)
	out, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(string(out), "OPENRIG_SESSION_NAME="+OperatorSession) {
		t.Fatalf("child environment lacks %s:\n%s", OperatorSession, out)
	}
}

// TestNewAdapterDerivesTheOperatorRunner: an ExecRunner gets an operator
// runner automatically; a bare Runner leaves the adapter to the parent
// environment, which is what the scripted tests and embedded callers want.
func TestNewAdapterDerivesTheOperatorRunner(t *testing.T) {
	if NewAdapter(ExecRunner{Path: "rig"}).Operator == nil {
		t.Fatal("ExecRunner did not derive an operator runner")
	}
	scripted := &scriptedRunner{fn: func([]string) ([]byte, error) { return nil, nil }}
	if NewAdapter(scripted).Operator != nil {
		t.Fatal("a bare Runner must not grow an operator runner")
	}
}

// TestQueueResolveRunsThroughTheOperator: when the adapter has an operator
// runner, the resolve argv goes to it, not to the ambient runner (#253).
func TestQueueResolveRunsThroughTheOperator(t *testing.T) {
	ambient := &scriptedRunner{fn: func(args []string) ([]byte, error) {
		return nil, fmt.Errorf("ambient runner must not see a resolve: %v", args)
	}}
	operator := &scriptedRunner{fn: func([]string) ([]byte, error) {
		return fixtureBytes(t, "queue-show.json"), nil
	}}
	adapter := NewAdapter(ambient)
	adapter.Operator = operator
	if _, err := adapter.QueueResolve(context.Background(), "q1", "yes"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !operator.saw("queue resolve q1 --decision yes --json") {
		t.Fatalf("operator saw: %v", operator.calls)
	}
}
