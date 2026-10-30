package preflight

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// specFile is the subset of a rendered rig spec the checks read: the members
// and the runtime they are declared with. Everything else in the file belongs
// to rig's own renderer.
type specFile struct {
	Name string    `yaml:"name"`
	Pods []specPod `yaml:"pods"`
}

type specPod struct {
	ID      string       `yaml:"id"`
	Members []specMember `yaml:"members"`
}

type specMember struct {
	ID      string `yaml:"id"`
	Runtime string `yaml:"runtime"`
}

// members flattens every pod's seats.
func (s *specFile) members() []specMember {
	if s == nil {
		return nil
	}
	var out []specMember
	for _, pod := range s.Pods {
		out = append(out, pod.Members...)
	}
	return out
}

// builders counts the members that are not the reviewer. The rendered spec
// gives a builder seat the id "build-N"; anything else is the reviewer.
func (s *specFile) builders() int {
	count := 0
	for _, member := range s.members() {
		if strings.HasPrefix(member.ID, "build-") {
			count++
		}
	}
	return count
}

// declaredRuntime is the runtime the spec's seats are declared with. A spec
// whose members disagree answers "" — no single value can be claimed.
func (s *specFile) declaredRuntime() string {
	seen := ""
	for _, member := range s.members() {
		value := strings.ToLower(strings.TrimSpace(member.Runtime))
		if value == "" {
			continue
		}
		if seen != "" && seen != value {
			return ""
		}
		seen = value
	}
	return seen
}

// readSpec loads the project's rig spec from the specs dir. A missing file is
// not an error to report here: checkRig turns it into the failing condition it
// is, so the operator reads one sentence instead of a stack of them.
func readSpec(deps Deps, rigName string) (*specFile, error) {
	if strings.TrimSpace(rigName) == "" {
		return nil, fmt.Errorf("the project names no rig")
	}
	path := filepath.Join(specDir(deps.Home, rigName), "rig.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, err
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var spec specFile
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(spec.members()) == 0 {
		return nil, fmt.Errorf("%s declares no seats", path)
	}
	return &spec, nil
}

// repoURL is the URL the checks read: the row's own clone URL when it has one
// (a self-hosted forge, a local bare repository), otherwise the web URL its
// forge and repository imply.
func repoURL(p Project) string {
	if explicit := strings.TrimSpace(p.CloneURL); explicit != "" {
		return explicit
	}
	repo := strings.TrimPrefix(strings.TrimSpace(p.Repo), "https://")
	repo = strings.TrimSuffix(repo, ".git")
	switch strings.ToLower(strings.TrimSpace(p.Forge)) {
	case "github":
		return "https://github.com/" + repo + ".git"
	case "gitlab":
		return "https://gitlab.com/" + repo + ".git"
	default:
		return ""
	}
}

// hasBranch reads ls-remote's own output: one "<sha>\trefs/heads/<name>" line
// per head.
func hasBranch(lsRemoteOutput, branch string) bool {
	want := "refs/heads/" + branch
	for _, line := range strings.Split(lsRemoteOutput, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == want {
			return true
		}
	}
	return false
}

func checkGitURL(ctx context.Context, deps Deps, p Project) Check {
	check := Check{ID: CheckGitURL, Label: "Repository answers"}
	url := repoURL(p)
	if url == "" {
		// A row naming some other forge is a definite fact, not an undecidable
		// one: the app has no URL to check because the row cannot produce one.
		check.State = Fail
		check.Detail = fmt.Sprintf("the forge %q is not github or gitlab, so there is no URL to read", p.Forge)
		check.Fix = Fix{Why: "Name the forge in the project row as github or gitlab."}
		return check
	}
	command := "git ls-remote --heads " + url
	check.Fix = Fix{
		Command: command,
		Why:     "Run it by hand: if it asks for a credential, log the forge in with `gh auth login` (or `glab auth login`); if it says not found, the repository name is wrong.",
	}

	runCtx, cancel := context.WithTimeout(ctx, deps.Timeout)
	defer cancel()
	stdout, stderr, err := deps.Git(runCtx, "ls-remote", "--heads", url)
	if err == nil {
		// The repository answers; whether it has the branch dispatch works from
		// is a second question, and a milder one — the branch can be pushed
		// later, so this is a warning rather than a failure.
		branch := strings.TrimSpace(p.DefaultBranch)
		if branch != "" && !hasBranch(stdout, branch) {
			check.State = Warn
			check.Detail = fmt.Sprintf("%s answers but has no branch named %s", url, branch)
			check.Fix = Fix{
				Command: "git ls-remote --heads " + url,
				Why:     "Dispatch works from the default branch; push it, or correct default_branch in the project row.",
			}
			return check
		}
		check.State = OK
		check.Detail = fmt.Sprintf("%s answers", url)
		return check
	}
	if runCtx.Err() != nil {
		check.State = Unknown
		check.Detail = fmt.Sprintf("%s did not answer within %s", url, deps.Timeout)
		return check
	}
	line := lastLine(stderr)
	if line == "" {
		line = lastLine(err.Error())
	}
	if line == "" {
		check.State = Unknown
		check.Detail = fmt.Sprintf("git could not read %s and printed nothing", url)
		return check
	}
	check.State = Fail
	check.Detail = line
	return check
}

func checkBaseDir(ctx context.Context, deps Deps, p Project) Check {
	check := Check{ID: CheckBaseDir, Label: "Checkout in place"}
	path := strings.TrimSpace(p.MainPath)
	url := repoURL(p)
	if path == "" {
		check.State = Fail
		check.Detail = "the project names no paths.main"
		check.Fix = Fix{Why: "Give the project the directory its checkout lives in."}
		return check
	}

	info, err := os.Stat(path)
	switch {
	case os.IsNotExist(err):
		check.State = Fail
		check.Detail = fmt.Sprintf("%s does not exist", path)
		check.Fix = Fix{
			Command: "mkdir -p " + path,
			Step:    StepMkdirBase,
			Why:     "The directory has to exist before a clone can fill it.",
		}
		return check
	case err != nil:
		check.State = Unknown
		check.Detail = fmt.Sprintf("could not read %s", path)
		check.Fix = Fix{Command: "ls -ld " + path, Why: "The path cannot be read; check its permissions."}
		return check
	case !info.IsDir():
		check.State = Fail
		check.Detail = fmt.Sprintf("%s is a file, not a directory", path)
		check.Fix = Fix{Why: "paths.main must name the checkout directory, not a file."}
		return check
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		check.State = Unknown
		check.Detail = fmt.Sprintf("could not list %s", path)
		check.Fix = Fix{Command: "ls -la " + path, Why: "The directory cannot be listed; check its permissions."}
		return check
	}
	if len(entries) == 0 {
		check.State = Fail
		check.Detail = fmt.Sprintf("%s is empty", path)
		if url == "" {
			check.Fix = Fix{Why: "The project's forge is not github or gitlab, so the clone URL is unknown."}
			return check
		}
		check.Fix = Fix{
			Command: "git clone " + url + " " + path,
			Step:    StepClone,
			Why:     "An empty directory is a checkout that was never filled.",
		}
		return check
	}

	// A populated directory is only this project's checkout when its origin is
	// this project's repository. Anything else is a different clone that a
	// setup step must not touch.
	runCtx, cancel := context.WithTimeout(ctx, deps.Timeout)
	defer cancel()
	stdout, stderr, err := deps.Git(runCtx, "-C", path, "remote", "get-url", "origin")
	if err != nil {
		line := lastLine(stderr)
		if line == "" {
			line = lastLine(err.Error())
		}
		check.State = Fail
		check.Detail = fmt.Sprintf("%s is not a git checkout of %s: %s", path, url, line)
		check.Fix = Fix{Why: "The directory holds something else; move it aside and let the clone step fill an empty directory."}
		return check
	}
	origin := strings.TrimSpace(stdout)
	if url != "" && !sameRepo(origin, url) {
		check.State = Fail
		check.Detail = fmt.Sprintf("%s is a checkout of %s, but the project's repository is %s", path, origin, url)
		check.Fix = Fix{Why: "The right directory holds the wrong repository; no setup step may replace it."}
		return check
	}
	check.State = OK
	if origin != "" {
		check.Detail = fmt.Sprintf("%s is a checkout of %s", path, origin)
	} else {
		check.Detail = fmt.Sprintf("%s is a checkout of %s", path, url)
	}

	// The checkout is right. The second half of "the base directory is ready" is
	// where per-item worktrees are cut: absent is worth saying, because the rig
	// declares seats with a cwd under it.
	if worktrees := strings.TrimSpace(p.Worktrees); worktrees != "" {
		if info, err := os.Stat(worktrees); err != nil || !info.IsDir() {
			check.State = Warn
			check.Detail = fmt.Sprintf("%s is a checkout of %s, but paths.worktrees %s does not exist yet", path, origin, worktrees)
			check.Fix = Fix{
				Command: "mkdir -p " + worktrees,
				Why:     "Per-item worktrees are cut under this directory; it has to exist before the first dispatch.",
			}
		}
	}
	return check
}

// sameRepo compares an origin URL with the project's URL, tolerating the
// differences that do not change which repository it is: a trailing .git, a
// scp-style remote, and the https/ssh transports of the same host and path.
func sameRepo(a, b string) bool {
	return normalizeRemote(a) == normalizeRemote(b)
}

func normalizeRemote(value string) string {
	s := strings.TrimSpace(strings.ToLower(value))
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	// git@github.com:owner/repo and ssh://git@github.com/owner/repo are the same
	// repository as https://github.com/owner/repo.
	if at := strings.Index(s, "@"); at >= 0 {
		s = s[at+1:]
		s = strings.Replace(s, ":", "/", 1)
	}
	for _, prefix := range []string{"https://", "http://", "ssh://", "git://", "git+"} {
		s = strings.TrimPrefix(s, prefix)
	}
	parts := strings.Split(s, "/")
	if len(parts) > 1 && strings.Contains(parts[0], ".") {
		parts = parts[1:]
	}
	return strings.Join(parts, "/")
}

func checkRig(ctx context.Context, deps Deps, p Project, spec *specFile, specErr error) Check {
	check := Check{ID: CheckRig, Label: "Rig running"}
	rigName := strings.TrimSpace(p.Rig)

	if p.Draft {
		check.State = Unknown
		check.Detail = "the rig is created with the project, so it cannot be checked before the project exists"
		check.Fix = Fix{
			Command: "rig specs sync",
			Why:     "Create the project first; the spec and the rig check both follow the registry row.",
		}
		return check
	}
	if rigName == "" {
		check.State = Fail
		check.Detail = "the project names no rig"
		check.Fix = Fix{
			Command: "rig specs sync",
			Step:    StepApplyRig,
			Why:     "Rendering the project's rig and syncing it gives the rig a name the dispatcher can launch.",
		}
		return check
	}
	if specErr != nil {
		check.State = Fail
		if errors.Is(specErr, fs.ErrNotExist) {
			check.Detail = fmt.Sprintf("rig %s is not in the specs: there is no rig.yaml under ~/.openrig/specs/rigs/%s", rigName, rigName)
		} else {
			check.Detail = fmt.Sprintf("rig %s is in the specs but cannot be read: %s", rigName, specErr)
		}
		check.Fix = Fix{
			Command: "rig specs sync",
			Step:    StepApplyRig,
			Why:     "Render the rig from the project row and sync it, then launch it with `rig up`.",
		}
		return check
	}

	runCtx, cancel := context.WithTimeout(ctx, deps.Timeout)
	defer cancel()
	seats, err := deps.Seats(runCtx, rigName)
	if err != nil {
		check.State = Unknown
		line := lastLine(err.Error())
		check.Detail = fmt.Sprintf("the rig could not be read: %s", line)
		check.Fix = Fix{
			Command: "rig ps --nodes --full --json --rig " + rigName,
			Why:     "The rig CLI did not answer; run it by hand to see whether the daemon is up.",
		}
		return check
	}
	if len(seats) == 0 {
		check.State = Fail
		check.Detail = fmt.Sprintf("rig %s is not up", rigName)
		check.Fix = Fix{
			Command: "rig up " + filepath.Join(specDir(deps.Home, rigName), "rig.yaml"),
			Step:    StepUpRig,
			Why:     "The spec exists but no seat is running, so nothing would pick up the work.",
		}
		return check
	}

	builders := spec.builders()
	want := builders + 1
	if builders == 0 {
		// A spec without build-N ids is not the shape this product renders; say
		// what was found instead of guessing which seat is the reviewer.
		check.State = Fail
		check.Detail = fmt.Sprintf("rig %s declares %d seats and none of them is a builder", rigName, len(seats))
		check.Fix = Fix{
			Command: "rig specs sync",
			Step:    StepApplyRig,
			Why:     "The rendered spec has no builder seats; re-render it from the project row.",
		}
		return check
	}
	if len(seats) != want {
		check.State = Fail
		check.Detail = fmt.Sprintf("rig %s is running %d seats; the spec declares %s and one reviewer (%d seats)",
			rigName, len(seats), plural(builders, "builder"), want)
		check.Fix = Fix{
			Command: "rig up " + filepath.Join(specDir(deps.Home, rigName), "rig.yaml"),
			Step:    StepUpRig,
			Why:     "A rig with the wrong seat count dispatches to seats the spec does not describe.",
		}
		return check
	}

	wantRuntime := resolveRuntime(p, spec)
	for _, seat := range seats {
		got := strings.ToLower(strings.TrimSpace(seat.Runtime))
		if wantRuntime != "" && got != "" && got != wantRuntime {
			check.State = Fail
			check.Detail = fmt.Sprintf("seat %s runs %s; the project runs %s", seat.Session, got, wantRuntime)
			check.Fix = Fix{
				Command: "rig up " + filepath.Join(specDir(deps.Home, rigName), "rig.yaml"),
				Step:    StepUpRig,
				Why:     "Every seat has to run the project's runtime, or its logins and models disagree.",
			}
			return check
		}
	}

	// The rig is the right shape; the only remaining question is whether the
	// spec on disk still agrees with the project row. A stale spec is mild — the
	// running rig is correct — so it warns instead of failing.
	if rowRuntime := strings.ToLower(strings.TrimSpace(p.Runtime)); rowRuntime != "" {
		if declared := spec.declaredRuntime(); declared != "" && declared != rowRuntime {
			check.State = Warn
			check.Detail = fmt.Sprintf("rig %s is up on %s, but its spec still declares %s", rigName, rowRuntime, declared)
			check.Fix = Fix{
				Command: "rig specs sync",
				Step:    StepApplyRig,
				Why:     "Re-render the spec from the project row so the next launch matches what is running now.",
			}
			return check
		}
	}

	check.State = OK
	check.Detail = fmt.Sprintf("rig %s is up with %s and one reviewer, all on %s", rigName, plural(builders, "builder"), wantRuntime)
	return check
}

func checkRuntime(ctx context.Context, deps Deps, p Project, runtime Runtime, probeErr error) Check {
	check := Check{ID: CheckRuntime, Label: "Runtime can log in"}
	if !KnownRuntime(runtime.ID) {
		check.State = Fail
		check.Detail = fmt.Sprintf("runtime %q is not one of %s", runtime.ID, strings.Join(Runtimes, ", "))
		check.Fix = Fix{Why: "Name the project's runtime as one of the four; config load refuses anything else."}
		return check
	}
	if !runtime.Supported {
		check.State = Fail
		check.Detail = fmt.Sprintf("runtime %s is not installed on this host", runtime.ID)
		check.Fix = Fix{Why: "Install the " + runtime.ID + " CLI on this host; the rig launches it by name."}
		return check
	}
	if probeErr != nil {
		check.State = Unknown
		check.Detail = fmt.Sprintf("the %s credential store could not be read: %s", runtime.ID, lastLine(probeErr.Error()))
		check.Fix = Fix{
			Command: "ls -ld " + strings.TrimSpace(deps.Home),
			Why:     "The app cannot see whether a credential exists; fix the permissions rather than logging in again.",
		}
		return check
	}
	if !runtime.AuthPresent {
		check.State = Fail
		check.Detail = fmt.Sprintf("no %s credential on this host", runtime.ID)
		check.Fix = Fix{
			Command: runtime.LoginHint,
			Why:     "The login belongs to the operator: the app never runs it and never stores a credential.",
		}
		return check
	}
	check.State = OK
	check.Detail = fmt.Sprintf("runtime %s is installed and a credential exists on this host", runtime.ID)
	return check
}

// plural keeps a sentence readable when a rig has one builder.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// lastLine is the fact a failing command reports, taken from the command's own
// stderr rather than from a paraphrase: "fatal: could not read Username for
// 'https://github.com'" tells an operator what a cheerful "auth failed" does
// not.
//
// git's own *first* diagnostic is the cause and its later lines are the wrapper
// ("fatal: Could not read from remote repository." followed by "Please make sure
// you have the correct access rights / and the repository exists."), so the
// first fatal wins. A preceding "error:" is often the environment rather than
// the repository — "unable to read askpass response" — so it only answers when
// git printed no fatal at all. The last non-empty line is the fallback for a
// command that does not use git's prefixes.
func lastLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "fatal:") {
			return strings.TrimSpace(line)
		}
	}
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "error:") {
			return strings.TrimSpace(line)
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

// execGit runs one git command with its streams kept apart: stdout is the
// answer, stderr is the diagnosis.
func execGit(ctx context.Context, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.WaitDelay = 200 * time.Millisecond // a killed git can leave a child holding the pipes
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}
