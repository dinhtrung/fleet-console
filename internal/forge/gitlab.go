package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
)

// GitLab reads and writes one project through `glab api` and the glab CLI.
type GitLab struct {
	project string
	enc     string
	cli     *cli
}

// NewGitLab binds the adapter to a project path ("group/project") and a tick budget.
func NewGitLab(project string, runner Runner, budget *Budget) *GitLab {
	if budget == nil {
		budget = &Budget{Max: 100}
	}
	return &GitLab{
		project: project,
		enc:     url.PathEscape(project),
		cli:     &cli{name: "glab", runner: runner, budget: budget},
	}
}

// Kind reports the forge.
func (g *GitLab) Kind() Kind { return KindGitLab }

type glIssue struct {
	IID         int      `json:"iid"`
	State       string   `json:"state"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Labels      []string `json:"labels"`
	WebURL      string   `json:"web_url"`
}

// Items reads open issues, their work-item hierarchy (the epic relation) and their
// related links. Hierarchy is the one GraphQL read: the REST issue payload has no
// parent/children fields, so parity with GitHub's sub-issues is not expressible in
// REST.
func (g *GitLab) Items(ctx context.Context, timeout Timeout) ([]Item, error) {
	listCtx, cancelList := context.WithTimeout(ctx, timeout.List())
	issues, err := g.openIssues(listCtx)
	cancelList()
	if err != nil {
		return nil, err
	}

	byNumber := make(map[int]*Item, len(issues))
	var numbers []int
	for _, issue := range issues {
		state := "open"
		if !strings.EqualFold(issue.State, "opened") {
			state = "closed"
		}
		byNumber[issue.IID] = &Item{
			Number: issue.IID, Kind: KindTask, State: state, Title: issue.Title,
			Body: issue.Description, Labels: issue.Labels, URL: issue.WebURL,
		}
		numbers = append(numbers, issue.IID)
	}

	itemsCtx, cancelItems := context.WithTimeout(ctx, timeout.For(len(numbers)))
	defer cancelItems()
	hierarchy, err := g.workItems(itemsCtx, numbers)
	if err != nil {
		return nil, err
	}
	for _, node := range hierarchy.Nodes {
		number := atoi(node.IID)
		item, ok := byNumber[number]
		if !ok {
			continue
		}
		for _, widget := range node.Widgets {
			if widget.Type != "HIERARCHY" {
				continue
			}
			if widget.Parent != nil {
				parent := atoi(widget.Parent.IID)
				if parent > 0 {
					item.Parent = &parent
				}
			}
			if widget.Children != nil && len(widget.Children.Nodes) > 0 {
				item.Kind = KindEpic
				for _, child := range widget.Children.Nodes {
					childNumber := atoi(child.IID)
					if childItem, ok := byNumber[childNumber]; ok {
						parent := number
						childItem.Parent = &parent
					}
				}
			}
		}
	}

	// Per-item related links run through the same small worker pool as the
	// GitHub dependencies: one CLI process per item, so the wall time is O(items/8).
	total := len(numbers)
	var completed atomic.Int64
	var tasks []int
	for index, number := range numbers {
		if byNumber[number].Kind == KindEpic {
			continue
		}
		tasks = append(tasks, index)
	}
	links := make([][]Link, len(numbers))
	if failed, err := parallelEach(itemsCtx, len(tasks), Parallelism, func(ctx context.Context, slot int) error {
		index := tasks[slot]
		result, err := g.related(ctx, numbers[index])
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
		byNumber[numbers[index]].Related = links[index]
	}

	sort.Ints(numbers)
	items := make([]Item, 0, len(numbers))
	for _, number := range numbers {
		items = append(items, *byNumber[number])
	}
	return items, nil
}

func (g *GitLab) openIssues(ctx context.Context) ([]glIssue, error) {
	var all []glIssue
	for page := 1; ; page++ {
		path := fmt.Sprintf("projects/%s/issues?state=opened&per_page=100&page=%d", g.enc, page)
		raw, err := g.cli.api(ctx, path)
		if err != nil {
			return nil, err
		}
		var issues []glIssue
		if err := json.Unmarshal(raw, &issues); err != nil {
			return nil, fmt.Errorf("forge: decode GitLab issues: %w", err)
		}
		all = append(all, issues...)
		if len(issues) < 100 {
			break
		}
	}
	return all, nil
}

type glWorkItemNode struct {
	IID   string `json:"iid"`
	State string `json:"state"`
	Type  struct {
		Name string `json:"name"`
	} `json:"workItemType"`
	Widgets []struct {
		Type   string `json:"type"`
		Parent *struct {
			IID string `json:"iid"`
		} `json:"parent"`
		Children *struct {
			Nodes []struct {
				IID string `json:"iid"`
			} `json:"nodes"`
		} `json:"children"`
	} `json:"widgets"`
}

func (g *GitLab) workItems(ctx context.Context, numbers []int) (struct {
	Nodes []glWorkItemNode `json:"nodes"`
}, error) {
	var out struct {
		Nodes []glWorkItemNode `json:"nodes"`
	}
	if len(numbers) == 0 {
		return out, nil
	}
	quoted := make([]string, len(numbers))
	for index, number := range numbers {
		quoted[index] = fmt.Sprintf("%q", fmt.Sprint(number))
	}
	query := fmt.Sprintf(`query { project(fullPath: %q) { workItems(iids: [%s]) {
		nodes { iid state workItemType { name }
			widgets { type
				... on WorkItemWidgetHierarchy {
					parent { iid }
					children(first: 100) { nodes { iid } }
				}
			}
		}
	} } }`, g.project, strings.Join(quoted, ","))
	raw, err := g.cli.run(ctx, "api", "graphql", "-f", "query="+query)
	if err != nil {
		return out, err
	}
	var response struct {
		Data struct {
			Project struct {
				WorkItems struct {
					Nodes []glWorkItemNode `json:"nodes"`
				} `json:"workItems"`
			} `json:"project"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return out, fmt.Errorf("forge: decode GitLab work items: %w", err)
	}
	if len(response.Errors) > 0 {
		return out, fmt.Errorf("forge: GitLab hierarchy: %s", response.Errors[0].Message)
	}
	out.Nodes = response.Data.Project.WorkItems.Nodes
	return out, nil
}

func (g *GitLab) related(ctx context.Context, number int) ([]Link, error) {
	path := fmt.Sprintf("projects/%s/issues/%d/links", g.enc, number)
	raw, err := g.cli.api(ctx, path)
	if err != nil {
		return nil, err
	}
	var links []struct {
		IID   int    `json:"iid"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(raw, &links); err != nil {
		return nil, fmt.Errorf("forge: decode GitLab issue links: %w", err)
	}
	out := make([]Link, 0, len(links))
	for _, link := range links {
		state := "open"
		if !strings.EqualFold(link.State, "opened") {
			state = "closed"
		}
		out = append(out, Link{Number: link.IID, State: state})
	}
	return out, nil
}

type glMergeRequest struct {
	IID         int    `json:"iid"`
	State       string `json:"state"`
	SHA         string `json:"sha"`
	Draft       bool   `json:"draft"`
	MergeStatus string `json:"merge_status"`
	Description string `json:"description"`
	WebURL      string `json:"web_url"`
}

// ChangeRequests reads every merge request without candidate filtering.
func (g *GitLab) ChangeRequests(ctx context.Context) ([]ChangeRequest, error) {
	return g.ChangeRequestsFor(ctx, nil)
}

// ChangeRequestsFor lists merge requests page by page and fetches changed files
// and pipeline jobs only for candidates: open merge requests that close an item
// or whose head sha carries an ACCEPT verdict. Everything else stays metadata.
func (g *GitLab) ChangeRequestsFor(ctx context.Context, accepted map[string]struct{}) ([]ChangeRequest, error) {
	var requests []ChangeRequest
	for page := 1; ; page++ {
		path := fmt.Sprintf("projects/%s/merge_requests?state=all&per_page=100&page=%d", g.enc, page)
		raw, err := g.cli.api(ctx, path)
		if err != nil {
			return nil, err
		}
		var batch []glMergeRequest
		if err := json.Unmarshal(raw, &batch); err != nil {
			return nil, fmt.Errorf("forge: decode GitLab merge requests: %w", err)
		}
		for _, mr := range batch {
			request := ChangeRequest{
				Number: mr.IID, State: normalizeMRState(mr.State), Merged: strings.EqualFold(mr.State, "merged"),
				HeadSHA: mr.SHA, Draft: mr.Draft,
				Mergeable: strings.EqualFold(mr.MergeStatus, "can_be_merged"),
				Body:      mr.Description, ClosesItem: ParseCloses(mr.Description), URL: mr.WebURL,
			}
			if mrNeedsDetails(mr, request.ClosesItem, accepted) {
				paths, err := g.changedPaths(ctx, mr.IID)
				if err != nil {
					return nil, err
				}
				checks, err := g.jobs(ctx, mr)
				if err != nil {
					return nil, err
				}
				request.Checks, request.DocsOnly = checks, DocsOnly(paths)
			}
			requests = append(requests, request)
		}
		if len(batch) < 100 {
			break
		}
	}
	sort.Slice(requests, func(a, b int) bool { return requests[a].Number < requests[b].Number })
	return requests, nil
}

// normalizeMRState gives the forge-neutral state the rest of the app matches on.
func normalizeMRState(state string) string {
	switch strings.ToLower(state) {
	case "opened":
		return "open"
	case "merged":
		return "merged"
	case "closed", "locked":
		return "closed"
	default:
		return strings.ToLower(state)
	}
}

// mrNeedsDetails reports whether the merge watchdog could act on this merge
// request this tick. Closed and merged requests never need another read.
func mrNeedsDetails(mr glMergeRequest, closes int, accepted map[string]struct{}) bool {
	if !strings.EqualFold(mr.State, "opened") {
		return false
	}
	if closes > 0 {
		return true
	}
	_, ok := accepted[strings.ToLower(mr.SHA)]
	return ok
}

func (g *GitLab) changedPaths(ctx context.Context, number int) ([]string, error) {
	path := fmt.Sprintf("projects/%s/merge_requests/%d/changes", g.enc, number)
	raw, err := g.cli.api(ctx, path)
	if err != nil {
		return nil, err
	}
	var changes struct {
		Changes []struct {
			OldPath string `json:"old_path"`
			NewPath string `json:"new_path"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(raw, &changes); err != nil {
		return nil, fmt.Errorf("forge: decode GitLab MR changes: %w", err)
	}
	paths := make([]string, 0, len(changes.Changes))
	for _, change := range changes.Changes {
		path := change.NewPath
		if path == "" {
			path = change.OldPath
		}
		paths = append(paths, path)
	}
	return paths, nil
}

func (g *GitLab) jobs(ctx context.Context, mr glMergeRequest) ([]Check, error) {
	path := fmt.Sprintf("projects/%s/merge_requests/%d/pipelines", g.enc, mr.IID)
	raw, err := g.cli.api(ctx, path)
	if err != nil {
		return nil, err
	}
	var pipelines []struct {
		ID  int    `json:"id"`
		SHA string `json:"sha"`
	}
	if err := json.Unmarshal(raw, &pipelines); err != nil {
		return nil, fmt.Errorf("forge: decode GitLab pipelines: %w", err)
	}
	var pipelineID int
	for _, pipeline := range pipelines {
		if pipeline.SHA == mr.SHA {
			pipelineID = pipeline.ID
			break
		}
	}
	if pipelineID == 0 {
		// A pipeline for another head is not this head's check. No pipeline means no
		// checks, which the merge gate treats as red unless the change is docs-only.
		return nil, nil
	}

	raw, err = g.cli.api(ctx, fmt.Sprintf("projects/%s/pipelines/%d/jobs?per_page=100", g.enc, pipelineID))
	if err != nil {
		return nil, err
	}
	var jobs []struct {
		Name         string `json:"name"`
		Status       string `json:"status"`
		AllowFailure bool   `json:"allow_failure"`
		WebURL       string `json:"web_url"`
	}
	if err := json.Unmarshal(raw, &jobs); err != nil {
		return nil, fmt.Errorf("forge: decode GitLab jobs: %w", err)
	}
	checks := make([]Check, 0, len(jobs))
	for _, job := range jobs {
		checks = append(checks, Check{
			Name: job.Name, Status: job.Status, Conclusion: conclusionOf(job.Status),
			AllowFailure: job.AllowFailure, URL: job.WebURL,
		})
	}
	return checks, nil
}

func conclusionOf(status string) string {
	switch strings.ToLower(status) {
	case "success":
		return "success"
	case "failed":
		return "failure"
	case "canceled", "cancelled":
		return "canceled"
	case "skipped", "manual":
		return "skipped"
	default:
		return strings.ToLower(status)
	}
}

// SetMarker adds or removes the in-flight marker label.
// Notes reads an issue's notes.
func (g *GitLab) Notes(ctx context.Context, number int) ([]Comment, error) {
	raw, err := g.cli.api(ctx, fmt.Sprintf("projects/%s/issues/%d/notes?per_page=100&sort=desc", g.enc, number))
	if err != nil {
		return nil, err
	}
	var batch []struct {
		ID        int64  `json:"id"`
		Body      string `json:"body"`
		CreatedAt string `json:"created_at"`
	}
	if err := json.Unmarshal(raw, &batch); err != nil {
		return nil, fmt.Errorf("forge: decode GitLab notes: %w", err)
	}
	out := make([]Comment, 0, len(batch))
	for _, note := range batch {
		out = append(out, Comment{ID: fmt.Sprint(note.ID), Body: note.Body, CreatedAt: note.CreatedAt})
	}
	return out, nil
}

func (g *GitLab) SetMarker(ctx context.Context, number int, present bool) error {
	field := "add_labels=" + MarkerLabel
	if !present {
		field = "remove_labels=" + MarkerLabel
	}
	_, err := g.cli.api(ctx, "-X", "PUT", fmt.Sprintf("projects/%s/issues/%d", g.enc, number), "-f", field)
	return err
}

// Comment posts one note on an issue.
func (g *GitLab) Comment(ctx context.Context, number int, body string) error {
	_, err := g.cli.api(ctx, "-X", "POST", fmt.Sprintf("projects/%s/issues/%d/notes", g.enc, number),
		"-f", "body="+body)
	return err
}

// CloseItem closes an issue.
func (g *GitLab) CloseItem(ctx context.Context, number int) error {
	_, err := g.cli.api(ctx, "-X", "PUT", fmt.Sprintf("projects/%s/issues/%d", g.enc, number),
		"-f", "state_event=close")
	return err
}

// Merge merges a change request only if its head is still sha.
func (g *GitLab) Merge(ctx context.Context, number int, sha string) error {
	_, err := g.cli.run(ctx, "mr", "merge", fmt.Sprint(number), "--sha", sha, "--yes", "--repo", g.project)
	return err
}

func atoi(value string) int {
	number, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return number
}
