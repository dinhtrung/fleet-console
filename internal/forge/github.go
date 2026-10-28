package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// GitHub reads and writes one repository through `gh api` and the gh CLI. REST is
// enough for every read; no GraphQL request is made.
type GitHub struct {
	slug string
	cli  *cli
}

// NewGitHub binds the adapter to a repository slug ("owner/repo") and a tick budget.
func NewGitHub(slug string, runner Runner, budget *Budget) *GitHub {
	if budget == nil {
		budget = &Budget{Max: 100}
	}
	return &GitHub{slug: slug, cli: &cli{name: "gh", runner: runner, budget: budget}}
}

// Kind reports the forge.
func (g *GitHub) Kind() Kind { return KindGitHub }

type ghIssue struct {
	Number           int       `json:"number"`
	State            string    `json:"state"`
	Title            string    `json:"title"`
	Body             string    `json:"body"`
	HTMLURL          string    `json:"html_url"`
	PullRequest      *struct{} `json:"pull_request,omitempty"`
	SubIssuesSummary *struct {
		Total int `json:"total"`
	} `json:"sub_issues_summary,omitempty"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

// Items reads open issues, their sub-issues (the epic relation) and their blocked_by
// dependencies.
func (g *GitHub) Items(ctx context.Context) ([]Item, error) {
	issues, err := g.openIssues(ctx)
	if err != nil {
		return nil, err
	}

	byNumber := make(map[int]*Item, len(issues))
	epics := make(map[int]bool)
	hasSummary := make(map[int]bool)
	var numbers []int
	for _, issue := range issues {
		if issue.PullRequest != nil {
			continue
		}
		labels := make([]string, len(issue.Labels))
		for index, label := range issue.Labels {
			labels[index] = label.Name
		}
		item := &Item{
			Number: issue.Number, Kind: KindTask, State: strings.ToLower(issue.State),
			Title: issue.Title, Body: issue.Body, Labels: labels, URL: issue.HTMLURL,
		}
		byNumber[issue.Number] = item
		numbers = append(numbers, issue.Number)
		if issue.SubIssuesSummary != nil {
			hasSummary[issue.Number] = true
			if issue.SubIssuesSummary.Total > 0 {
				epics[issue.Number] = true
			}
		}
	}

	// Sub-issues are the epic relation. A child named by its parent is a child even when
	// it is closed and therefore absent from the open list.
	for _, number := range numbers {
		if hasSummary[number] && !epics[number] {
			continue
		}
		children, err := g.subIssues(ctx, number)
		if err != nil {
			return nil, err
		}
		if len(children) == 0 {
			continue
		}
		epics[number] = true
		byNumber[number].Kind = KindEpic
		for _, child := range children {
			parent := number
			if item, ok := byNumber[child.Number]; ok {
				item.Parent = &parent
			}
		}
	}

	for _, number := range numbers {
		item := byNumber[number]
		if item.Kind == KindEpic {
			continue
		}
		blockers, err := g.blockedBy(ctx, number)
		if err != nil {
			return nil, err
		}
		item.BlockedBy = blockers
	}

	sort.Ints(numbers)
	items := make([]Item, 0, len(numbers))
	for _, number := range numbers {
		items = append(items, *byNumber[number])
	}
	return items, nil
}

func (g *GitHub) openIssues(ctx context.Context) ([]ghIssue, error) {
	var all []ghIssue
	for page := 1; ; page++ {
		path := fmt.Sprintf("repos/%s/issues?state=open&per_page=100&page=%d", g.slug, page)
		raw, err := g.cli.api(ctx, path)
		if err != nil {
			return nil, err
		}
		var issues []ghIssue
		if err := json.Unmarshal(raw, &issues); err != nil {
			return nil, fmt.Errorf("forge: decode GitHub issues: %w", err)
		}
		all = append(all, issues...)
		if len(issues) < 100 {
			break
		}
	}
	return all, nil
}

func (g *GitHub) subIssues(ctx context.Context, number int) ([]struct {
	Number int    `json:"number"`
	State  string `json:"state"`
}, error) {
	path := fmt.Sprintf("repos/%s/issues/%d/sub_issues", g.slug, number)
	raw, err := g.cli.api(ctx, path)
	if err != nil {
		return nil, err
	}
	var children []struct {
		Number int    `json:"number"`
		State  string `json:"state"`
	}
	if err := json.Unmarshal(raw, &children); err != nil {
		return nil, fmt.Errorf("forge: decode GitHub sub-issues: %w", err)
	}
	return children, nil
}

func (g *GitHub) blockedBy(ctx context.Context, number int) ([]Link, error) {
	path := fmt.Sprintf("repos/%s/issues/%d/dependencies/blocked_by", g.slug, number)
	raw, err := g.cli.api(ctx, path)
	if err != nil {
		return nil, err
	}
	var dependencies []struct {
		Number int    `json:"number"`
		State  string `json:"state"`
	}
	if err := json.Unmarshal(raw, &dependencies); err != nil {
		return nil, fmt.Errorf("forge: decode GitHub dependencies: %w", err)
	}
	links := make([]Link, 0, len(dependencies))
	for _, dependency := range dependencies {
		links = append(links, Link{Number: dependency.Number, State: strings.ToLower(dependency.State)})
	}
	return links, nil
}

type ghPull struct {
	Number    int     `json:"number"`
	Draft     bool    `json:"draft"`
	Body      string  `json:"body"`
	HTMLURL   string  `json:"html_url"`
	Mergeable *bool   `json:"mergeable"`
	State     string  `json:"state"`
	MergedAt  *string `json:"merged_at"`
	Head      struct {
		SHA string `json:"sha"`
	} `json:"head"`
}

// ChangeRequests reads open pull requests, their changed files and the head's checks.
func (g *GitHub) ChangeRequests(ctx context.Context) ([]ChangeRequest, error) {
	var pulls []ghPull
	for page := 1; ; page++ {
		path := fmt.Sprintf("repos/%s/pulls?state=all&per_page=100&page=%d", g.slug, page)
		raw, err := g.cli.api(ctx, path)
		if err != nil {
			return nil, err
		}
		var batch []ghPull
		if err := json.Unmarshal(raw, &batch); err != nil {
			return nil, fmt.Errorf("forge: decode GitHub pulls: %w", err)
		}
		pulls = append(pulls, batch...)
		if len(batch) < 100 {
			break
		}
	}

	requests := make([]ChangeRequest, 0, len(pulls))
	for _, pull := range pulls {
		paths, err := g.files(ctx, pull.Number)
		if err != nil {
			return nil, err
		}
		checks, err := g.checks(ctx, pull.Head.SHA)
		if err != nil {
			return nil, err
		}
		mergeable := pull.Mergeable != nil && *pull.Mergeable
		if pull.Mergeable == nil {
			// The list endpoint leaves mergeable null while GitHub computes it; one
			// single-PR read is the only place the merged gate may trust.
			raw, err := g.cli.api(ctx, fmt.Sprintf("repos/%s/pulls/%d", g.slug, pull.Number))
			if err != nil {
				return nil, err
			}
			var detail struct {
				Mergeable *bool `json:"mergeable"`
			}
			if err := json.Unmarshal(raw, &detail); err != nil {
				return nil, fmt.Errorf("forge: decode GitHub pull: %w", err)
			}
			mergeable = detail.Mergeable != nil && *detail.Mergeable
		}
		requests = append(requests, ChangeRequest{
			Number: pull.Number, State: pull.State, Merged: pull.MergedAt != nil,
			HeadSHA: pull.Head.SHA, Draft: pull.Draft,
			Mergeable: mergeable, Body: pull.Body, ClosesItem: ParseCloses(pull.Body),
			Checks: checks, DocsOnly: DocsOnly(paths), URL: pull.HTMLURL,
		})
	}
	sort.Slice(requests, func(a, b int) bool { return requests[a].Number < requests[b].Number })
	return requests, nil
}

func (g *GitHub) files(ctx context.Context, number int) ([]string, error) {
	path := fmt.Sprintf("repos/%s/pulls/%d/files?per_page=100", g.slug, number)
	raw, err := g.cli.api(ctx, path)
	if err != nil {
		return nil, err
	}
	var files []struct {
		Filename string `json:"filename"`
	}
	if err := json.Unmarshal(raw, &files); err != nil {
		return nil, fmt.Errorf("forge: decode GitHub pull files: %w", err)
	}
	paths := make([]string, len(files))
	for index, file := range files {
		paths[index] = file.Filename
	}
	return paths, nil
}

func (g *GitHub) checks(ctx context.Context, sha string) ([]Check, error) {
	var checks []Check
	raw, err := g.cli.api(ctx, fmt.Sprintf("repos/%s/commits/%s/check-runs", g.slug, sha))
	if err != nil {
		return nil, err
	}
	var runs struct {
		CheckRuns []struct {
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			DetailsURL string `json:"details_url"`
		} `json:"check_runs"`
	}
	if err := json.Unmarshal(raw, &runs); err != nil {
		return nil, fmt.Errorf("forge: decode GitHub check runs: %w", err)
	}
	for _, run := range runs.CheckRuns {
		checks = append(checks, Check{
			Name: run.Name, Status: run.Status, Conclusion: run.Conclusion,
			AllowFailure: false, URL: run.DetailsURL,
		})
	}

	raw, err = g.cli.api(ctx, fmt.Sprintf("repos/%s/commits/%s/status", g.slug, sha))
	if err != nil {
		return nil, err
	}
	var status struct {
		Statuses []struct {
			Context string `json:"context"`
			State   string `json:"state"`
			Target  string `json:"target_url"`
		} `json:"statuses"`
	}
	if err := json.Unmarshal(raw, &status); err != nil {
		return nil, fmt.Errorf("forge: decode GitHub commit status: %w", err)
	}
	for _, entry := range status.Statuses {
		conclusion := entry.State
		switch strings.ToLower(entry.State) {
		case "success":
			conclusion = "success"
		case "failure", "error":
			conclusion = "failure"
		case "pending":
			conclusion = "pending"
		}
		checks = append(checks, Check{
			Name: entry.Context, Status: "completed", Conclusion: conclusion, URL: entry.Target,
		})
	}
	return checks, nil
}

// Notes reads an item's comments (issue and PR conversation).
func (g *GitHub) Notes(ctx context.Context, number int) ([]Comment, error) {
	raw, err := g.cli.api(ctx, fmt.Sprintf("repos/%s/issues/%d/comments?per_page=100", g.slug, number))
	if err != nil {
		return nil, err
	}
	var batch []struct {
		ID        int64  `json:"id"`
		Body      string `json:"body"`
		CreatedAt string `json:"created_at"`
	}
	if err := json.Unmarshal(raw, &batch); err != nil {
		return nil, fmt.Errorf("forge: decode GitHub comments: %w", err)
	}
	out := make([]Comment, 0, len(batch))
	for _, note := range batch {
		out = append(out, Comment{ID: fmt.Sprint(note.ID), Body: note.Body, CreatedAt: note.CreatedAt})
	}
	return out, nil
}

// SetMarker adds or removes the in-flight marker label.
func (g *GitHub) SetMarker(ctx context.Context, number int, present bool) error {
	flag := "--remove-label"
	if present {
		flag = "--add-label"
	}
	_, err := g.cli.run(ctx, "issue", "edit", fmt.Sprint(number), flag, MarkerLabel)
	return err
}

// Comment posts one comment on an item.
func (g *GitHub) Comment(ctx context.Context, number int, body string) error {
	_, err := g.cli.run(ctx, "issue", "comment", fmt.Sprint(number), "--body", body)
	return err
}

// CloseItem closes an issue.
func (g *GitHub) CloseItem(ctx context.Context, number int) error {
	_, err := g.cli.run(ctx, "issue", "close", fmt.Sprint(number))
	return err
}

// Merge merges a pull request only if its head is still sha.
func (g *GitHub) Merge(ctx context.Context, number int, sha string) error {
	_, err := g.cli.run(ctx, "pr", "merge", fmt.Sprint(number), "--squash", "--match-head-commit", sha)
	return err
}
