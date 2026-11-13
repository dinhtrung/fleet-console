package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
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
// dependencies. The per-item calls run through a small worker pool: with one CLI
// process per item, a serial loop cannot fit a large repository into a tick.
func (g *GitHub) Items(ctx context.Context, timeout Timeout) ([]Item, error) {
	listCtx, cancelList := context.WithTimeout(ctx, timeout.List())
	issues, err := g.openIssues(listCtx)
	cancelList()
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
		if hasEpicLabel(labels) {
			// The label is the only fact that says "container" before the first
			// child is linked; the summary and the child list catch it later.
			item.Kind = KindEpic
			epics[issue.Number] = true
		}
		if issue.SubIssuesSummary != nil {
			hasSummary[issue.Number] = true
			if issue.SubIssuesSummary.Total > 0 {
				epics[issue.Number] = true
			}
		}
	}
	total := len(numbers)
	var completed atomic.Int64
	itemsCtx, cancelItems := context.WithTimeout(ctx, timeout.For(total))
	defer cancelItems()

	// Phase 1: sub-issues are the epic relation. A child named by its parent is a
	// child even when it is closed and therefore absent from the open list.
	var fetch []int
	for index, number := range numbers {
		if hasSummary[number] && !epics[number] {
			continue
		}
		fetch = append(fetch, index)
	}
	children := make([][]ghChild, len(numbers))
	if failed, err := parallelEach(itemsCtx, len(fetch), Parallelism, func(ctx context.Context, slot int) error {
		index := fetch[slot]
		result, err := g.subIssues(ctx, numbers[index])
		if err != nil {
			return err
		}
		children[index] = result
		if len(result) > 0 {
			completed.Add(1) // an epic is fully read here; its deps are not read
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("read %d of %d items, stopped at #%d: %w",
			completed.Load(), total, numbers[fetch[failed]], err)
	}
	for _, index := range fetch {
		number := numbers[index]
		if len(children[index]) == 0 {
			continue
		}
		epics[number] = true
		byNumber[number].Kind = KindEpic
		for _, entry := range children[index] {
			parent := number
			if item, ok := byNumber[entry.Number]; ok {
				item.Parent = &parent
			}
		}
	}

	// Phase 2: blocked_by for every non-epic item.
	var tasks []int
	for index, number := range numbers {
		if epics[number] {
			continue
		}
		tasks = append(tasks, index)
	}
	links := make([][]Link, len(numbers))
	if failed, err := parallelEach(itemsCtx, len(tasks), Parallelism, func(ctx context.Context, slot int) error {
		index := tasks[slot]
		result, err := g.blockedBy(ctx, numbers[index])
		if err != nil {
			return err
		}
		links[index] = result
		completed.Add(1)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("read %d of %d items, stopped at #%d: %w",
			completed.Load(), total, numbers[tasks[failed]], err)
	}
	for _, index := range tasks {
		byNumber[numbers[index]].BlockedBy = links[index]
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

type ghChild struct {
	Number int    `json:"number"`
	State  string `json:"state"`
}

// SubIssues reads one epic's children, open and closed both. Items calls
// subIssues for every open epic already, but keeps a child only when the child is
// itself in the open list (github.go:120), so the closed children — the whole
// point of the sweep's evidence — are dropped there. This is the same request,
// exposed with the states kept.
//
// The children come back as Item rows whose Parent is the epic, so a caller
// counts them with the rule the snapshot uses: an item whose Parent is not this
// epic is not its child. A child's own Kind stays task; the hierarchy is carried
// by Parent, not by Kind.
func (g *GitHub) SubIssues(ctx context.Context, number int) ([]Item, error) {
	children, err := g.subIssues(ctx, number)
	if err != nil {
		return nil, err
	}
	parent := number
	items := make([]Item, 0, len(children))
	for _, child := range children {
		// An absent or unrecognised state is not evidence of a close. GitHub's REST
		// payload always carries the field, but a partial payload or a future state
		// name must not read as "closed": closing an epic over a child whose state
		// nobody reported is the failure this whole read exists to avoid. An unknown
		// state reads as open, which leaves the epic open and costs nothing.
		state := strings.ToLower(child.State)
		if state != StateOpen && state != StateClosed {
			state = StateOpen
		}
		items = append(items, Item{
			Number: child.Number, Kind: KindTask, State: state,
			Parent: &parent,
		})
	}
	return items, nil
}

func hasEpicLabel(labels []string) bool {
	for _, label := range labels {
		if label == EpicLabel {
			return true
		}
	}
	return false
}

func (g *GitHub) subIssues(ctx context.Context, number int) ([]ghChild, error) {
	path := fmt.Sprintf("repos/%s/issues/%d/sub_issues", g.slug, number)
	raw, err := g.cli.api(ctx, path)
	if err != nil {
		return nil, err
	}
	var children []ghChild
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
	Number     int     `json:"number"`
	Title      string  `json:"title"`
	Draft      bool    `json:"draft"`
	Body       string  `json:"body"`
	HTMLURL    string  `json:"html_url"`
	Mergeable  *bool   `json:"mergeable"`
	MergeState string  `json:"mergeable_state"`
	State      string  `json:"state"`
	MergedAt   *string `json:"merged_at"`
	Head       struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
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
		if pull.State != "open" || pull.MergedAt != nil {
			// A closed or merged pull request can never merge again: its files, its
			// checks and its mergeable flag are dead weight the tick must not pay for.
			requests = append(requests, ChangeRequest{
				Number: pull.Number, State: pull.State, Merged: pull.MergedAt != nil,
				HeadSHA: pull.Head.SHA, Branch: pull.Head.Ref, Draft: pull.Draft, Body: pull.Body,
				Title:      pull.Title,
				ClosesItem: ParseCloses(pull.Body), URL: pull.HTMLURL,
			})
			continue
		}
		paths, err := g.files(ctx, pull.Number)
		if err != nil {
			return nil, err
		}
		checks, err := g.checks(ctx, pull.Head.SHA)
		if err != nil {
			return nil, err
		}
		mergeable := pull.Mergeable != nil && *pull.Mergeable
		// The list page's mergeable_state rides along with the same snapshot
		// mergeable came from; it is only trustworthy when that snapshot
		// answered, so it is the default and the refetch replaces it.
		state := pull.MergeState
		if pull.Mergeable == nil {
			// The list endpoint leaves mergeable null while GitHub computes it; one
			// single-PR read is the only place the merged gate may trust.
			raw, err := g.cli.api(ctx, fmt.Sprintf("repos/%s/pulls/%d", g.slug, pull.Number))
			if err != nil {
				return nil, err
			}
			var detail struct {
				Mergeable  *bool  `json:"mergeable"`
				MergeState string `json:"mergeable_state"`
			}
			if err := json.Unmarshal(raw, &detail); err != nil {
				return nil, fmt.Errorf("forge: decode GitHub pull: %w", err)
			}
			mergeable = detail.Mergeable != nil && *detail.Mergeable
			// The refetch is the read that computed mergeability, so its word
			// is the one the gate may quote (epic #341, design D1) — the list
			// page's `unknown` would otherwise outlive the computation.
			state = detail.MergeState
		}
		requests = append(requests, ChangeRequest{
			Number: pull.Number, State: pull.State, Merged: pull.MergedAt != nil,
			HeadSHA: pull.Head.SHA, Branch: pull.Head.Ref, Draft: pull.Draft,
			Mergeable: mergeable, MergeState: state, Title: pull.Title,
			Body: pull.Body, ClosesItem: ParseCloses(pull.Body),
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
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if err := json.Unmarshal(raw, &batch); err != nil {
		return nil, fmt.Errorf("forge: decode GitHub comments: %w", err)
	}
	out := make([]Comment, 0, len(batch))
	for _, note := range batch {
		out = append(out, Comment{
			ID: fmt.Sprint(note.ID), Body: note.Body, CreatedAt: note.CreatedAt, Author: note.User.Login,
		})
	}
	return out, nil
}

// CRNotes reads the conversation on a pull request. GitHub keeps pull-request
// comments in the same issue-comment stream as an issue's, but under the pull
// request's own number — and that number is not the number of the issue the
// pull request closes. Reading Notes(itemNumber) for a verdict therefore reads
// the wrong conversation and finds nothing.
func (g *GitHub) CRNotes(ctx context.Context, number int) ([]Comment, error) {
	return g.Notes(ctx, number)
}

// githubPermissionRefusal reports whether a GitHub write refusal is a permission
// fact rather than a transient condition (#258). The sentence classes:
//
//   - HTTP 403 the token caused — the anonymous fine-grained PAT answer;
//   - "Resource not accessible by personal access token" — the same refusal in
//     GitHub's own words, without the status;
//   - "must have push access" — a valid credential below the write floor;
//   - a 404 on a write — a fine-grained token without access to the repository
//     answers 404, which is a permission fact there, not a missing target.
//
// The 404 class exists on writes only: a read 404 stays an ordinary failure,
// because not-found reads are legitimate answers about missing targets.
func githubPermissionRefusal(stderr string, err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(stderr + " " + err.Error())
	switch {
	case strings.Contains(message, "http 403"),
		strings.Contains(message, "resource not accessible by personal access token"),
		strings.Contains(message, "must have push access"),
		strings.Contains(message, "http 404"),
		strings.Contains(message, "404 not found"):
		return true
	}
	return false
}

// SetMarker adds or removes the in-flight marker label.
//
// -R is not optional on any `gh` verb: the service runs in $HOME, where the CLI
// resolves a repository from the working directory and dies with
// "failed to run git: ... not a git repository". `gh api` carries the slug in its
// path and needs none of this.
func (g *GitHub) SetMarker(ctx context.Context, number int, present bool) error {
	flag := "--remove-label"
	if present {
		flag = "--add-label"
	}
	_, err := g.cli.write(ctx, "label", "issue", "edit", fmt.Sprint(number), flag, MarkerLabel, "-R", g.slug)
	return err
}

// Comment posts one comment on an item.
func (g *GitHub) Comment(ctx context.Context, number int, body string) error {
	_, err := g.cli.write(ctx, "comment", "issue", "comment", fmt.Sprint(number), "--body", body, "-R", g.slug)
	return err
}

// CloseItem closes an issue.
func (g *GitHub) CloseItem(ctx context.Context, number int) error {
	_, err := g.cli.write(ctx, "close", "issue", "close", fmt.Sprint(number), "-R", g.slug)
	return err
}

// Ready takes a pull request out of draft.
func (g *GitHub) Ready(ctx context.Context, number int) error {
	_, err := g.cli.write(ctx, "ready", "pr", "ready", fmt.Sprint(number), "-R", g.slug)
	return err
}

// Merge merges a pull request only if its head is still sha.
func (g *GitHub) Merge(ctx context.Context, number int, sha string) error {
	// -R is not optional: the service runs in $HOME where `gh pr merge` finds no
	// repository and dies with "failed to run git: not a git repository". Every
	// `gh api` call carries the slug in its path, so only the verbs needed it.
	_, err := g.cli.write(ctx, "merge", "pr", "merge", fmt.Sprint(number), "--squash",
		"--match-head-commit", sha, "-R", g.slug)
	return err
}
