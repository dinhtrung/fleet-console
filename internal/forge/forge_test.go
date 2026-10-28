package forge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fallbackFile struct {
	contains string
	file     string
}

type fixtureRunner struct {
	root     string
	exact    map[string]string
	fallback []fallbackFile
	stderr   map[string]string
	calls    []string
}

func (r *fixtureRunner) Run(_ context.Context, argv ...string) (string, string, error) {
	key := strings.Join(argv, " ")
	r.calls = append(r.calls, key)
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
	items, err := adapter.Items(context.Background())
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
	runner.exact["gh api repos/dinhtrung/fleet-console/pulls?state=open&per_page=100&page=1"] = "github/pulls-open.json"
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
	items, err := adapter.Items(context.Background())
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

func TestGitLabChangeRequests(t *testing.T) {
	runner := gitlabFixtures()
	runner.fallback = append(runner.fallback,
		fallbackFile{contains: "merge_requests?state=opened", file: "gitlab/merge-requests.json"},
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
	var merged *ChangeRequest
	for index := range requests {
		if requests[index].Number == 529 {
			merged = &requests[index]
		}
	}
	if merged == nil {
		t.Fatal("MR !529 missing")
	}
	if !merged.Mergeable {
		t.Fatal("MR !529 mergeable = false, want can_be_merged")
	}
	if merged.HeadSHA != "7170c3375aea1ee41f140ed3f3e4e8748679c2d6" {
		t.Fatalf("head = %q", merged.HeadSHA)
	}
	if merged.DocsOnly {
		t.Fatal("docs_only = true, want false (the recorded MR changes code)")
	}
	var allowed *Check
	for index := range merged.Checks {
		if merged.Checks[index].Name == "golangci-lint" {
			allowed = &merged.Checks[index]
		}
	}
	if allowed == nil || !allowed.AllowFailure {
		t.Fatalf("golangci-lint = %+v, want allow_failure", allowed)
	}
	if green, reason := merged.HeadGreen(); !green || reason != "all checks passed" {
		t.Fatalf("HeadGreen = %v %q, want green", green, reason)
	}
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

	ghCalls := []string{
		"gh issue edit 5 --add-label status::ongoing",
		"gh issue edit 5 --remove-label status::ongoing",
		"gh issue comment 5 --body hello",
		"gh issue close 5",
		"gh pr merge 14 --squash --match-head-commit abc123",
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
	if _, err := adapter.Items(context.Background()); !errors.Is(err, ErrBudget) {
		t.Fatalf("Items error = %v, want ErrBudget after two calls", err)
	}
}

func TestFakeSatisfiesTheDecideSeam(t *testing.T) {
	fake := NewFake(KindGitHub)
	fake.ItemsValue = []Item{{Number: 1, Kind: KindTask, State: StateOpen, Title: "one"}}
	fake.RequestsValue = []ChangeRequest{{Number: 2, HeadSHA: "abc", Checks: []Check{{Name: "go", Conclusion: "success"}}}}
	ctx := context.Background()
	items, err := fake.Items(ctx)
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
	if _, err := adapter.Items(context.Background()); !errors.Is(err, ErrStale) {
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
	if _, err := adapter.Items(context.Background()); err != nil {
		t.Fatalf("Items: %v", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("calls = %d, want one retry", len(runner.calls))
	}
}
