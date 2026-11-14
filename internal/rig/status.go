package rig

import (
	"context"
	"fmt"
	"strings"
)

// StatusReport is the part of `rig status --json` the app needs (#429): where
// the OpenRig UI answers, so a surface can link to it instead of describing it.
// The read is additive — an older rig that omits a key leaves the field zero —
// and only the daemon block is modelled: the rig list is already read by
// `rig ps` and a second read of it would be a second truth.
type StatusReport struct {
	Daemon struct {
		State     string `json:"state"`
		Reachable bool   `json:"reachable"`
		Port      int    `json:"port"`
	} `json:"daemon"`
}

// UIURL is the OpenRig UI's base address, or "" when this daemon does not say
// where it listens. The UI is served by the daemon on its own port
// (`rig ui open` derives the same URL), so the port is the whole fact: the
// callers add no path of their own.
func (r StatusReport) UIURL() string {
	if r.Daemon.Port <= 0 {
		return ""
	}
	return fmt.Sprintf("http://127.0.0.1:%d", r.Daemon.Port)
}

// Status reads `rig status --json`. It is a read like every other adapter call:
// a daemon that is not running comes back as ErrDaemonDown, and the caller
// decides what an unread status means for its surface.
func (a *Adapter) Status(ctx context.Context) (StatusReport, error) {
	data, err := a.run(ctx, "status", "--json")
	if err != nil {
		return StatusReport{}, err
	}
	var report StatusReport
	if err := decode(data, &report); err != nil {
		return StatusReport{}, fmt.Errorf("status: %w", err)
	}
	report.Daemon.State = strings.ToLower(strings.TrimSpace(report.Daemon.State))
	return report, nil
}
