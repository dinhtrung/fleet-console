package act

import "github.com/dinhtrung/fleet-console/internal/rig"

// Refusal is the act loop's hard stop. It carries the one named reason the
// operator sees; the loop must not perform a single write after receiving it.
type Refusal struct {
	Reason string
}

func (r *Refusal) Error() string { return "act loop refused: " + r.Reason }

// Gate is the act loop's first check. The loop is wired in task 5.x; every
// entry point calls Check before it plans or writes, so a failed compat probe
// keeps the act loop off while the mirror and the board keep serving.
type Gate struct {
	report rig.Report
}

// NewGate binds the gate to the boot compat report.
func NewGate(report rig.Report) Gate { return Gate{report: report} }

// Check returns a Refusal when the compat report is blocked, and nil when the
// act loop may start.
func (g Gate) Check() error {
	if !g.report.Blocked() {
		return nil
	}
	reason := g.report.Error
	if reason == "" {
		reason = "compat probe is blocked"
	}
	return &Refusal{Reason: reason}
}

// Reason is the named refusal reason, or an empty string when the gate is open.
func (g Gate) Reason() string {
	if err := g.Check(); err != nil {
		return err.(*Refusal).Reason
	}
	return ""
}
