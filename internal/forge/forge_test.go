package forge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fallbackFile struct {
	contains string
	file     string
}

type fixtureRunner struct {
	mu       sync.Mutex
	root     string
	exact    map[string]string
	fallback []fallbackFile
	stderr   map[string]string
	calls    []string
}

func (r *fixtureRunner) Run(_ context.Context, argv ...string) (string, string, error) {
	key := strings.Join(argv, " ")
	r.mu.Lock()
	r.calls = append(r.calls, key)
	r.mu.Unlock()
	if message, ok := r.stderr[key]; ok {
		return "", message, errors.New("exit status 1")
	}
	if file, ok := r.exact[key]; ok {
		return readFixture(r.root, file), "", nil
	}
	for _, f := range r.fallback {
		if strings.Contains(key, f.contains) {
			return readFixture(r.root, f.file), "", nil
		}
	}
	return "[]", "", nil
}

func readFixture(root, name string) string {
	raw, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func githubFixtures() *fixtureRunner {
	return &fixtureRunner{
		root: "testdata",
		exact: map[string]string{
			"gh api repos/dinhtrung/fleet-console/issues?state=open&per_page=100&page=1": "github/issues.json",
			"gh api repos/dinhtrung/fleet-console/issues/1/sub_issues":                   "github/issue-1-sub-issues.json",
			"gh api repos/dinhtrung/fleet-console/issues/3/dependencies/blocked_by":      "github/issue-3-blocked-by.json",
			"gh api repos/dinhtrung/fleet-console/issues/6/dependencies/blocked_by":      "github/issue-6-blocked-by.json",
		},
	}
}

func TestGitHubItems(t *testing.T) {
	runner := githubFixtures()
	adapter := NewGitHub("dinhtrung/fleet-console", runner, &Budget{Max: 100})
	items, err := adapter.Items(context.Background(), Timeout{})
	if err != nil {
		t.Fatalf("Items: %v", err)
	}
	byNumber := map[int]Item{}
	for _, item := range items {
		byNumber[item.Number] = item
	}
	if len(items) != 8 {
		t.Fatalf("items = %d, want 8 open issues (the pull request is not an item)", len(items))
	}
	epic, ok := byNumber[1]
	if !ok || epic.Kind != KindEpic {
		t.Fatalf("#1 = %+v, want an epic", epic)
	}
	child, ok := byNumber[6]
	if !ok || child.Parent == nil || *child.Parent != 1 {
		t.Fatalf("#6 = %+v, want a child of #1", child)
	}
	if len(child.BlockedBy) != 2 || child.BlockedBy[0].Number != 3 || child.BlockedBy[0].State != "open" {
		t.Fatalf("#6 blocked_by = %+v, want #3 open and #5 open", child.BlockedBy)
	}
	if got := byNumber[3].BlockedBy; len(got) != 1 || got[0].Number != 2 || got[0].State != "closed" {
		t.Fatalf("#3 blocked_by = %+v, want #2 closed", got)
	}
	if _, ok := byNumber[14]; ok {
		t.Fatal("#14 is a pull request and must not be an item")
	}
}

func TestGitHubChangeRequests(t *testing.T) {
	runner := githubFixtures()
	runner.exact["gh api repos/dinhtrung/fleet-console/pulls?state=all&per_page=100&page=1"] = "github/pulls-open.json"
	runner.exact["gh api repos/dinhtrung/fleet-console/pulls/14"] = "github/pull-14.json"
	runner.exact["gh api repos/dinhtrung/fleet-console/pulls/14/files?per_page=100"] = "github/pull-files.json"
	runner.exact["gh api repos/dinhtrung/fleet-console/commits/a21e0626300f54f2c062bbb8026fe67bbdf47c3d/check-runs"] = "github/check-runs.json"
	runner.exact["gh api repos/dinhtrung/fleet-console/commits/a21e0626300f54f2c062bbb8026fe67bbdf47c3d/status"] = "github/commit-status.json"

	adapter := NewGitHub("dinhtrung/fleet-console", runner, &Budget{Max: 100})
	requests, err := adapter.ChangeRequests(context.Background())
	if err != nil {
		t.Fatalf("ChangeRequests: %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(requests))
	}
	cr := requests[0]
	if cr.Number != 14 || cr.HeadSHA != "a21e0626300f54f2c062bbb8026fe67bbdf47c3d" {
		t.Fatalf("cr = %+v, want PR #14 at its head", cr)
	}
	if cr.ClosesItem != 3 {
		t.Fatalf("closes = %d, want 3", cr.ClosesItem)
	}
	if cr.Draft {
		t.Fatal("draft = true, want false")
	}
	if cr.Mergeable {
		t.Fatal("mergeable = true, want false (the recorded head is dirty)")
	}
	if cr.DocsOnly {
		t.Fatal("docs_only = true, want false")
	}
	if len(cr.Checks) != 3 {
		t.Fatalf("checks = %+v, want go, web, openspec", cr.Checks)
	}
	if green, reason := cr.HeadGreen(); !green || reason != "all checks passed" {
		t.Fatalf("HeadGreen = %v %q, want green", green, reason)
	}
}

func gitlabFixtures() *fixtureRunner {
	return &fixtureRunner{
		root: "testdata",
		exact: map[string]string{
			"glab api projects/horisen%2Fmnp%2Fengine%2Ftraffic-simulator-v2/issues?state=opened&per_page=100&page=1": "gitlab/issues.json",
			"glab api projects/horisen%2Fmnp%2Fengine%2Ftraffic-simulator-v2/issues/109/links":                        "gitlab/issue-109-links.json",
			"glab api projects/horisen%2Fmnp%2Fengine%2Ftraffic-simulator-v2/issues/110/links":                        "gitlab/issue-110-links.json",
		},
		fallback: []fallbackFile{{contains: "graphql", file: "gitlab/work-items.json"}},
	}
}

func TestGitLabItems(t *testing.T) {
	runner := gitlabFixtures()
	adapter := NewGitLab("horisen/mnp/engine/traffic-simulator-v2", runner, &Budget{Max: 100})
	items, err := adapter.Items(context.Background(), Timeout{})
	if err != nil {
		t.Fatalf("Items: %v", err)
	}
	byNumber := map[int]Item{}
	for _, item := range items {
		byNumber[item.Number] = item
	}
	if len(items) != 4 {
		t.Fatalf("items = %d, want 4", len(items))
	}
	if byNumber[101].Kind != KindEpic || byNumber[102].Kind != KindEpic {
		t.Fatalf("#101/#102 = %+v/%+v, want epics", byNumber[101], byNumber[102])
	}
	if byNumber[110].Parent == nil || *byNumber[110].Parent != 111 {
		t.Fatalf("#110 parent = %v, want 111", byNumber[110].Parent)
	}
	if got := byNumber[110].Related; len(got) != 1 || got[0].Number != 109 || got[0].State != "closed" {
		t.Fatalf("#110 related = %+v, want #109 closed", got)
	}
	if byNumber[109].State != "closed" {
		t.Fatalf("#109 state = %q, want closed", byNumber[109].State)
	}
}

func TestGitLabChangeRequestsSkipsDetailsForMerged(t *testing.T) {
	runner := gitlabFixtures()
	runner.fallback = append(runner.fallback,
		fallbackFile{contains: "merge_requests?state=all", file: "gitlab/merge-requests.json"},
		fallbackFile{contains: "/changes", file: "gitlab/merge-request-changes.json"},
		fallbackFile{contains: "/jobs?", file: "gitlab/pipeline-jobs.json"},
		fallbackFile{contains: "/pipelines", file: "gitlab/pipelines.json"},
	)
	adapter := NewGitLab("horisen/mnp/engine/traffic-simulator-v2", runner, &Budget{Max: 100})
	requests, err := adapter.ChangeRequests(context.Background())
	if err != nil {
		t.Fatalf("ChangeRequests: %v", err)
	}
	if len(requests) != 5 {
		t.Fatalf("requests = %d, want the 5 recorded merge requests", len(requests))
	}
	if len(runner.calls) != 1 {
		t.Fatalf("calls = %v, want only the merge-request list", runner.calls)
	}
	var merged *ChangeRequest
	for index := range requests {
		if requests[index].Number == 529 {
			merged = &requests[index]
		}
	}
	if merged == nil {
		t.Fatal("MR !529 missing")
	}
	if !merged.Mergeable || merged.State != "merged" {
		t.Fatalf("MR !529 = %+v, want merged and mergeable", merged)
	}
	if merged.HeadSHA != "7170c3375aea1ee41f140ed3f3e4e8748679c2d6" {
		t.Fatalf("head = %q", merged.HeadSHA)
	}
	if merged.DocsOnly || merged.Checks != nil {
		t.Fatalf("merged MR details = docs_only %v checks %+v, want none", merged.DocsOnly, merged.Checks)
	}
}

func mrJSON(iids []int, candidate int) string {
	type mr struct {
		IID         int    `json:"iid"`
		State       string `json:"state"`
		SHA         string `json:"sha"`
		Description string `json:"description"`
	}
	batch := make([]mr, 0, len(iids))
	for _, iid := range iids {
		sha := fmt.Sprintf("sha%03d", iid)
		body := ""
		if iid == candidate {
			body = "Closes #7"
		}
		batch = append(batch, mr{IID: iid, State: "opened", SHA: sha, Description: body})
	}
	raw, err := json.Marshal(batch)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func TestGitLabChangeRequestsOnlyFetchCandidates(t *testing.T) {
	page := func(iids []int) []int { return iids }
	runner := &stubRunner{respond: func(_ int, argv []string) (string, string, error) {
		key := strings.Join(argv, " ")
		switch {
		case strings.Contains(key, "merge_requests?state=all") && strings.HasSuffix(key, "page=1"):
			return mrJSON(page(seq(1, 100)), 50), "", nil
		case strings.Contains(key, "merge_requests?state=all") && strings.HasSuffix(key, "page=2"):
			return mrJSON(page(seq(101, 200)), 50), "", nil
		case strings.Contains(key, "merge_requests?state=all") && strings.HasSuffix(key, "page=3"):
			return "[]", "", nil
		case strings.Contains(key, "merge_requests/50/changes"):
			return `{"changes":[{"new_path":"README.md"}]}`, "", nil
		case strings.Contains(key, "merge_requests/50/pipelines"):
			return `[{"id":9,"sha":"sha050"}]`, "", nil
		case strings.Contains(key, "pipelines/9/jobs"):
			return `[{"name":"test","status":"success"}]`, "", nil
		case strings.Contains(key, "/changes") || strings.Contains(key, "/pipelines"):
			t.Errorf("per-MR detail read for a non-candidate: %s", key)
			return "{}", "", nil
		}
		return "[]", "", nil
	}}
	adapter := NewGitLab("horisen/mnp/engine/traffic-simulator-v2", runner, &Budget{Max: 100})
	requests, err := adapter.ChangeRequestsFor(context.Background(), nil)
	if err != nil {
		t.Fatalf("ChangeRequestsFor: %v", err)
	}
	if len(requests) != 200 {
		t.Fatalf("requests = %d, want 200", len(requests))
	}
	t.Logf("glab calls for 200 merge requests with 1 candidate: %d", len(runner.calls))
	if len(runner.calls) > 15 {
		t.Fatalf("calls = %d, want <= 15: %v", len(runner.calls), runner.calls)
	}
	for _, request := range requests {
		if request.Number == 50 {
			if !request.DocsOnly || len(request.Checks) != 1 {
				t.Fatalf("candidate = %+v, want details", request)
			}
			continue
		}
		if request.Checks != nil {
			t.Fatalf("non-candidate %d has checks: %+v", request.Number, request.Checks)
		}
	}
}

func TestGitLabChangeRequestsAcceptedHeadIsACandidate(t *testing.T) {
	runner := &stubRunner{respond: func(_ int, argv []string) (string, string, error) {
		key := strings.Join(argv, " ")
		switch {
		case strings.Contains(key, "merge_requests?state=all"):
			return `[
				{"iid":1,"state":"opened","sha":"aaa","description":""},
				{"iid":2,"state":"opened","sha":"bbb","description":""}
			]`, "", nil
		case strings.Contains(key, "merge_requests/2/changes"):
			return `{"changes":[{"new_path":"main.go"}]}`, "", nil
		case strings.Contains(key, "merge_requests/2/pipelines"):
			return `[{"id":4,"sha":"bbb"}]`, "", nil
		case strings.Contains(key, "pipelines/4/jobs"):
			return `[{"name":"test","status":"success"}]`, "", nil
		case strings.Contains(key, "merge_requests/1/"):
			t.Errorf("MR 1 is not a candidate: %s", key)
		}
		return "[]", "", nil
	}}
	adapter := NewGitLab("horisen/mnp/engine/traffic-simulator-v2", runner, &Budget{Max: 100})
	requests, err := adapter.ChangeRequestsFor(context.Background(), map[string]struct{}{"bbb": {}})
	if err != nil {
		t.Fatalf("ChangeRequestsFor: %v", err)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(requests))
	}
	if requests[0].Checks != nil {
		t.Fatalf("unaccepted MR 1 has checks: %+v", requests[0].Checks)
	}
	if requests[1].Checks == nil || requests[1].DocsOnly {
		t.Fatalf("accepted MR 2 = %+v, want code checks", requests[1])
	}
}

func seq(from, to int) []int {
	out := make([]int, 0, to-from+1)
	for i := from; i <= to; i++ {
		out = append(out, i)
	}
	return out
}

type stubRunner struct {
	calls   []string
	respond func(call int, argv []string) (string, string, error)
}

func (r *stubRunner) Run(_ context.Context, argv ...string) (string, string, error) {
	r.calls = append(r.calls, strings.Join(argv, " "))
	if r.respond == nil {
		return "{}", "", nil
	}
	return r.respond(len(r.calls), argv)
}

func TestWritesAreExactlyTheFour(t *testing.T) {
	ctx := context.Background()
	github := NewGitHub("dinhtrung/fleet-console", &stubRunner{}, &Budget{Max: 100})
	if err := github.SetMarker(ctx, 5, true); err != nil {
		t.Fatal(err)
	}
	if err := github.SetMarker(ctx, 5, false); err != nil {
		t.Fatal(err)
	}
	if err := github.Comment(ctx, 5, "hello"); err != nil {
		t.Fatal(err)
	}
	if err := github.CloseItem(ctx, 5); err != nil {
		t.Fatal(err)
	}
	if err := github.Ready(ctx, 14); err != nil {
		t.Fatal(err)
	}
	if err := github.Merge(ctx, 14, "abc123"); err != nil {
		t.Fatal(err)
	}

	gitlab := NewGitLab("horisen/mnp/engine/traffic-simulator-v2", &stubRunner{}, &Budget{Max: 100})
	if err := gitlab.SetMarker(ctx, 5, true); err != nil {
		t.Fatal(err)
	}
	if err := gitlab.SetMarker(ctx, 5, false); err != nil {
		t.Fatal(err)
	}
	if err := gitlab.Comment(ctx, 5, "hello"); err != nil {
		t.Fatal(err)
	}
	if err := gitlab.CloseItem(ctx, 5); err != nil {
		t.Fatal(err)
	}
	if err := gitlab.Merge(ctx, 529, "abc123"); err != nil {
		t.Fatal(err)
	}

	// Every verb carries -R: the service runs in $HOME, where `gh` resolves a
	// repository from the working directory and a verb without it fails with
	// "not a git repository" (the #112 failure). `gh api` is exempt.
	ghCalls := []string{
		"gh issue edit 5 --add-label status::ongoing -R dinhtrung/fleet-console",
		"gh issue edit 5 --remove-label status::ongoing -R dinhtrung/fleet-console",
		"gh issue comment 5 --body hello -R dinhtrung/fleet-console",
		"gh issue close 5 -R dinhtrung/fleet-console",
		"gh pr ready 14 -R dinhtrung/fleet-console",
		"gh pr merge 14 --squash --match-head-commit abc123 -R dinhtrung/fleet-console",
	}
	glCalls := []string{
		"glab api -X PUT projects/horisen%2Fmnp%2Fengine%2Ftraffic-simulator-v2/issues/5 -f add_labels=status::ongoing",
		"glab api -X PUT projects/horisen%2Fmnp%2Fengine%2Ftraffic-simulator-v2/issues/5 -f remove_labels=status::ongoing",
		"glab api -X POST projects/horisen%2Fmnp%2Fengine%2Ftraffic-simulator-v2/issues/5/notes -f body=hello",
		"glab api -X PUT projects/horisen%2Fmnp%2Fengine%2Ftraffic-simulator-v2/issues/5 -f state_event=close",
		"glab mr merge 529 --sha abc123 --yes --repo horisen/mnp/engine/traffic-simulator-v2",
	}
	if got, want := strings.Join(github.cli.runner.(*stubRunner).calls, "\n"), strings.Join(ghCalls, "\n"); got != want {
		t.Fatalf("GitHub writes:\n%s\nwant:\n%s", got, want)
	}
	if got, want := strings.Join(gitlab.cli.runner.(*stubRunner).calls, "\n"), strings.Join(glCalls, "\n"); got != want {
		t.Fatalf("GitLab writes:\n%s\nwant:\n%s", got, want)
	}
}

func TestHeadGreenRules(t *testing.T) {
	cases := []struct {
		name   string
		cr     ChangeRequest
		green  bool
		reason string
	}{
		{"red check", ChangeRequest{Checks: []Check{{Name: "go", Conclusion: "failure"}}}, false, "check go failed"},
		{"allowed failure", ChangeRequest{Checks: []Check{{Name: "lint", Conclusion: "failure", AllowFailure: true}}}, true, "all checks passed"},
		{"pending", ChangeRequest{Checks: []Check{{Name: "go", Status: "in_progress"}}}, false, "check go pending"},
		{"docs-only no checks", ChangeRequest{DocsOnly: true}, true, "docs-only: no checks"},
		{"no checks", ChangeRequest{}, false, "no checks"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			green, reason := test.cr.HeadGreen()
			if green != test.green || reason != test.reason {
				t.Fatalf("HeadGreen = %v %q, want %v %q", green, reason, test.green, test.reason)
			}
		})
	}
}

const twoTaskIssues = `[{"number":2,"state":"open","title":"two","sub_issues_summary":{"total":0}},
{"number":3,"state":"open","title":"three","sub_issues_summary":{"total":0}}]`

func TestBudget(t *testing.T) {
	budget := &Budget{Max: 100}
	for call := 1; call <= 100; call++ {
		if err := budget.Take(); err != nil {
			t.Fatalf("call %d: %v", call, err)
		}
	}
	if err := budget.Take(); !errors.Is(err, ErrBudget) {
		t.Fatalf("the 101st call error = %v, want ErrBudget", err)
	}

	runner := &stubRunner{respond: func(call int, argv []string) (string, string, error) {
		if strings.Contains(strings.Join(argv, " "), "/issues?") {
			return twoTaskIssues, "", nil
		}
		return "[]", "", nil
	}}
	adapter := NewGitHub("dinhtrung/fleet-console", runner, &Budget{Max: 2})
	if _, err := adapter.Items(context.Background(), Timeout{}); !errors.Is(err, ErrBudget) {
		t.Fatalf("Items error = %v, want ErrBudget after two calls", err)
	}
}

func TestFakeSatisfiesTheDecideSeam(t *testing.T) {
	fake := NewFake(KindGitHub)
	fake.ItemsValue = []Item{{Number: 1, Kind: KindTask, State: StateOpen, Title: "one"}}
	fake.RequestsValue = []ChangeRequest{{Number: 2, HeadSHA: "abc", Checks: []Check{{Name: "go", Conclusion: "success"}}}}
	ctx := context.Background()
	items, err := fake.Items(ctx, Timeout{})
	if err != nil || len(items) != 1 || items[0].Number != 1 {
		t.Fatalf("Items = %+v, %v", items, err)
	}
	if err := fake.SetMarker(ctx, 1, true); err != nil {
		t.Fatal(err)
	}
	if err := fake.Comment(ctx, 1, "note"); err != nil {
		t.Fatal(err)
	}
	if err := fake.CloseItem(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := fake.Merge(ctx, 2, "abc"); err != nil {
		t.Fatal(err)
	}
	if len(fake.Writes) != 4 {
		t.Fatalf("writes = %+v, want the four", fake.Writes)
	}
}

func TestStaleOn401(t *testing.T) {
	runner := &stubRunner{respond: func(int, []string) (string, string, error) {
		return "", "HTTP 401: Bad credentials", errors.New("exit status 1")
	}}
	adapter := NewGitHub("dinhtrung/fleet-console", runner, &Budget{Max: 100})
	if _, err := adapter.Items(context.Background(), Timeout{}); !errors.Is(err, ErrStale) {
		t.Fatalf("Items error = %v, want ErrStale", err)
	}
}

func TestRetryOn5xx(t *testing.T) {
	runner := &stubRunner{respond: func(call int, argv []string) (string, string, error) {
		if call == 1 {
			return "", "HTTP 502: Bad Gateway", errors.New("exit status 1")
		}
		return "[]", "", nil
	}}
	adapter := NewGitHub("dinhtrung/fleet-console", runner, &Budget{Max: 100})
	if _, err := adapter.Items(context.Background(), Timeout{}); err != nil {
		t.Fatalf("Items: %v", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("calls = %d, want one retry", len(runner.calls))
	}
}

func TestForbiddenNamesTheOperation(t *testing.T) {
	runner := &stubRunner{respond: func(int, []string) (string, string, error) {
		return "", "gh: You must be an org admin or have the actions policies fine-grained permission. (HTTP 403)", errors.New("exit status 1")
	}}
	adapter := NewGitHub("dinhtrung/fleet-console", runner, &Budget{Max: 100})
	_, err := adapter.Items(context.Background(), Timeout{})
	t.Logf("classified: %v (errors.Is ErrForbidden=%v, ErrStale=%v)", err, errors.Is(err, ErrForbidden), errors.Is(err, ErrStale))
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("error = %v, want ErrForbidden", err)
	}
	if errors.Is(err, ErrStale) {
		t.Fatalf("403 also matched ErrStale: %v", err)
	}
	var forbidden *ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("error = %T, want *ForbiddenError", err)
	}
	if !strings.Contains(forbidden.Operation, "gh api repos/dinhtrung/fleet-console/issues") {
		t.Fatalf("operation = %q, want the API path", forbidden.Operation)
	}
	if !strings.Contains(err.Error(), "org admin") {
		t.Fatalf("error = %q, want the forge's permission message", err)
	}
}

func TestRateLimitIsStaleNotForbidden(t *testing.T) {
	runner := &stubRunner{respond: func(int, []string) (string, string, error) {
		return "", "gh: API rate limit exceeded for user ID 1. (HTTP 403)", errors.New("exit status 1")
	}}
	adapter := NewGitHub("dinhtrung/fleet-console", runner, &Budget{Max: 100})
	_, err := adapter.Items(context.Background(), Timeout{})
	if !errors.Is(err, ErrStale) {
		t.Fatalf("error = %v, want ErrStale", err)
	}
	if errors.Is(err, ErrForbidden) {
		t.Fatalf("a rate limit read as forbidden: %v", err)
	}
}

type slowRunner struct {
	issues string
	delay  time.Duration
}

func (r slowRunner) Run(ctx context.Context, argv ...string) (string, string, error) {
	key := strings.Join(argv, " ")
	if strings.Contains(key, "/dependencies/blocked_by") {
		select {
		case <-time.After(r.delay):
			return "[]", "", nil
		case <-ctx.Done():
			return "", "", ctx.Err()
		}
	}
	return r.issues, "", nil
}

func manyIssues(count int) string {
	var b strings.Builder
	b.WriteString("[")
	for i := 1; i <= count; i++ {
		if i > 1 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"number":%d,"state":"open","title":"T%d","body":"","labels":[],`+
			`"sub_issues_summary":{"total":0},"html_url":"u"}`, i, i)
	}
	b.WriteString("]")
	return b.String()
}

// TestParallelItemReadsFitDeadline: 40 items at 100ms per item call fit a 2s
// deadline only because the per-item reads are parallel (8 workers).
func TestParallelItemReadsFitDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	adapter := NewGitHub("o/r", slowRunner{issues: manyIssues(40), delay: 100 * time.Millisecond}, &Budget{Max: 100})
	start := time.Now()
	items, err := adapter.Items(ctx, Timeout{})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Items: %v after %s", err, elapsed)
	}
	if len(items) != 40 {
		t.Fatalf("items = %d, want 40", len(items))
	}
	if elapsed >= 2*time.Second {
		t.Fatalf("Items took %s; parallelism did not fit the deadline", elapsed)
	}
	t.Logf("40 items in %s (parallelism %d)", elapsed.Round(time.Millisecond), Parallelism)
}

// TestItemReadDeadlineReportsProgress: an expiring deadline says how many items
// were read and which item it stopped at.
func TestItemReadDeadlineReportsProgress(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	adapter := NewGitHub("o/r", slowRunner{issues: manyIssues(40), delay: 100 * time.Millisecond}, &Budget{Max: 100})
	_, err := adapter.Items(ctx, Timeout{})
	if err == nil {
		t.Fatal("Items returned nil, want a deadline error")
	}
	message := err.Error()
	if !strings.Contains(message, "of 40 items") || !strings.Contains(message, "stopped at #") {
		t.Fatalf("error = %q, want the count and the item", message)
	}
	t.Logf("deadline error: %v", err)
}

// TestGitHubSubIssuesKeepsClosedChildrenAndDefaultsUnknownToOpen covers the two
// things the sweep's evidence depends on. Items drops closed children from the
// open list, so SubIssues must keep them; and a child whose state the payload
// omits must NOT read as closed, because closed is the direction that closes an
// epic over work nobody looked at. The fixture carries both: the real sub-issues
// payload has closed children, and a child with no state at all is folded in by
// the runner below.
func TestGitHubSubIssuesKeepsClosedChildrenAndDefaultsUnknownToOpen(t *testing.T) {
	runner := githubFixtures()
	runner.exact["gh api repos/dinhtrung/fleet-console/issues/1/sub_issues"] = "github/issue-1-sub-issues.json"
	adapter := NewGitHub("dinhtrung/fleet-console", runner, &Budget{Max: 100})

	children, err := adapter.SubIssues(context.Background(), 1)
	if err != nil {
		t.Fatalf("SubIssues: %v", err)
	}
	if len(children) != 10 {
		t.Fatalf("children = %d, want the fixture's 10 (closed ones included)", len(children))
	}
	for _, child := range children {
		if child.Parent == nil || *child.Parent != 1 {
			t.Fatalf("child #%d parent = %v, want 1 so the sweep can count it", child.Number, child.Parent)
		}
	}
	var closed, open int
	for _, child := range children {
		switch child.State {
		case StateClosed:
			closed++
		case StateOpen:
			open++
		default:
			t.Fatalf("child #%d state = %q, want open or closed", child.Number, child.State)
		}
	}
	if closed == 0 {
		t.Fatalf("no closed children came back; the fixture has three, so Items' dropping must be undone")
	}
	if open == 0 {
		t.Fatalf("no open children came back")
	}
}

// TestGitHubSubIssuesTreatsAnAbsentStateAsOpen is the guard against the permissive
// default: an absent state must leave the epic open, never close it.
func TestGitHubSubIssuesTreatsAnAbsentStateAsOpen(t *testing.T) {
	runner := githubFixtures()
	// A payload whose children carry no state field at all.
	runner.fallback = append([]fallbackFile{{contains: "/sub_issues", file: "github/sub-issues-no-state.json"}}, runner.fallback...)
	runner.exact = map[string]string{}
	adapter := NewGitHub("dinhtrung/fleet-console", runner, &Budget{Max: 100})

	children, err := adapter.SubIssues(context.Background(), 7)
	if err != nil {
		t.Fatalf("SubIssues: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("children = %d, want 2", len(children))
	}
	for _, child := range children {
		if child.State != StateOpen {
			t.Fatalf("child #%d state = %q, want open for an absent payload state", child.Number, child.State)
		}
	}
}

// TestGitLabSubIssuesKeepsClosedChildrenAndDefaultsUnknownToOpen is the same pair
// for GitLab. Its only hierarchy fixture has no state on any child, so before this
// test the adapter's decode was proven by nothing and its zero value fell through
// to "closed" — the direction that closes an epic over unreported work.
func TestGitLabSubIssuesKeepsClosedChildrenAndDefaultsUnknownToOpen(t *testing.T) {
	runner := &fixtureRunner{root: "testdata", fallback: []fallbackFile{
		{contains: "graphql", file: "gitlab/sub-issues.json"},
	}}
	adapter := NewGitLab("horisen/mnp/engine/traffic-simulator-v2", runner, &Budget{Max: 100})

	children, err := adapter.SubIssues(context.Background(), 101)
	if err != nil {
		t.Fatalf("SubIssues: %v", err)
	}
	if len(children) != 3 {
		t.Fatalf("children = %d, want 3", len(children))
	}
	byNumber := map[int]Item{}
	for _, child := range children {
		byNumber[child.Number] = child
	}
	if byNumber[91].State != StateClosed {
		t.Fatalf("#91 state = %q, want closed (payload says CLOSED)", byNumber[91].State)
	}
	if byNumber[90].State != StateOpen {
		t.Fatalf("#90 state = %q, want open (payload says OPENED)", byNumber[90].State)
	}
	// #89 carries no state in the payload: it must read as open, not closed.
	if byNumber[89].State != StateOpen {
		t.Fatalf("#89 state = %q, want open for an absent payload state", byNumber[89].State)
	}
	for _, child := range children {
		if child.Parent == nil || *child.Parent != 101 {
			t.Fatalf("child #%d parent = %v, want 101", child.Number, child.Parent)
		}
	}
}
