// Package forge adapters map one normalized Item/ChangeRequest model onto GitHub and
// GitLab.
//
// The decide loop never reads a forge: it consumes this model. Credentials come from
// the gh/glab CLI stores only — the app never reads, stores or logs a token. Reads are
// bounded by a per-tick request budget; a 401, a rate limit or a 5xx surfaces as
// ErrStale so the project is marked stale rather than rendered as an empty board,
// while a 403 is a distinct ErrForbidden naming the operation. The only mutations
// are the four writes on Forge.
package forge

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Kind is the forge a project is bound to. A project is never a mix of the two.
type Kind string

const (
	KindGitHub Kind = "github"
	KindGitLab Kind = "gitlab"
)

// ItemKind is the normalized work-item type: an item with children is an epic.
type ItemKind string

const (
	KindEpic ItemKind = "epic"
	KindTask ItemKind = "task"
)

// State is the forge's item state.
const (
	StateOpen   = "open"
	StateClosed = "closed"
)

var (
	// ErrStale means the forge could not be read reliably (401, 5xx, budget). The
	// caller marks the project stale; it must not read as "no tasks".
	ErrStale = errors.New("forge read is stale")
	// ErrBudget means this tick used its request budget.
	ErrBudget = errors.New("forge request budget exhausted")
	// ErrForbidden means the credential is valid but not allowed: a missing
	// scope or a protected branch. Retrying the same call cannot succeed.
	ErrForbidden = errors.New("forge operation forbidden")
)

// ForbiddenError names the operation the credential may not perform. The board
// shows its Error() text, so the operator sees the missing scope, not "stale".
type ForbiddenError struct {
	Operation string
	Detail    string
}

func (e *ForbiddenError) Error() string {
	if e.Detail == "" {
		return "forge: forbidden: " + e.Operation
	}
	return "forge: forbidden: " + e.Operation + ": " + e.Detail
}

// Is lets errors.Is(err, ErrForbidden) work without unwrapping a sentinel.
func (e *ForbiddenError) Is(target error) bool { return target == ErrForbidden }

// Link is one blocking/related edge with the other item's state.
type Link struct {
	Number int    `json:"number"`
	State  string `json:"state"`
}

// Item is the normalized mirror row.
type Item struct {
	Number    int      `json:"number"`
	Kind      ItemKind `json:"kind"`
	State     string   `json:"state"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Labels    []string `json:"labels"`
	Parent    *int     `json:"parent,omitempty"`
	BlockedBy []Link   `json:"blocked_by,omitempty"` // GitHub dependencies (directional)
	Related   []Link   `json:"related,omitempty"`    // GitLab relates_to (symmetric)
	URL       string   `json:"url"`
}

// Check is one head check (a GitHub check run / commit status, a GitLab job).
type Check struct {
	Name         string `json:"name"`
	Status       string `json:"status"`
	Conclusion   string `json:"conclusion"`
	AllowFailure bool   `json:"allow_failure"`
	URL          string `json:"url,omitempty"`
}

// Failed reports a non-allowed failure. A pending/running check is not a failure: it
// is simply not green yet.
func (c Check) Failed() bool {
	if c.AllowFailure {
		return false
	}
	switch strings.ToLower(c.Conclusion) {
	case "failure", "failed", "error", "timed_out", "cancelled", "canceled", "action_required":
		return true
	}
	return false
}

// Pending reports a check that has not finished.
func (c Check) Pending() bool {
	status := strings.ToLower(c.Status)
	if status == "" {
		return false
	}
	return status != "completed" && status != "success" && status != "failed" && status != "canceled" &&
		status != "cancelled"
}

// ChangeRequest is the normalized pull/merge request.
type ChangeRequest struct {
	Number     int     `json:"number"`
	State      string  `json:"state"`  // open | closed | merged
	Merged     bool    `json:"merged"` // the change landed
	HeadSHA    string  `json:"head_sha"`
	Draft      bool    `json:"draft"`
	Mergeable  bool    `json:"mergeable"`
	Body       string  `json:"body"`
	ClosesItem int     `json:"closes_item,omitempty"`
	Checks     []Check `json:"checks"`
	DocsOnly   bool    `json:"docs_only"`
	URL        string  `json:"url"`
}

// HeadGreen answers the merge gate's head question and says why. A docs-only change
// request with no checks is green because there is nothing to run, and the reason names
// that. A failing check marked allow-failure is not red.
func (cr ChangeRequest) HeadGreen() (bool, string) {
	if len(cr.Checks) == 0 {
		if cr.DocsOnly {
			return true, "docs-only: no checks"
		}
		return false, "no checks"
	}
	for _, check := range cr.Checks {
		if check.Failed() {
			return false, fmt.Sprintf("check %s failed", check.Name)
		}
		if check.Pending() {
			return false, fmt.Sprintf("check %s pending", check.Name)
		}
	}
	return true, "all checks passed"
}

// Comment is one reviewer note on an item.
type Comment struct {
	ID        string `json:"id"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

// CandidateReader is implemented by a forge that can skip per-change details for
// changes that cannot matter this tick. accepted holds the head SHAs of ACCEPT
// verdicts; a change is a candidate when it is open and either closes an item or
// its head sha is in accepted.
type CandidateReader interface {
	ChangeRequestsFor(ctx context.Context, accepted map[string]struct{}) ([]ChangeRequest, error)
}

// Forge is the whole interface the app has to a forge. Exactly four writes: the marker
// label, a comment, closing an item, and merging a change request at a sha.
type Forge interface {
	Kind() Kind
	Items(ctx context.Context, timeout Timeout) ([]Item, error)
	ChangeRequests(ctx context.Context) ([]ChangeRequest, error)
	Notes(ctx context.Context, number int) ([]Comment, error)
	SetMarker(ctx context.Context, number int, present bool) error
	Comment(ctx context.Context, number int, body string) error
	CloseItem(ctx context.Context, number int) error
	Merge(ctx context.Context, number int, sha string) error
}

// Timeout scales one project's read deadline with the work in it:
// base + per_item*items, capped at ceiling. The item count is only known after the
// issues list, so Items applies it to the per-item phase itself.
type Timeout struct {
	Base, PerItem, Ceiling time.Duration
}

const (
	DefaultTimeoutBase    = 30 * time.Second
	DefaultTimeoutPerItem = 2 * time.Second
	DefaultTimeoutCeiling = 2 * time.Minute
)

func (t Timeout) resolved() Timeout {
	if t.Base <= 0 {
		t.Base = DefaultTimeoutBase
	}
	if t.Ceiling <= 0 {
		t.Ceiling = DefaultTimeoutCeiling
	}
	if t.Ceiling < t.Base {
		t.Ceiling = t.Base
	}
	return t
}

// List is the deadline for the issue list (one paginated read).
func (t Timeout) List() time.Duration { return t.resolved().Base }

// For is the deadline for the per-item reads of a project with items items.
func (t Timeout) For(items int) time.Duration {
	t = t.resolved()
	limit := t.Base + time.Duration(items)*t.PerItem
	if limit > t.Ceiling {
		return t.Ceiling
	}
	return limit
}

// Hard is the hard ceiling on one project's read.
func (t Timeout) Hard() time.Duration { return t.resolved().Ceiling }

// Runner executes one CLI command. It is the only seam between an adapter and the
// forge: the adapters use gh/glab, which own the credentials.
type Runner interface {
	Run(ctx context.Context, argv ...string) (stdout, stderr string, err error)
}

// Budget bounds the forge requests in one tick. It counts reads and writes alike;
// the per-item reads are parallel, so Take is serialized.
type Budget struct {
	mu   sync.Mutex
	Max  int
	Used int
}

// Take records one request or refuses it.
func (b *Budget) Take() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.Max > 0 && b.Used >= b.Max {
		return ErrBudget
	}
	if b.Max > 0 {
		b.Used++
	}
	return nil
}

var (
	closesPattern = regexp.MustCompile(`(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\s+#(\d+)\b`)
	docsPattern   = regexp.MustCompile(`(?i)^(docs/|readme|license|changelog|contributing|code_of_conduct)`)
)

// ParseCloses extracts the item number a change request closes, if the body says so.
func ParseCloses(body string) int {
	match := closesPattern.FindStringSubmatch(body)
	if match == nil {
		return 0
	}
	number, err := strconv.Atoi(match[1])
	if err != nil {
		return 0
	}
	return number
}

// DocsOnly reports whether every changed path is documentation.
func DocsOnly(paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	for _, path := range paths {
		lower := strings.ToLower(path)
		if !(strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".mdx") ||
			strings.HasSuffix(lower, ".rst") || strings.HasSuffix(lower, ".txt") ||
			docsPattern.MatchString(lower)) {
			return false
		}
	}
	return true
}
