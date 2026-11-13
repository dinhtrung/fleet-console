# Forge credentials

The app never holds a forge token. Every forge call is a `gh` or `glab` invocation with the parent environment
(`internal/forge/cli.go`, `CLIRunner`), so **the credential is whatever the CLI has stored on that box, and its
permissions are a property of the token, not of any environment variable.** `FLEET_CONSOLE_TOKEN` is the bearer
the app's own HTTP API checks; it has nothing to do with the forge.

Read this before creating the token on a new box, and before granting anything when a tick prints
`permission: <operation>: …`.

## What each write needs

`act` writes in six places. Each one has its own permission on a fine-grained token, which is why "the token can
read and comment" is not the same as "the token can merge".

| Journal operation | `gh` verb | Classic PAT scope | Fine-grained permission (repository) |
|---|---|---|---|
| `merge` | `gh pr merge` → GraphQL `mergePullRequest` | `repo` | **Contents: Read and write** |
| `ready` | `gh pr ready` | `repo` | Pull requests: Read and write |
| `comment` | `gh pr comment`, issue comment | `repo` | Issues: Read and write (or Pull requests: write on a pull request) |
| `close` | `gh issue close` | `repo` | Issues: Read and write |
| in-flight marker (`status::ongoing`) | `gh issue edit --add-label` | `repo` | Issues: Read and write |
| checks read | `/repos/<slug>/commits/<sha>/check-runs` | `repo` (private repo) | not documented for PATs at all — see below |

Plus `workflow` (classic) if a builder may edit files under `.github/workflows/`.

**The merge row is the trap.** GitHub files a merge under *Contents*, not *Pull requests*, so a fine-grained token
carrying `Pull requests: write` comments, labels and marks ready perfectly — and is refused at the last step:

```
act-skip: <project>: #<n> permission: merge: GraphQL: Resource not accessible by personal access token (mergePullRequest)
```

Everything else on the board looks healthy, so the line reads like a fleet problem. It is one missing permission.

GitLab has no such trap: the token needs the `api` scope, and the account needs **Developer** or better on the
project (Developer can merge; Guest/Reporter cannot).

## Two setups that work

### Classic PAT — recommended for a box that must merge

One token, one scope, and the Checks API works.

```sh
# device flow (interactive box)
gh auth login --hostname github.com --git-protocol ssh --scopes repo

# or from a file you delete afterwards
gh auth login --hostname github.com --with-token < ~/pat.txt && shred -u ~/pat.txt
gh auth status          # Token: ghp_… and Token scopes: … 'repo' …
```

Add `workflow` to `--scopes` when builders touch `.github/workflows`. A classic token reaches **every** repository
the account can, which is the reason to prefer the fine-grained one on a shared or long-lived box — pick per
box, not per project.

### Fine-grained PAT — narrower, one extra permission to remember

Create it as the account that owns (or can write to) the repository, then:

- **Resource owner** — the owner of the repository. A token made for your user account does not reach an
  organization's repositories.
- **Repository access** — "Only select repositories", and select the one this box drives.
- **Repository permissions** — `Contents: Read and write`, `Pull requests: Read and write`,
  `Issues: Read and write`. `Metadata: Read` is added automatically. Missing `Contents: write` is exactly the
  refused merge above.
- **Organization approval** — if the org requires it, the token stays pending and reads only public resources
  until an admin approves; the symptom is a 404 on a private repository, not a 403.

The Checks API is documented for GitHub App tokens only. In practice a fine-grained PAT has read
`check-runs` on this repo, and the read fails loudly if it ever stops: `checks` is called inside
`ChangeRequests` (`internal/forge/github.go:318`), so a refusal surfaces as a forge error on the project read and
no change request is planned at all — never as a silent merge without CI.

## Set it up on a new box

```sh
gh auth status                      # which accounts exist, which one is active, which token kind
gh auth switch --hostname github.com --user <account>   # when more than one account is logged in
gh auth login --hostname github.com --git-protocol ssh --scopes repo
glab auth login                     # GitLab: paste the `api` token, or GITLAB_TOKEN
```

With more than one account logged in, **the active one is the one `act` uses** — a box carrying a work account and
a personal account will merge as whichever `gh auth status` marks `Active account: true`.

## Verify, in this order

```sh
gh api user --jq .login                                  # 1. the token authenticates at all
gh api -i user | grep -i x-oauth-scopes                  # 2. classic (scopes listed) or fine-grained (empty/absent)
gh api repos/<owner>/<repo> --jq .permissions            # 3. push:true — necessary, not sufficient for merge
gh api repos/<owner>/<repo>/commits/<sha>/check-runs >/dev/null   # 4. the merge gate's CI read answers
```

`push:true` on step 3 comes from the account's role, not from the token's permission set: a fine-grained token
without `Contents: write` still reports `push:true` and still refuses the merge. The only proof that a token can
merge is a merge, so do not manufacture one — fix the permission, then let the next tick do it.

`./install.sh --verify` proves the *read* path (`gh api user`). The app's write preflight probes a comment and —
since #329 — a merge: a squash merge of an already-merged pull request, which cannot merge again, so a token
without `Contents: write` is refused there before GitHub reaches the merged-head check, and the check names the
missing permission. A repository with no already-merged pull request to aim at reads as an honest unknown, never
green. Treat a first merge on a new box as the real test anyway, and read the `act-skip` line if it fails.

## After the fix

Nothing else to do. `gh` re-reads the credential on every call, `act` retries a permission refusal on the next
tick by itself, and a permission refusal never ends a dispatch — the change request stays open and merges the
moment the token allows it. No restart, no re-dispatch, no re-marker.

## Symptom → cause

| Symptom | Cause |
|---|---|
| `permission: merge: … (mergePullRequest)` on a fine-grained token | `Contents: write` missing |
| `permission: ready` / `permission: comment` | `Pull requests: write` / `Issues: write` missing, or the role is Reader |
| Forge error on the project read naming `check-runs` | the credential cannot use the Checks API (fine-grained, or an app installation without Checks: read) |
| 404 on a private repository the account can see in a browser | fine-grained token with the wrong resource owner, repository not selected, or org approval pending |
| Everything works until a merge, then `failed to run git: not a git repository` | not a permission problem: a `gh` verb without `-R <slug>` (the service starts in `$HOME`) |
| Works by hand, fails in the unit | the unit's environment differs from your shell — `GH_TOKEN`/`GITLAB_TOKEN` in it overrides the keyring, and the unit's `PATH` decides which `gh` runs |
