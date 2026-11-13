package preflight

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/dinhtrung/fleet-console/internal/forge"
)

// execForge runs one forge CLI command. The default for Deps.Forge: gh and glab
// own their credential stores, and the app never reads a token.
func execForge(ctx context.Context, binary string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.WaitDelay = 200 * time.Millisecond // a killed CLI can leave a child holding the pipes
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// checkForgeWrite proves the forge token may write, not just read. It asks the
// forge to perform a write the forge must refuse on its merits — an invalid
// head, an empty comment body — and reads which refusal comes back. A
// validation refusal (422 on GitHub, 400 on GitLab) means the token cleared
// authorization, because a forge only validates what it was willing to accept.
// A permission refusal means the token can read but never deliver: every item
// this fleet dispatches would stall at the first write, so the check fails
// where the mirror's first write would. Both probes are deliberately invalid —
// nothing they do can leave state behind, because a forge that accepted an
// invalid head or an empty comment would be broken past what a check can paper
// over.
//
// The mapping is narrow on purpose: anything the probe cannot classify — a
// rate limit, a server fault, a timeout, a refusal on a field the probe did
// not aim at — is unknown, never a green lie.
func checkForgeWrite(ctx context.Context, deps Deps, p Project) Check {
	check := Check{ID: CheckForgeWrite, Label: "Forge token can write"}

	forgeKind := strings.ToLower(strings.TrimSpace(p.Forge))
	repo := strings.TrimPrefix(strings.TrimSpace(p.Repo), "https://")
	repo = strings.TrimSuffix(repo, ".git")
	if repo == "" {
		// Without a repository there is nothing to write to. git_url reports
		// the same gap as its own failure; repeating it here would read as two
		// faults where there is one.
		check.State = Unknown
		check.Detail = "the project names no repository, so the write cannot be probed"
		return check
	}

	switch forgeKind {
	case "github":
		return probeGitHub(ctx, deps, p, repo)
	case "gitlab":
		return probeGitLab(ctx, deps, p, repo)
	default:
		// A row naming some other forge is the fact git_url already reports as
		// a failure. Here it is only unprobeable; say so without restating the
		// other check's verdict.
		check.State = Unknown
		check.Detail = fmt.Sprintf("the forge %q is not github or gitlab, so the write cannot be probed", p.Forge)
		return check
	}
}

// probeGitHub asks two questions. The change-request probe names a head that
// cannot exist; the note probe sends an empty body to the newest open item.
// GitHub refuses both before creating anything, and which refusal it picks is
// the answer. A fail on either is a fail of the check: a token that cannot
// comment cannot deliver a verdict, and one that cannot open a change request
// cannot deliver the work.
func probeGitHub(ctx context.Context, deps Deps, p Project, repo string) Check {
	check := Check{ID: CheckForgeWrite, Label: "Forge token can write"}
	branch := strings.TrimSpace(p.DefaultBranch)
	if branch == "" {
		branch = "main"
	}
	runCtx, cancel := context.WithTimeout(ctx, deps.Timeout)
	defer cancel()
	stdout, stderr, err := deps.Forge(runCtx, "gh", "api", "-X", "POST", "repos/"+repo+"/pulls",
		"-f", "title=preflight", "-f", "head=fleet-console-preflight-nonexistent",
		"-f", "base="+branch)
	number := p.ProbeItemNumber
	if err != nil {
		if runCtx.Err() != nil {
			check.State = Unknown
			check.Detail = fmt.Sprintf("gh did not answer within %s", deps.Timeout)
			check.Fix = Fix{
				Command: "gh api -i user",
				Why:     "The forge CLI did not answer; check its login and connectivity by hand.",
			}
			return check
		}
		if _, ok := classifyWrite(ctx, deps, kindGitHub, repo, "", stdout, stderr, err); ok {
			return forgeWriteVerdict(ctx, deps, check, stdout, stderr, err, "change request")
		}
		// A failure the classifier does not recognize is not a permission fact;
		// reading the body is still worth it, because the refusal may be the
		// validation the probe was designed to draw.
	}

	// The body carries the answer either way: gh prints the forge's JSON to
	// stdout whether the call failed with 422 or, against every expectation,
	// succeeded.
	return githubBodyVerdict(ctx, deps, check, repo, number, stdout, stderr, err)
}

// githubBodyVerdict reads the change-request probe's body. A validation error
// on the probe head is the pass; anything else is named, not guessed.
func githubBodyVerdict(ctx context.Context, deps Deps, check Check, repo string, number int, stdout, stderr string, err error) Check {
	field, code, refused := githubRefusal(stdout)
	switch {
	case refused && field == "head" && code == "invalid":
		return probeNotesGitHub(ctx, deps, check, repo, number)
	case refused && field == "head":
		check.State = Unknown
		check.Detail = fmt.Sprintf("the probe head was refused with code %q, not the validation the probe aims to draw", code)
		check.Fix = Fix{
			Command: "gh api -i user",
			Why:     "Run it by hand to see why the forge refused the probe head.",
		}
		return check
	case refused:
		check.State = Unknown
		check.Detail = fmt.Sprintf("the probe was refused on %q, not on the probe head; the answer may not be about writing", field)
		check.Fix = Fix{
			Command: "gh api -i user",
			Why:     "Run it by hand to see which field the forge refused and why.",
		}
		return check
	case err == nil:
		// A change request for an invalid head cannot exist; a 2xx here means
		// the probe left something behind, which the check must never claim as
		// its own success.
		check.State = Unknown
		check.Detail = "the probe write unexpectedly succeeded; inspect the repository by hand"
		return check
	}
	line := firstNonEmpty(lastLine(stderr), lastLine(err.Error()))
	check.State = Unknown
	check.Detail = fmt.Sprintf("the change-request probe did not classify: %s", line)
	check.Fix = Fix{
		Command: "gh api -i user",
		Why:     "Run it by hand to see the scopes the token actually carries.",
	}
	return check
}

// probeNotesGitHub asks the commenting question on the newest open item in the
// mirror. An empty body is refused before a comment is created, and the
// refusal says whether the token may comment at all. A comment the forge
// would validate does not end the check: a token may comment and still not
// merge (#329), so the merge question follows.
func probeNotesGitHub(ctx context.Context, deps Deps, check Check, repo string, number int) Check {
	if number <= 0 {
		// The mirror has no open item to aim at. That is an honest unknown, not
		// a failure: nothing observed the token refuse a write.
		check.State = Unknown
		check.Detail = "no open item to probe a note on"
		return check
	}
	runCtx, cancel := context.WithTimeout(ctx, deps.Timeout)
	defer cancel()
	stdout, stderr, err := deps.Forge(runCtx, "gh", "api", "-X", "POST",
		fmt.Sprintf("repos/%s/issues/%d/comments", repo, number), "-f", "body=")
	if err != nil {
		if runCtx.Err() != nil {
			check.State = Unknown
			check.Detail = fmt.Sprintf("gh did not answer within %s", deps.Timeout)
			return check
		}
		if _, ok := classifyWrite(ctx, deps, kindGitHub, repo, "", stdout, stderr, err); ok {
			check.State = Fail
			check.Detail = forgeOwnLine(stderr, err)
			check.Fix = forgeWriteFix(ctx, deps, "The token may open a change request but may not comment", opComment)
			return check
		}
	}
	if message, blank := githubBlankBody(stdout); blank {
		return probeMerge(ctx, deps, check, repo)
	} else if message != "" {
		check.State = Unknown
		check.Detail = fmt.Sprintf("the note probe was refused with %q, not the blank-body validation the probe aims to draw", message)
		return check
	}
	if err == nil {
		// An empty comment was accepted; the probe left a note behind. Own up
		// to it instead of reading green.
		check.State = Unknown
		check.Detail = "the probe note was unexpectedly accepted; remove it and inspect the token by hand"
		return check
	}
	check.State = Unknown
	check.Detail = fmt.Sprintf("the note probe did not classify: %s", forgeOwnLine(stderr, err))
	return check
}

// probeMerge asks the merge question (#329). The trap the comment probe cannot
// see: GitHub files a merge under Contents, not Pull requests, so a
// fine-grained token carrying only Pull requests: write comments happily and
// is refused at the merge — the first sign today being a refused merge on real
// work. The probe aims a squash merge at an already-merged pull request: such
// a request cannot merge again, so the mutation is a no-op whatever the answer,
// while a token without Contents: write is refused before GitHub ever reaches
// the merged-head check, and the refusal is the same mergePullRequest sentence
// a real merge would draw. Green, then, means "may comment and may merge";
// a merge the forge would not classify is an unknown, never a green lie — the
// missing-merged-PR exit in particular admits the check cannot tell.
func probeMerge(ctx context.Context, deps Deps, check Check, repo string) Check {
	runCtx, cancel := context.WithTimeout(ctx, deps.Timeout)
	defer cancel()
	stdout, stderr, err := deps.Forge(runCtx, "gh", "pr", "list", "--state", "merged", "--limit", "1",
		"--json", "number,headRefOid", "-R", repo)
	if err != nil {
		if runCtx.Err() != nil {
			check.State = Unknown
			check.Detail = fmt.Sprintf("gh did not answer within %s", deps.Timeout)
			return check
		}
		check.State = Unknown
		check.Detail = fmt.Sprintf("the merge probe could not find an already-merged change request to aim at: %s", firstNonEmpty(lastLine(stderr), lastLine(err.Error())))
		return check
	}
	number, sha, found := mergedPR(stdout)
	if !found {
		check.State = Unknown
		check.Detail = "no already-merged change request to aim the merge probe at; the check cannot vouch for merge, only for comment and open a change request"
		return check
	}
	stdout, stderr, err = deps.Forge(runCtx, "gh", "pr", "merge", fmt.Sprint(number),
		"--squash", "--match-head-commit", sha, "-R", repo)
	if err != nil {
		if runCtx.Err() != nil {
			check.State = Unknown
			check.Detail = fmt.Sprintf("gh did not answer within %s", deps.Timeout)
			return check
		}
		if _, ok := classifyMerge(ctx, deps, repo, number, sha, stdout, stderr, err); ok {
			check.State = Fail
			check.Detail = forgeOwnLine(stderr, err)
			check.Fix = forgeWriteFix(ctx, deps, "The token may comment and open a change request but may not merge", opMerge)
			return check
		}
	}
	if message, merged := githubAlreadyMerged(stdout, stderr); merged {
		check.State = OK
		check.Detail = "the forge validates the probe writes, so the token may comment, open a change request and merge"
		return check
	} else if message != "" {
		check.State = Unknown
		check.Detail = fmt.Sprintf("the merge probe was refused with %q, not the merged-head refusal the probe aims to draw", message)
		return check
	}
	if err == nil {
		// The merge of an already-merged pull request cannot succeed; a 2xx here
		// means the probe did not aim at what it thought it did.
		check.State = Unknown
		check.Detail = "the merge probe unexpectedly succeeded; inspect the repository by hand"
		return check
	}
	check.State = Unknown
	check.Detail = fmt.Sprintf("the merge probe did not classify: %s", forgeOwnLine(stderr, err))
	return check
}

// mergedPR reads `gh pr list --json number,headRefOid` output for the newest
// merged pull request: its number and head sha name the no-op merge target.
func mergedPR(stdout string) (number int, sha string, found bool) {
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		return 0, "", false
	}
	var list []struct {
		Number     int    `json:"number"`
		HeadRefOid string `json:"headRefOid"`
	}
	if err := json.Unmarshal([]byte(trimmed), &list); err != nil || len(list) == 0 {
		return 0, "", false
	}
	return list[0].Number, list[0].HeadRefOid, true
}

// githubAlreadyMerged reads the merge probe's answer for the forge's own
// merged-head refusal — the sentence a merge of an already-merged pull request
// draws when the token cleared authorization. message is a different refusal's
// own words, when the body carries one.
func githubAlreadyMerged(stdout, stderr string) (message string, merged bool) {
	combined := strings.ToLower(stdout + "\n" + stderr)
	for _, phrase := range []string{
		"already merged",
		"pull request is already merged",
		"cannot be merged",
	} {
		if strings.Contains(combined, phrase) {
			return "", true
		}
	}
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		return "", false
	}
	var body struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(trimmed), &body); err != nil {
		return "", false
	}
	return body.Message, false
}

// probeGitLab asks the merge-request question with a source branch that cannot
// exist. GitLab answers 400 when the token cleared authorization and 403/401
// when it did not — the same distinction GitHub draws with 422 and 403.
// Every exit names the host the probe aimed at (forge-host D5): a green
// against gitlab.com for a mirror on the company instance reads as two
// different answers if the detail does not say which one it is.
func probeGitLab(ctx context.Context, deps Deps, p Project, repo string) Check {
	check := Check{ID: CheckForgeWrite, Label: "Forge token can write"}
	host := p.ForgeHost
	if host == "" {
		host = "glab's default"
	}
	runCtx, cancel := context.WithTimeout(ctx, deps.Timeout)
	defer cancel()
	stdout, stderr, err := deps.Forge(runCtx, "glab", "api", "-X", "POST",
		"projects/"+strings.ReplaceAll(repo, "/", "%2F")+"/merge_requests",
		"-f", "source_branch=fleet-console-preflight-nonexistent",
		"-f", "target_branch=main", "-f", "title=preflight")
	if err != nil {
		if runCtx.Err() != nil {
			check.State = Unknown
			check.Detail = fmt.Sprintf("glab did not answer within %s (API host: %s)", deps.Timeout, host)
			check.Fix = Fix{
				Command: "glab auth status",
				Why:     "The forge CLI did not answer; check its login and connectivity by hand.",
			}
			return check
		}
		if glabHost, refused := glabRefusedHost(stderr); refused {
			// The credential never reached the forge: glab saw a host it does
			// not have a login for and stopped before any HTTP call, so there
			// is no forge verdict to classify. The sentence is glab's own,
			// quoted verbatim, and the remedy is the login command aimed at
			// the host glab named (not the project's spelling of it).
			remedy := glabHost
			if remedy == "" {
				remedy = host
			}
			check.State = Fail
			check.Detail = fmt.Sprintf("%s Remedy: glab auth login --hostname %s, as the user that runs the service (API host: %s).",
				glabHostLine(stderr, glabHost), remedy, remedy)
			check.Fix = Fix{
				Command: "glab auth login --hostname " + remedy,
				Why:     "The host is not registered with glab, so the credential is never sent; log in to it as the user that runs the service.",
			}
			return check
		}
		if _, ok := classifyWrite(ctx, deps, kindGitLab, repo, p.ForgeHost, stdout, stderr, err); ok {
			check.State = Fail
			check.Detail = fmt.Sprintf("%s (API host: %s)", forgeOwnLine(stderr, err), host)
			check.Fix = Fix{
				Command: "glab auth login",
				Why:     "The token may read the project but may not open merge requests; log glab in with a token that carries the api scope.",
			}
			return check
		}
		// A 400 the classifier did not claim is the validation the probe aims
		// to draw — but only when it names the probe branch, so a refusal on
		// some other field is not read as a pass.
		if strings.Contains(strings.ToLower(stderr+"\n"+stdout), "source_branch") {
			check.State = OK
			check.Detail = fmt.Sprintf("the forge validates the probe write, so the token may comment and open a change request (API host: %s)", host)
			return check
		}
		check.State = Unknown
		check.Detail = fmt.Sprintf("the merge-request probe did not classify: %s (API host: %s)", forgeOwnLine(stderr, err), host)
		check.Fix = Fix{
			Command: "glab auth status",
			Why:     "Run it by hand to see the scopes the token actually carries.",
		}
		return check
	}
	// glab exits non-zero on a 400, so a clean exit means the merge request was
	// created — the probe left something behind and must not read as green.
	check.State = Unknown
	check.Detail = fmt.Sprintf("the probe merge request was unexpectedly accepted; close it and inspect the token by hand (API host: %s)", host)
	return check
}

// glabRefusedHost reports whether glab refused to send the credential because
// the host is not registered with `glab auth login`. The phrase is glab's own,
// measured verbatim on a self-hosted instance; host is what glab named in the
// sentence, so the remedy aims at the spelling glab itself uses.
func glabRefusedHost(stderr string) (host string, refused bool) {
	const phrase = "is neither the default host nor a configured host"
	message := strings.ToLower(stderr)
	if !strings.Contains(message, phrase) {
		return "", false
	}
	// The host travels in the sentence as a double-quoted word, read from the
	// raw stderr so the remedy carries glab's own spelling of it.
	if _, after, ok := strings.Cut(stderr, `"`); ok {
		if quoted, _, ok := strings.Cut(after, `"`); ok {
			return quoted, true
		}
	}
	return "", true
}

// glabHostLine is glab's own sentence, verbatim, the way a failing probe
// reports every other refusal.
func glabHostLine(stderr, host string) string {
	for _, line := range strings.Split(stderr, "\n") {
		if strings.Contains(strings.ToLower(line), "is neither the default host nor a configured host") {
			return strings.TrimSpace(line)
		}
	}
	return fmt.Sprintf("glab refused to send the credential to %q", host)
}

// classifyWrite routes the probe's raw result through the forge adapter's own
// write classifier, so the permission matchers T1 added stay in exactly one
// place. The recorded runner replays what the CLI answered; the adapter
// classifies it the same way it would classify a real mirror write, and
// forge.IsPermission reads the verdict. ok is false when the classifier has no
// opinion — a validation refusal, which is the pass — so the caller reads the
// body itself.
func classifyWrite(ctx context.Context, deps Deps, kind, repo, host, stdout, stderr string, err error) (error, bool) {
	runner := recordedRunner{stdout: stdout, stderr: stderr, err: err}
	var classified error
	switch kind {
	case kindGitLab:
		// The probe's adapter is bound to the project's host (forge-host D4):
		// the classification answers for the forge the mirror will talk to.
		classified = forge.NewGitLab(repo, host, runner, nil).Comment(ctx, 1, "")
	default:
		classified = forge.NewGitHub(repo, runner, nil).Comment(ctx, 1, "")
	}
	return classified, forge.IsPermission(classified)
}

// classifyMerge routes the merge probe's raw result through the forge adapter's
// own merge classifier (#329) — the same matchers a refused merge on real work
// would meet, so the probe and the tick cannot disagree about what a permission
// refusal looks like. number and sha name the already-merged pull request the
// probe aims at; the adapter passes them to `gh pr merge` the way a real merge
// would.
func classifyMerge(ctx context.Context, deps Deps, repo string, number int, sha, stdout, stderr string, err error) (error, bool) {
	runner := recordedRunner{stdout: stdout, stderr: stderr, err: err}
	classified := forge.NewGitHub(repo, runner, nil).Merge(ctx, number, sha)
	return classified, forge.IsPermission(classified)
}

// recordedRunner replays one CLI answer into the forge adapter.
type recordedRunner struct {
	stdout, stderr string
	err            error
}

func (r recordedRunner) Run(ctx context.Context, argv ...string) (string, string, error) {
	return r.stdout, r.stderr, r.err
}

// forgeWriteVerdict turns a classified refusal into the check's failure: the
// forge's own line is the detail, and the fix is picked by the scopes the
// token actually carries. operation names the write the probe aimed at, so the
// fix names the permission that operation needs (#329) instead of a generic
// pair that leaves a merge refused.
func forgeWriteVerdict(ctx context.Context, deps Deps, check Check, _, stderr string, err error, operation string) Check {
	check.State = Fail
	check.Detail = forgeOwnLine(stderr, err)
	check.Fix = forgeWriteFix(ctx, deps, "The token may read the repository but may not "+forgeWriteVerb(operation), operation)
	return check
}

// forgeWriteVerb words the operation as the refusal sentence's verb.
func forgeWriteVerb(operation string) string {
	switch operation {
	case opComment:
		return "comment"
	case opMerge:
		return "merge"
	case "change request":
		return "open a change request"
	}
	return "perform the write"
}

// forgeWriteFix names the repair. A classic PAT carries its scopes in
// x-oauth-scopes; a fine-grained token leaves the header empty, and the two
// are repaired differently. The fine-grained sentence names the one permission
// the refused operation needs (forge.FixPermission, #329) — "Issues: write and
// Pull requests: write" on a refused merge sent the operator to grant, wait,
// and hit the same 403. A scope read that will not answer gets a sentence that
// covers both, never a guess.
func forgeWriteFix(ctx context.Context, deps Deps, prefix string, operation string) Fix {
	runCtx, cancel := context.WithTimeout(ctx, deps.Timeout)
	defer cancel()
	stdout, _, err := deps.Forge(runCtx, "gh", "api", "-i", "user")
	grant := "grant the fine-grained token the permissions the operation needs"
	if fix := forge.FixPermission(operation); fix != "" {
		grant = "grant it " + fix + " on this repository"
	}
	if err != nil {
		return Fix{
			Why: prefix + ": refresh the classic token with `gh auth refresh -s repo`, or " + grant + ".",
		}
	}
	if classicToken(stdout) {
		return Fix{
			Command: "gh auth refresh -s repo",
			Why:     prefix + "; the classic token's repo scope was never granted or has been revoked.",
		}
	}
	return Fix{
		Why: prefix + "; " + grant + ".",
	}
}

// classicToken reads the scope header out of `gh api -i user`'s response. A
// classic PAT always sends x-oauth-scopes; a fine-grained token sends the
// header with an empty value or not at all.
func classicToken(headers string) bool {
	for _, line := range strings.Split(headers, "\n") {
		name, value, found := strings.Cut(strings.TrimSpace(line), ":")
		if !found || !strings.EqualFold(strings.TrimSpace(name), "x-oauth-scopes") {
			continue
		}
		return strings.TrimSpace(value) != ""
	}
	return false
}

// githubRefusal reads the probe's JSON body for the field the forge refused.
// refused is false when the body is no refusal at all.
func githubRefusal(stdout string) (field, code string, refused bool) {
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		return "", "", false
	}
	var body struct {
		Message string `json:"message"`
		Errors  []struct {
			Field string `json:"field"`
			Code  string `json:"code"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(trimmed), &body); err != nil || len(body.Errors) == 0 {
		return "", "", false
	}
	return body.Errors[0].Field, body.Errors[0].Code, true
}

// githubBlankBody reads the note probe's body for the blank-body validation.
// message is the refusal's own words when the body is a different refusal.
func githubBlankBody(stdout string) (message string, blank bool) {
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		return "", false
	}
	var body struct {
		Message string `json:"message"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(trimmed), &body); err != nil {
		return "", false
	}
	for _, e := range body.Errors {
		if strings.Contains(strings.ToLower(e.Message), "cannot be blank") {
			return "", true
		}
	}
	if len(body.Errors) > 0 {
		return body.Errors[0].Message, false
	}
	return "", false
}

// forgeOwnLine is the fact a failing probe reports: the forge's own last line,
// verbatim, not a paraphrase.
func forgeOwnLine(stderr string, err error) string {
	line := lastLine(stderr)
	if line == "" {
		line = lastLine(err.Error())
	}
	return line
}

// forge kinds, matching the values the forge adapters declare.
const (
	kindGitHub = "github"
	kindGitLab = "gitlab"
)

// probe operations, matching the forge adapter's operation names — the same
// words a permission raise carries, so a refusal names the same operation
// everywhere it is reported (#329).
const (
	opComment = "comment"
	opMerge   = "merge"
)

// firstNonEmpty keeps the detail one line even when one stream is empty.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if line := strings.TrimSpace(value); line != "" {
			return line
		}
	}
	return ""
}
