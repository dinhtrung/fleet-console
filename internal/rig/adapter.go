package rig

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Adapter is one function per rig CLI command. Domain types leave this package;
// no decoded map or raw JSON does.
type Adapter struct {
	Runner Runner
}

// NewAdapter returns an adapter over a runner.
func NewAdapter(runner Runner) *Adapter { return &Adapter{Runner: runner} }

func (a *Adapter) run(ctx context.Context, args ...string) ([]byte, error) {
	if a == nil || a.Runner == nil {
		return nil, fmt.Errorf("rig adapter: no runner")
	}
	return a.Runner.Run(ctx, args...)
}

func decode(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("decode rig response: %w", err)
	}
	return nil
}

// QueueItem is one queue row as the board reads it.
type QueueItem struct {
	ID              string `json:"id"`
	Source          string `json:"source"`
	Destination     string `json:"destination"`
	State           string `json:"state"`
	PickupState     string `json:"pickup_state"`
	Priority        string `json:"priority"`
	Tier            string `json:"tier"`
	Summary         string `json:"summary"`
	Body            string `json:"body"`
	BlockedOn       string `json:"blocked_on"`
	ClosureReason   string `json:"closure_reason"`
	ObligationOwner string `json:"obligation_owner"`
	ObligationState string `json:"obligation_state"`
	CreatedAt       string `json:"created_at"`
	DeadlineAt      string `json:"deadline_at"`
	EvidenceRef     string `json:"evidence_ref"`
}

type queueItemWire struct {
	Pickup struct {
		State    string `json:"state"`
		Evidence string `json:"evidence"`
	} `json:"pickup"`
	Waiting struct {
		Obligation string `json:"obligation"`
		Owner      string `json:"owner"`
		State      string `json:"state"`
		DeadlineAt string `json:"deadlineAt"`
	} `json:"waiting"`
	ID            string `json:"qitemId"`
	CreatedAt     string `json:"tsCreated"`
	EvidenceRef   string `json:"evidenceRef"`
	Source        string `json:"sourceSession"`
	Destination   string `json:"destinationSession"`
	State         string `json:"state"`
	Priority      string `json:"priority"`
	Tier          string `json:"tier"`
	Summary       string `json:"summary"`
	Body          string `json:"body"`
	BlockedOn     string `json:"blockedOn"`
	ClosureReason string `json:"closureReason"`
}

func (w queueItemWire) item() (*QueueItem, error) {
	item := &QueueItem{
		ID:              w.ID,
		Source:          w.Source,
		Destination:     w.Destination,
		State:           w.State,
		PickupState:     w.Pickup.State,
		Priority:        w.Priority,
		Tier:            w.Tier,
		Summary:         w.Summary,
		Body:            w.Body,
		BlockedOn:       w.BlockedOn,
		ClosureReason:   w.ClosureReason,
		ObligationOwner: w.Waiting.Owner,
		ObligationState: w.Waiting.State,
		CreatedAt:       w.CreatedAt,
		DeadlineAt:      w.Waiting.DeadlineAt,
		EvidenceRef:     w.EvidenceRef,
	}
	for _, req := range []struct {
		field, value string
	}{
		{"qitem_id", item.ID},
		{"destination_session", item.Destination},
		{"state", item.State},
		{"pickup_state", item.PickupState},
	} {
		if req.value == "" {
			return nil, &FieldError{Command: "queue", Field: req.field}
		}
	}
	return item, nil
}

// QueueList reads every queue row, including history.
func (a *Adapter) QueueList(ctx context.Context) ([]QueueRow, error) {
	data, err := a.run(ctx, "queue", "list", "-A", "-a", "--json")
	if err != nil {
		return nil, err
	}
	return ParseQueueList(data, nil)
}

// QueueShow reads one row with its body and waiting block.
func (a *Adapter) QueueShow(ctx context.Context, id string) (*QueueItem, error) {
	if id == "" {
		return nil, fmt.Errorf("queue show: id is required")
	}
	data, err := a.run(ctx, "queue", "show", id, "--full", "--json")
	if err != nil {
		return nil, err
	}
	var wire queueItemWire
	if err := decode(data, &wire); err != nil {
		return nil, fmt.Errorf("queue show: %w", err)
	}
	return wire.item()
}

// CreateRequest is the create surface the dispatcher needs.
type CreateRequest struct {
	Destination string
	Summary     string
	BodyFile    string
	Source      string
	Priority    string
	Tags        []string
}

// QueueCreate creates a row and returns it.
func (a *Adapter) QueueCreate(ctx context.Context, req CreateRequest) (*QueueItem, error) {
	if req.Destination == "" || req.Summary == "" || req.BodyFile == "" {
		return nil, fmt.Errorf("queue create: destination, summary and body file are required")
	}
	args := []string{"queue", "create", "--destination", req.Destination, "--summary", req.Summary, "--body-file", req.BodyFile, "--json"}
	if req.Source != "" {
		args = append(args, "--source", req.Source)
	}
	if req.Priority != "" {
		args = append(args, "--priority", req.Priority)
	}
	if len(req.Tags) > 0 {
		args = append(args, "--tags", strings.Join(req.Tags, ","))
	}
	return a.writeItem(ctx, "queue create", args...)
}

// UpdateRequest is the mutation surface for one row.
type UpdateRequest struct {
	Note          string
	State         string
	ClosureReason string
	ClosureTarget string
	Resolution    string
}

// QueueUpdate appends a note and optionally moves the row.
func (a *Adapter) QueueUpdate(ctx context.Context, id string, req UpdateRequest) (*QueueItem, error) {
	if id == "" {
		return nil, fmt.Errorf("queue update: id is required")
	}
	args := []string{"queue", "update", id, "--json"}
	if req.Note != "" {
		args = append(args, "--note", req.Note)
	}
	if req.State != "" {
		args = append(args, "--state", req.State)
	}
	if req.ClosureReason != "" {
		args = append(args, "--closure-reason", req.ClosureReason)
	}
	if req.ClosureTarget != "" {
		args = append(args, "--closure-target", req.ClosureTarget)
	}
	if req.Resolution != "" {
		args = append(args, "--resolution", req.Resolution)
	}
	return a.writeItem(ctx, "queue update", args...)
}

// HandoffRequest is one transactional handoff.
type HandoffRequest struct {
	To       string
	Note     string
	Summary  string
	BodyFile string
}

// QueueHandoff closes the source row and creates the destination row.
func (a *Adapter) QueueHandoff(ctx context.Context, id string, req HandoffRequest) (*QueueItem, error) {
	if id == "" || req.To == "" {
		return nil, fmt.Errorf("queue handoff: id and destination are required")
	}
	args := []string{"queue", "handoff", id, "--to", req.To, "--json"}
	if req.Note != "" {
		args = append(args, "--note", req.Note)
	}
	if req.Summary != "" {
		args = append(args, "--summary", req.Summary)
	}
	if req.BodyFile != "" {
		args = append(args, "--body-file", req.BodyFile)
	}
	return a.writeItem(ctx, "queue handoff", args...)
}

// QueueResolve records a human decision on a parked row.
func (a *Adapter) QueueResolve(ctx context.Context, id, decision string) (*QueueItem, error) {
	if id == "" || decision == "" {
		return nil, fmt.Errorf("queue resolve: id and decision are required")
	}
	return a.writeItem(ctx, "queue resolve", "queue", "resolve", id, "--decision", decision, "--json")
}

func (a *Adapter) writeItem(ctx context.Context, command string, args ...string) (*QueueItem, error) {
	data, err := a.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		QitemID     string          `json:"qitemId"`
		Created     json.RawMessage `json:"created"`
		ClosedQitem json.RawMessage `json:"closedQitem"`
		Closed      json.RawMessage `json:"closed"`
	}
	_ = decode(data, &envelope)
	candidate := data
	switch {
	case len(envelope.Created) > 0:
		candidate = envelope.Created
	case len(envelope.ClosedQitem) > 0:
		candidate = envelope.ClosedQitem
	case len(envelope.Closed) > 0:
		candidate = envelope.Closed
	}
	var wire queueItemWire
	if err := decode(candidate, &wire); err != nil {
		return nil, fmt.Errorf("%s: %w", command, err)
	}
	return wire.item()
}

// Seats reads every node in every rig.
func (a *Adapter) Seats(ctx context.Context) ([]Seat, error) {
	data, err := a.run(ctx, "ps", "--nodes", "--full", "--json")
	if err != nil {
		return nil, err
	}
	return ParseSeats(data, nil)
}

// SeatsForRig reads the nodes of one rig.
func (a *Adapter) SeatsForRig(ctx context.Context, rig string) ([]Seat, error) {
	if rig == "" {
		return nil, fmt.Errorf("ps: rig is required")
	}
	data, err := a.run(ctx, "ps", "--nodes", "--full", "--json", "--rig", rig)
	if err != nil {
		return nil, err
	}
	return ParseSeats(data, nil)
}

// ParkedSeat is one seat's derived diagnosis.
type ParkedSeat struct {
	SeatNodeID  string `json:"seat_node_id"`
	SessionName string `json:"session_name"`
	Parked      bool   `json:"parked"`
	Reason      string `json:"reason"`
	OpenCount   int    `json:"open_count"`
	HeldCount   int    `json:"held_count"`
}

// ParkedRig is the rig-level diagnosis.
type ParkedRig struct {
	Parked bool         `json:"parked"`
	Reason string       `json:"reason"`
	Seats  []ParkedSeat `json:"seats"`
}

// ParkedReport is the full diagnosis.
type ParkedReport struct {
	OK  bool      `json:"ok"`
	Rig ParkedRig `json:"rig"`
}

// Parked runs the parked diagnosis for a rig.
func (a *Adapter) Parked(ctx context.Context, rig string) (*ParkedReport, error) {
	if rig == "" {
		return nil, fmt.Errorf("parked: rig is required")
	}
	data, err := a.run(ctx, "parked", "--rig", rig, "--json")
	if err != nil {
		return nil, err
	}
	var wire struct {
		OK  bool `json:"ok"`
		Rig struct {
			Parked bool   `json:"parked"`
			Reason string `json:"reason"`
			Seats  []struct {
				SeatNodeID  string `json:"seatNodeId"`
				SessionName string `json:"sessionName"`
				Parked      bool   `json:"parked"`
				Reason      string `json:"reason"`
				Obligations struct {
					OpenCount int `json:"openCount"`
					HeldCount int `json:"heldCount"`
				} `json:"obligations"`
			} `json:"seats"`
		} `json:"rig"`
	}
	if err := decode(data, &wire); err != nil {
		return nil, fmt.Errorf("parked: %w", err)
	}
	report := &ParkedReport{OK: wire.OK, Rig: ParkedRig{Parked: wire.Rig.Parked, Reason: wire.Rig.Reason}}
	for _, seat := range wire.Rig.Seats {
		report.Rig.Seats = append(report.Rig.Seats, ParkedSeat{
			SeatNodeID: seat.SeatNodeID, SessionName: seat.SessionName,
			Parked: seat.Parked, Reason: seat.Reason,
			OpenCount: seat.Obligations.OpenCount, HeldCount: seat.Obligations.HeldCount,
		})
	}
	return report, nil
}

// ProviderSignal is one provider/account reading.
type ProviderSignal struct {
	Provider             string  `json:"provider"`
	SeatSession          string  `json:"seat_session"`
	SourceClass          string  `json:"source_class"`
	Authority            string  `json:"authority"`
	Window               string  `json:"window"`
	UsedPercent          float64 `json:"used_percent"`
	AsOf                 string  `json:"as_of"`
	AutomationUse        string  `json:"automation_use"`
	SupportsNotification bool    `json:"supports_notification"`
}

// ProviderSignals reads the provider signal block.
func (a *Adapter) ProviderSignals(ctx context.Context) ([]ProviderSignal, error) {
	data, err := a.run(ctx, "provider", "signals", "--json")
	if err != nil {
		return nil, err
	}
	var wire struct {
		Signals []struct {
			Provider             string  `json:"provider"`
			SeatSession          string  `json:"seatSession"`
			SourceClass          string  `json:"sourceClass"`
			Authority            string  `json:"authority"`
			Window               string  `json:"window"`
			UsedPercent          float64 `json:"usedPercent"`
			AsOf                 string  `json:"asOf"`
			AutomationUse        string  `json:"automationUse"`
			SupportsNotification bool    `json:"supportsNotification"`
		} `json:"signals"`
	}
	if err := decode(data, &wire); err != nil {
		return nil, fmt.Errorf("provider signals: %w", err)
	}
	signals := make([]ProviderSignal, 0, len(wire.Signals))
	for _, s := range wire.Signals {
		signals = append(signals, ProviderSignal{
			Provider: s.Provider, SeatSession: s.SeatSession, SourceClass: s.SourceClass,
			Authority: s.Authority, Window: s.Window, UsedPercent: s.UsedPercent,
			AsOf: s.AsOf, AutomationUse: s.AutomationUse, SupportsNotification: s.SupportsNotification,
		})
	}
	return signals, nil
}

// Validation is the result of `rig spec validate`.
type Validation struct {
	Valid  bool     `json:"valid"`
	Errors []string `json:"errors"`
}

// SpecValidate validates a spec file. An invalid spec is a report, not a
// transport error; only unreadable output is an error.
func (a *Adapter) SpecValidate(ctx context.Context, path string) (*Validation, error) {
	if path == "" {
		return nil, fmt.Errorf("spec validate: path is required")
	}
	data, runErr := a.run(ctx, "spec", "validate", path, "--json")
	var validation Validation
	if err := decode(data, &validation); err != nil {
		if runErr != nil {
			return nil, runErr
		}
		return nil, fmt.Errorf("spec validate: %w", err)
	}
	return &validation, nil
}

// SpecEntry is one entry in the spec library.
type SpecEntry struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Version    string `json:"version"`
	SourceType string `json:"sourceType"`
}

// SpecsSync rescans the spec library.
func (a *Adapter) SpecsSync(ctx context.Context) ([]SpecEntry, error) {
	data, err := a.run(ctx, "specs", "sync", "--json")
	if err != nil {
		return nil, err
	}
	var wire []struct {
		ID         string `json:"id"`
		Kind       string `json:"kind"`
		Name       string `json:"name"`
		Version    string `json:"version"`
		SourceType string `json:"sourceType"`
	}
	if err := decode(data, &wire); err != nil {
		return nil, fmt.Errorf("specs sync: %w", err)
	}
	entries := make([]SpecEntry, 0, len(wire))
	for _, e := range wire {
		entries = append(entries, SpecEntry{ID: e.ID, Kind: e.Kind, Name: e.Name, Version: e.Version, SourceType: e.SourceType})
	}
	return entries, nil
}

// RunDetail carries the fields a run stage reports. The stage detail differs by
// stage; this is the typed subset the app reads.
type RunDetail struct {
	SpecName    string   `json:"specName"`
	SpecVersion string   `json:"specVersion"`
	RigName     string   `json:"rigName"`
	RigID       string   `json:"rigId"`
	Errors      []string `json:"errors"`
	Warnings    []string `json:"warnings"`
}

// RunStage is one stage of a launch or teardown.
type RunStage struct {
	Stage  string    `json:"stage"`
	Status string    `json:"status"`
	Detail RunDetail `json:"detail"`
}

// RunReport is the typed result of plan/up/down/grow. A failed run is still a
// report: callers check Status and Errors.
type RunReport struct {
	RunID    string     `json:"runId"`
	Status   string     `json:"status"`
	Errors   []string   `json:"errors"`
	Warnings []string   `json:"warnings"`
	Stages   []RunStage `json:"stages"`
}

func (a *Adapter) runReport(ctx context.Context, command string, args ...string) (*RunReport, error) {
	data, runErr := a.run(ctx, args...)
	var report RunReport
	if err := decode(data, &report); err != nil {
		if runErr != nil {
			return nil, runErr
		}
		return nil, fmt.Errorf("%s: %w", command, err)
	}
	return &report, nil
}

// Plan previews a launch without executing it.
func (a *Adapter) Plan(ctx context.Context, specPath string) (*RunReport, error) {
	if specPath == "" {
		return nil, fmt.Errorf("plan: spec path is required")
	}
	return a.runReport(ctx, "up --plan", "up", specPath, "--plan", "--json")
}

// Up launches a spec.
func (a *Adapter) Up(ctx context.Context, specPath string) (*RunReport, error) {
	if specPath == "" {
		return nil, fmt.Errorf("up: spec path is required")
	}
	return a.runReport(ctx, "up", "up", specPath, "--non-interruptive", "--json")
}

// DownReport is the daemon's teardown result.
type DownReport struct {
	SessionsKilled int      `json:"sessionsKilled"`
	Deleted        bool     `json:"deleted"`
	DeleteBlocked  bool     `json:"deleteBlocked"`
	AlreadyStopped bool     `json:"alreadyStopped"`
	SnapshotID     string   `json:"snapshotId"`
	RigName        string   `json:"rigName"`
	IsUniqueName   bool     `json:"isUniqueName"`
	Errors         []string `json:"errors"`
}

// Down stops and optionally deletes a rig.
func (a *Adapter) Down(ctx context.Context, rigName string, deleteRig bool) (*DownReport, error) {
	if rigName == "" {
		return nil, fmt.Errorf("down: rig is required")
	}
	args := []string{"down", rigName, "--json"}
	if deleteRig {
		args = append(args, "--delete")
	}
	data, runErr := a.run(ctx, args...)
	var report DownReport
	if err := decode(data, &report); err != nil {
		if runErr != nil {
			return nil, runErr
		}
		return nil, fmt.Errorf("down: %w", err)
	}
	return &report, nil
}

// GrowNode is one seat the grow call reported.
type GrowNode struct {
	LogicalID   string `json:"logicalId"`
	SessionName string `json:"sessionName"`
	Status      string `json:"status"`
	Error       string `json:"error"`
}

// GrowReport is the result of `rig grow --json`.
type GrowReport struct {
	OK       bool       `json:"ok"`
	RigID    string     `json:"rigId"`
	RigName  string     `json:"rigName"`
	Pod      string     `json:"pod"`
	Seats    []string   `json:"seats"`
	Source   string     `json:"source"`
	Seat     string     `json:"seat"`
	Nodes    []GrowNode `json:"nodes"`
	Warnings []string   `json:"warnings"`
	Detail   string     `json:"detail"`
}

// Grow adds a seat to a running rig.
func (a *Adapter) Grow(ctx context.Context, rigID, member, pod, cwd, runtime string) (*GrowReport, error) {
	if rigID == "" || member == "" {
		return nil, fmt.Errorf("grow: rig and member are required")
	}
	args := []string{"grow", rigID, member, "--json"}
	if pod != "" {
		args = append(args, "--pod", pod)
	}
	if cwd != "" {
		args = append(args, "--cwd", cwd)
	}
	if runtime != "" {
		args = append(args, "--runtime", runtime)
	}
	data, runErr := a.run(ctx, args...)
	var report GrowReport
	if err := decode(data, &report); err != nil {
		if runErr != nil {
			return nil, runErr
		}
		return nil, fmt.Errorf("grow: %w", err)
	}
	return &report, nil
}

// Export writes the running topology back to a spec file.
func (a *Adapter) Export(ctx context.Context, rigName, output string) error {
	if rigName == "" || output == "" {
		return fmt.Errorf("export: rig and output are required")
	}
	_, err := a.run(ctx, "export", rigName, "-o", output)
	return err
}

// Send delivers text to one seat's terminal.
func (a *Adapter) Send(ctx context.Context, session, text string) error {
	if session == "" {
		return fmt.Errorf("send: session is required")
	}
	_, err := a.run(ctx, "send", session, text)
	return err
}

// Capture reads the last lines of one seat's pane.
func (a *Adapter) Capture(ctx context.Context, session string, lines int) (string, error) {
	if session == "" {
		return "", fmt.Errorf("capture: session is required")
	}
	if lines <= 0 {
		lines = 20
	}
	data, err := a.run(ctx, "capture", session, "--lines", fmt.Sprint(lines))
	if err != nil {
		return "", err
	}
	return string(data), nil
}
