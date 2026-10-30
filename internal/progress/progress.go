// Package progress projects one tick into the plain-text progress message:
// the project record, every seat on the rig, the work buckets and, when the
// fleet is idle, the first rule that explains why.
//
// Build classifies what the tick read; Render formats it. Neither invents a
// fact: the renderer never reads the clock (now is an argument) and never
// returns an error, which is what makes the golden file stable and a missing
// roster one line instead of a failed send.
package progress

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/dinhtrung/fleet-console/internal/config"
	"github.com/dinhtrung/fleet-console/internal/decide"
	"github.com/dinhtrung/fleet-console/internal/forge"
	"github.com/dinhtrung/fleet-console/internal/git"
	"github.com/dinhtrung/fleet-console/internal/mirror"
	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// Snapshot is one report: the project record, the rig, the four work buckets
// and the true size of each bucket.
type Snapshot struct {
	TakenAt  time.Time
	Project  ProjectBlock // the whole row, field for field
	Rig      RigBlock     // name(s), every seat with its state, and why it is idle
	Epics    []Epic       // open epics with at least one open child
	InFlight []Item       // dispatch rows with no ended_at
	Pending  []Item       // open, not dispatched, not on hold, not an epic
	OnHold   []Item       // onhold label, or the project's own pause
	Counts   Counts       // true size of each bucket, even when a list is truncated
}

// ProjectBlock copies every field of the project the service reads. It is the
// row, not a summary.
type ProjectBlock struct {
	Key, Name, Forge, Repo, RepoURL, DefaultBranch string
	Rigs                                           []string
	Cap                                            int
	OnHold, Act                                    bool
	MainPath, WorktreePath                         string
}

// RigBlock is the rig header, every seat the roster returned and why an idle
// rig is idle. A failed roster read is RosterErr: one line, never a failed send.
type RigBlock struct {
	Names     []string
	Seats     []SeatLine // every seat the roster returned, in roster order
	Busy      int
	Total     int
	Idle      bool   // no item in flight and no seat working
	IdleWhy   string // one line, first matching rule in design §2a; "" when not idle
	RosterErr string // "roster unavailable: <why>" — a message, never a failed send
}

// SeatLine is one roster seat. IdleWhy is "" while the seat holds work.
type SeatLine struct {
	Session, LogicalID string
	Lifecycle          string // as the rig reports it: run, down, …
	LastActivity       string // as the rig reports it; "" when never seen
	Item               int    // the issue it holds, 0 when idle
	Title              string // the held issue's title
	Branch             string
	Age                string // how long on that item; "" when idle
	Since              time.Time
	IdleWhy            string // per-seat reason, "" when working
}

// Item is one entry in a work bucket. In-flight items carry the seat, branch
// and age; pending and on-hold items only the issue.
type Item struct {
	Number int
	Title  string
	Kind   string
	Seat   string
	Branch string
	Age    string
	Since  time.Time
}

// Epic is one open epic with at least one open child.
type Epic struct {
	Number int
	Title  string
	Done   int
	Total  int
}

// Counts is the true size of each bucket, independent of the rendered caps.
type Counts struct {
	InFlight, Pending, OnHold, Epics int
}

// Roster is the rig read plus the two tick facts the idle rules quote but the
// mirror read does not carry: the plan this tick produced (rule 8) and the
// decision id of an active quota pause (rule 3). A nil Seats falls back to the
// seats the mirror read for the project. Err is the roster read failure; Build
// reports it as one line and never returns an error.
type Roster struct {
	Seats      []rig.Seat
	Err        error
	Plan       *decide.Plan
	QuotaPause string
}

// Build projects one tick into a Snapshot. It performs no I/O and never fails:
// a project missing from the read, a missing config entry and a failed roster
// each render as data, not as an error.
func Build(read mirror.TickResult, rows []store.DispatchRow, project store.ProjectRow, eff *config.Effective, roster Roster) Snapshot {
	projectRead := read.Projects[project.Key]
	projectCfg := configProject(eff, project.Key)

	snapshot := Snapshot{TakenAt: read.At}
	snapshot.Project = projectBlock(project, projectCfg, eff)

	seats, rosterErr := rosterSeats(read, projectRead, roster)
	if len(snapshot.Project.Rigs) == 0 {
		snapshot.Project.Rigs = rigNames(seats)
	}
	snapshot.Project.Rigs = uniqueNames(snapshot.Project.Rigs)
	seats = seatsForRigs(seats, snapshot.Project.Rigs)

	items := make(map[int]forge.Item, len(projectRead.Items))
	for _, item := range projectRead.Items {
		items[item.Number] = item
	}

	inFlight, inFlightNumbers := inflightItems(rows, project, items)
	snapshot.InFlight = inFlight
	snapshot.Pending, snapshot.OnHold = openBuckets(projectRead.Items, inFlightNumbers, snapshot.Project.OnHold)
	snapshot.Epics = openEpics(projectRead.Items)

	rigBlock := RigBlock{
		Names:     append([]string(nil), snapshot.Project.Rigs...),
		RosterErr: rosterErr,
		Total:     len(seats),
	}
	work := dispatchWork(rows, project, items)
	working := 0
	for _, seat := range seats {
		line := SeatLine{
			Session:      seat.Session,
			LogicalID:    seat.LogicalID,
			Lifecycle:    seat.Lifecycle,
			LastActivity: seat.LastActivity,
		}
		// design §2a: the per-seat order is fixed. A dead seat explains itself
		// first, whatever work the tables still name it for.
		switch {
		case rosterErr != "":
			// A failed read has no states to classify; the rig line carries it.
		case !running(seat.Lifecycle):
			line.IdleWhy = "idle — seat " + lifecycleName(seat.Lifecycle)
		default:
			entry, held := workFor(seat, work)
			if !held {
				entry, held = queueWorkFor(seat, projectRead.Rows, items)
			}
			if held {
				line.Item, line.Title, line.Branch, line.Since = entry.item, entry.title, entry.branch, entry.since
				line.Age = ageString(read.At.Sub(entry.since))
				working++
				break
			}
			if isReviewer(seat) {
				cr := openDraft(projectRead.CRs)
				if cr == nil {
					line.IdleWhy = "idle — nothing to review"
					break
				}
				line.Item = cr.ClosesItem
				line.Title = itemTitle(items, cr.ClosesItem)
				line.Branch = branchFor(projectRead.Branches, cr.ClosesItem)
				working++
				break
			}
			if len(snapshot.Pending) > 0 {
				line.IdleWhy = "idle — waiting for a dispatch"
			} else {
				line.IdleWhy = "idle — nothing pending"
			}
		}
		rigBlock.Seats = append(rigBlock.Seats, line)
	}
	rigBlock.Busy = working

	snapshot.Rig = rigBlock
	snapshot.Counts = Counts{
		InFlight: len(snapshot.InFlight),
		Pending:  len(snapshot.Pending),
		OnHold:   len(snapshot.OnHold),
		Epics:    len(snapshot.Epics),
	}
	if snapshot.Rig.Idle = len(snapshot.InFlight) == 0 && snapshot.Rig.Busy == 0; snapshot.Rig.Idle {
		if why := idleReason(snapshot.Project, snapshot.Rig, projectRead, roster); why != "" {
			snapshot.Rig.IdleWhy = why
		} else {
			// no_free_builder means the fleet is busy, not idle.
			snapshot.Rig.Idle = false
		}
	}
	return snapshot
}

// projectBlock copies the project row and the config fields the row does not
// store (repo_url and both paths), then applies the one repo URL rule.
func projectBlock(project store.ProjectRow, cfg config.Project, eff *config.Effective) ProjectBlock {
	block := ProjectBlock{
		Key:           project.Key,
		Name:          project.Name,
		Forge:         project.ForgeKind,
		Repo:          project.ForgeProject,
		DefaultBranch: project.DefaultBranch,
		Rigs:          append([]string(nil), project.Rigs...),
		Cap:           project.ConcurrencyCap,
		OnHold:        project.OnHold,
		Act:           eff != nil && eff.Act,
		MainPath:      cfg.Paths.Main,
		WorktreePath:  cfg.Paths.Worktrees,
	}
	if block.Key == "" {
		block.Key = cfg.Key
	}
	if block.Name == "" {
		block.Name = cfg.Name
	}
	if block.Forge == "" {
		block.Forge = cfg.Forge
	}
	if block.Repo == "" {
		block.Repo = cfg.Repo
	}
	if block.DefaultBranch == "" {
		block.DefaultBranch = cfg.DefaultBranch
	}
	if len(block.Rigs) == 0 {
		block.Rigs = append([]string(nil), cfg.Rigs...)
	}
	block.Forge = strings.ToLower(strings.TrimSpace(block.Forge))
	block.RepoURL = repoURL(block.Forge, block.Repo, cfg.RepoURL)
	return block
}

// repoURL is design §3: repo_url verbatim, otherwise derived from the forge.
// A self-hosted GitLab cannot be derived; config.Load warns about that case.
func repoURL(forgeKind, repo, configured string) string {
	if value := strings.TrimSpace(configured); value != "" {
		return value
	}
	slug := strings.Trim(strings.TrimSpace(repo), "/")
	if slug == "" {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(forgeKind)) {
	case "github":
		return "https://github.com/" + slug
	case "gitlab":
		return "https://gitlab.com/" + slug
	default:
		return ""
	}
}

func configProject(eff *config.Effective, key string) config.Project {
	if eff == nil {
		return config.Project{}
	}
	for _, project := range eff.Projects {
		if project.Key == key {
			return project
		}
	}
	return config.Project{}
}

// rosterSeats prefers the caller's roster read, falls back to the seats the
// mirror read, and turns a failed read into its reason.
func rosterSeats(read mirror.TickResult, projectRead mirror.ProjectResult, roster Roster) ([]rig.Seat, string) {
	seats := roster.Seats
	if seats == nil {
		seats = projectRead.Seats
	}
	if roster.Err != nil {
		return seats, roster.Err.Error()
	}
	if len(seats) == 0 && read.RigStale && read.RigError != "" {
		return nil, read.RigError
	}
	return seats, ""
}

// seatsForRigs keeps the seats on the project's rigs in the project's rig
// order, as mirror.filterSeats does for the decide snapshot.
func seatsForRigs(seats []rig.Seat, rigs []string) []rig.Seat {
	if len(rigs) == 0 {
		return seats
	}
	order := make(map[string]int, len(rigs))
	for index, name := range rigs {
		order[name] = index
	}
	out := make([]rig.Seat, 0, len(seats))
	for _, seat := range seats {
		if _, ok := order[seat.Rig]; ok {
			out = append(out, seat)
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return order[out[a].Rig] < order[out[b].Rig] })
	return out
}

func rigNames(seats []rig.Seat) []string {
	var names []string
	for _, seat := range seats {
		if seat.Rig == "" {
			continue
		}
		names = append(names, seat.Rig)
	}
	return uniqueNames(names)
}

func uniqueNames(names []string) []string {
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

type workEntry struct {
	item   int
	title  string
	branch string
	since  time.Time
}

// dispatchWork indexes the app's open dispatch rows by seat.
func dispatchWork(rows []store.DispatchRow, project store.ProjectRow, items map[int]forge.Item) map[string]workEntry {
	work := make(map[string]workEntry)
	for _, row := range rows {
		if row.EndedAt != nil || row.Seat == "" {
			continue
		}
		if project.ID != 0 && row.ProjectID != 0 && row.ProjectID != project.ID {
			continue
		}
		since, _ := parseTime(row.CreatedAt)
		entry := work[row.Seat]
		if entry.item != 0 && !entry.since.IsZero() && !since.Before(entry.since) {
			continue
		}
		work[row.Seat] = workEntry{
			item: row.ItemNumber, title: itemTitle(items, row.ItemNumber),
			branch: row.Branch, since: since,
		}
	}
	return work
}

// inflightItems lists one entry per item with an ended_at-nil dispatch, oldest
// first. A duplicate attempt keeps the earliest row.
func inflightItems(rows []store.DispatchRow, project store.ProjectRow, items map[int]forge.Item) ([]Item, map[int]bool) {
	byItem := make(map[int]Item)
	numbers := make(map[int]bool)
	for _, row := range rows {
		if row.EndedAt != nil || row.ItemNumber == 0 {
			continue
		}
		if project.ID != 0 && row.ProjectID != 0 && row.ProjectID != project.ID {
			continue
		}
		since, _ := parseTime(row.CreatedAt)
		if existing, ok := byItem[row.ItemNumber]; ok {
			if !existing.Since.IsZero() && !since.Before(existing.Since) {
				continue
			}
		}
		byItem[row.ItemNumber] = Item{
			Number: row.ItemNumber, Title: itemTitle(items, row.ItemNumber),
			Kind: itemKind(items, row.ItemNumber), Seat: row.Seat, Branch: row.Branch,
			Since: since,
		}
		numbers[row.ItemNumber] = true
	}
	out := make([]Item, 0, len(byItem))
	for _, item := range byItem {
		out = append(out, item)
	}
	// Issue order, as design §2 prints the bucket; the age is a field, not a key.
	sort.Slice(out, func(a, b int) bool { return out[a].Number < out[b].Number })
	return out, numbers
}

// openBuckets splits open non-epic items into pending and on hold. An item in
// flight is in neither: it is already being built.
func openBuckets(items []forge.Item, inFlight map[int]bool, projectOnHold bool) (pending, onHold []Item) {
	byNumber := make(map[int]forge.Item, len(items))
	for _, item := range items {
		byNumber[item.Number] = item
	}
	for _, item := range items {
		if !strings.EqualFold(item.State, forge.StateOpen) || item.Kind == forge.KindEpic || inFlight[item.Number] {
			continue
		}
		entry := Item{Number: item.Number, Title: item.Title, Kind: string(item.Kind)}
		if projectOnHold || onHoldLabel(item, byNumber) {
			onHold = append(onHold, entry)
			continue
		}
		pending = append(pending, entry)
	}
	sort.Slice(pending, func(a, b int) bool { return pending[a].Number < pending[b].Number })
	sort.Slice(onHold, func(a, b int) bool { return onHold[a].Number < onHold[b].Number })
	return pending, onHold
}

func onHoldLabel(item forge.Item, byNumber map[int]forge.Item) bool {
	if hasLabel(item.Labels, "onhold") {
		return true
	}
	if item.Parent != nil {
		if parent, ok := byNumber[*item.Parent]; ok {
			return hasLabel(parent.Labels, "onhold")
		}
	}
	return false
}

func hasLabel(labels []string, want string) bool {
	for _, label := range labels {
		if strings.EqualFold(label, want) {
			return true
		}
	}
	return false
}

// openEpics lists open epics that still have an open child, with the child
// counts the message prints.
func openEpics(items []forge.Item) []Epic {
	var epics []Epic
	for _, epic := range items {
		if epic.Kind != forge.KindEpic || !strings.EqualFold(epic.State, forge.StateOpen) {
			continue
		}
		entry := Epic{Number: epic.Number, Title: epic.Title}
		for _, child := range items {
			if child.Parent == nil || *child.Parent != epic.Number {
				continue
			}
			entry.Total++
			if !strings.EqualFold(child.State, forge.StateOpen) {
				entry.Done++
			}
		}
		if entry.Total > 0 && entry.Done < entry.Total {
			epics = append(epics, entry)
		}
	}
	sort.Slice(epics, func(a, b int) bool { return epics[a].Number < epics[b].Number })
	return epics
}

// workFor matches a seat to its dispatch row by session, logical id or the
// session's short name, deterministically.
func workFor(seat rig.Seat, work map[string]workEntry) (workEntry, bool) {
	candidates := []string{seat.Session, seat.LogicalID, shortSession(seat.Session)}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if entry, ok := work[candidate]; ok {
			return entry, true
		}
	}
	keys := make([]string, 0, len(work))
	for key := range work {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, candidate := range candidates {
			if candidate != "" && strings.EqualFold(key, candidate) {
				return work[key], true
			}
		}
	}
	return workEntry{}, false
}

// queueWorkFor covers a live rig row the app has no dispatch row for: the rig
// is working even though the dispatch table is behind.
func queueWorkFor(seat rig.Seat, rows []rig.QueueRow, items map[int]forge.Item) (workEntry, bool) {
	for _, row := range rows {
		if !rowLive(row) || !seatMatches(seat, row.Destination) {
			continue
		}
		number := firstItemNumber(row.Summary, row.Body)
		if number == 0 {
			continue
		}
		since, _ := parseTime(row.CreatedAt)
		return workEntry{item: number, title: itemTitle(items, number), since: since}, true
	}
	return workEntry{}, false
}

func seatMatches(seat rig.Seat, name string) bool {
	if name == "" {
		return false
	}
	for _, candidate := range []string{seat.Session, seat.LogicalID, shortSession(seat.Session)} {
		if candidate != "" && strings.EqualFold(candidate, name) {
			return true
		}
	}
	return false
}

func rowLive(row rig.QueueRow) bool {
	state := strings.ToLower(strings.TrimSpace(row.PickupState))
	if state == "" {
		state = strings.ToLower(strings.TrimSpace(row.State))
	}
	switch state {
	case "", "terminal", "closed", "done", "cancelled", "canceled", "denied", "failed",
		"handed-off", "handed_off", "handedoff":
		return false
	}
	return true
}

var itemNumberPattern = regexp.MustCompile(`#(\d+)`)

func firstItemNumber(texts ...string) int {
	for _, text := range texts {
		if match := itemNumberPattern.FindStringSubmatch(text); len(match) == 2 {
			var number int
			fmt.Sscanf(match[1], "%d", &number)
			return number
		}
	}
	return 0
}

func itemNumbers(texts ...string) []int {
	seen := map[int]bool{}
	var out []int
	for _, text := range texts {
		for _, match := range itemNumberPattern.FindAllStringSubmatch(text, -1) {
			var number int
			fmt.Sscanf(match[1], "%d", &number)
			if number == 0 || seen[number] {
				continue
			}
			seen[number] = true
			out = append(out, number)
		}
	}
	sort.Ints(out)
	return out
}

func itemTitle(items map[int]forge.Item, number int) string {
	if item, ok := items[number]; ok {
		return item.Title
	}
	return ""
}

func itemKind(items map[int]forge.Item, number int) string {
	if item, ok := items[number]; ok {
		return string(item.Kind)
	}
	return ""
}

func branchFor(branches []git.Branch, item int) string {
	for _, branch := range branches {
		if branch.Item == item {
			return branch.Name
		}
	}
	return ""
}

func openDraft(crs []forge.ChangeRequest) *forge.ChangeRequest {
	var found *forge.ChangeRequest
	for index := range crs {
		cr := &crs[index]
		if cr.Merged || !strings.EqualFold(cr.State, forge.StateOpen) || !cr.Draft {
			continue
		}
		if found == nil || cr.Number < found.Number {
			found = cr
		}
	}
	return found
}

// isReviewer is a seat whose name says it reviews. The roster carries no role
// field, so the name is the only signal there is.
func isReviewer(seat rig.Seat) bool {
	label := strings.ToLower(seat.LogicalID + " " + seat.Session)
	return strings.Contains(label, "review")
}

func running(lifecycle string) bool {
	value := strings.ToLower(strings.TrimSpace(lifecycle))
	return value == "run" || value == "running"
}

func lifecycleName(lifecycle string) string {
	value := strings.TrimSpace(lifecycle)
	if value == "" {
		return "unknown"
	}
	return value
}

// idleReason is design §2a: the first matching rule, in order.
func idleReason(project ProjectBlock, block RigBlock, projectRead mirror.ProjectResult, roster Roster) string {
	switch {
	case !project.Act:
		return "idle — act is off: the board mirrors, nothing is dispatched"
	case project.OnHold:
		return "idle — project on hold"
	case roster.QuotaPause != "":
		return "idle — paused by a quota decision: " + roster.QuotaPause
	}
	if decisions := openDecisions(projectRead.Rows); len(decisions) > 0 {
		return fmt.Sprintf("idle — waiting on you: %d open decisions (%s)", len(decisions), decisionItems(decisions))
	}
	if open := openWork(projectRead.Items); open == 0 {
		return "idle — no open work: every issue is closed"
	}
	if block.RosterErr == "" && !anyRunning(block.Seats) {
		return fmt.Sprintf("idle — no seat running: %d seats reported %s", len(block.Seats), lifecycles(block.Seats))
	}
	if block.RosterErr != "" {
		return "idle — rig roster unavailable: " + block.RosterErr
	}
	// Rule 8 quotes the plan's own skip, never a reason invented here.
	if text, ok := firstSkipText(roster.Plan); ok {
		return "idle — nothing dispatchable: " + text
	}
	if busyFleet(roster.Plan) {
		// Every skip is no_free_builder: that is a busy fleet, not an idle one.
		return ""
	}
	return "idle — nothing dispatchable: the plan named no skip"
}

type decisionRow struct {
	summary string
	body    string
}

func openDecisions(rows []rig.QueueRow) []decisionRow {
	var out []decisionRow
	for _, row := range rows {
		state := strings.ToLower(strings.TrimSpace(row.State))
		if state != "pending" && state != "blocked" {
			continue
		}
		if !humanDestination(row.Destination) {
			continue
		}
		out = append(out, decisionRow{summary: row.Summary, body: row.Body})
	}
	return out
}

func decisionItems(rows []decisionRow) string {
	var numbers []int
	for _, row := range rows {
		numbers = append(numbers, itemNumbers(row.summary, row.body)...)
	}
	sort.Ints(numbers)
	unique := numbers[:0]
	for _, number := range numbers {
		if len(unique) == 0 || unique[len(unique)-1] != number {
			unique = append(unique, number)
		}
	}
	if len(unique) == 0 {
		return "no item"
	}
	parts := make([]string, 0, len(unique))
	for _, number := range unique {
		parts = append(parts, fmt.Sprintf("#%d", number))
	}
	return strings.Join(parts, ", ")
}

func humanDestination(destination string) bool {
	return strings.Contains(destination, "human@") || strings.HasSuffix(destination, "@external")
}

func openWork(items []forge.Item) int {
	count := 0
	for _, item := range items {
		if strings.EqualFold(item.State, forge.StateOpen) && item.Kind != forge.KindEpic {
			count++
		}
	}
	return count
}

func anyRunning(seats []SeatLine) bool {
	for _, seat := range seats {
		if running(seat.Lifecycle) {
			return true
		}
	}
	return false
}

func lifecycles(seats []SeatLine) string {
	seen := map[string]bool{}
	var values []string
	for _, seat := range seats {
		value := lifecycleName(seat.Lifecycle)
		if seen[value] {
			continue
		}
		seen[value] = true
		values = append(values, value)
	}
	if len(values) == 0 {
		return "none"
	}
	sort.Strings(values)
	return strings.Join(values, ", ")
}

// firstSkipText is the plan's own skip, rendered as the plan recorded it:
// `#70 parent_onhold: parent #64 carries onhold`. no_free_builder is not a
// candidate — it describes a busy fleet. ok is false when no skip is left.
func firstSkipText(plan *decide.Plan) (string, bool) {
	if plan == nil {
		return "", false
	}
	for _, skip := range plan.Skips {
		if skip.Reason == decide.ReasonNoFreeBuilder {
			continue
		}
		text := fmt.Sprintf("#%d %s", skip.Number, skip.Reason)
		if detail := strings.TrimSpace(skip.Detail); detail != "" {
			text += ": " + detail
		}
		return text, true
	}
	return "", false
}

// busyFleet is true when the plan skipped work only because every builder was
// taken: the rig is working, so it must not render as idle.
func busyFleet(plan *decide.Plan) bool {
	if plan == nil || len(plan.Skips) == 0 {
		return false
	}
	for _, skip := range plan.Skips {
		if skip.Reason != decide.ReasonNoFreeBuilder {
			return false
		}
	}
	return true
}

func parseTime(raw string) (time.Time, bool) {
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05Z"} {
		if at, err := time.Parse(layout, raw); err == nil {
			return at.UTC(), true
		}
	}
	return time.Time{}, false
}

func ageString(duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	switch {
	case duration < time.Minute:
		return "<1m"
	case duration < time.Hour:
		return fmt.Sprintf("%dm", int(duration/time.Minute))
	case duration < 24*time.Hour:
		hours := int(duration / time.Hour)
		minutes := int((duration % time.Hour) / time.Minute)
		if minutes == 0 {
			return fmt.Sprintf("%dh", hours)
		}
		return fmt.Sprintf("%dh%02dm", hours, minutes)
	default:
		days := int(duration / (24 * time.Hour))
		hours := int((duration % (24 * time.Hour)) / time.Hour)
		if hours == 0 {
			return fmt.Sprintf("%dd", days)
		}
		return fmt.Sprintf("%dd%02dh", days, hours)
	}
}

func shortSession(session string) string {
	if index := strings.Index(session, "@"); index > 0 {
		return session[:index]
	}
	return session
}
