# Design — a project names its forge host

## D1. One resolver, three answers

`config.ForgeHost(project config.Project) (string, error)`:

1. `forge_host` when set — trimmed, no scheme, no path, lower-cased host[:port];
2. otherwise the host of `repo_url` when that is set (`net/url`, `.Host`, lower-cased);
3. otherwise `gitlab.com` for a GitLab project, and empty for GitHub.

The resolved value goes into the effective config so `--print-config` prints what the process actually uses, and
every caller (tick, `ForgeFor`, preflight) reads one function. `Rejected:` resolving inside the adapter from
`repo_url` — the adapter would parse config, and `--print-config` could disagree with what it parsed.

## D2. The host travels as the adapter's environment, not as an argument

`forge.CLIRunner` gains `Env []string`, applied as `cmd.Env = append(os.Environ(), r.Env...)`. The GitLab adapter
receives its host and sets `GITLAB_HOST=<host>` on its own runner.

Measured, on this box: `GITLAB_HOST=no-such-host.invalid glab api projects/1` →
`Get "https://no-such-host.invalid/api/v4/projects/1": dial tcp: lookup …`. The variable is honoured by `glab api`,
which is the only surface the adapter uses.

`Rejected:` a `--hostname <host>` prefix on every argv — `internal/forge/writes_test.go` and `forge_test.go` assert
whole command strings, and glab's global-flag placement differs between `glab api` and the other verbs.
`Rejected:` a global `GITLAB_HOST` on the unit (today's workaround) — one host per process, so `gitlab.com` and a
self-hosted instance cannot coexist, and nothing in `--print-config` shows it.

## D3. Which combinations are errors, and which keep today's behaviour

| Project | Resolved host | Outcome |
|---|---|---|
| `forge: gitlab`, no `forge_host`, no `repo_url` | `gitlab.com` | works; the existing warning stays (a self-hosted project that forgot them looks identical) |
| `forge: gitlab`, `repo_url` on another host | that host | works — the common case needs no new key |
| `forge: gitlab`, `forge_host` ≠ `repo_url` host | contradiction | **load error** on `forge_host`: two answers to one question |
| `forge: github`, `forge_host` set | n/a | **load error** on `forge_host`: GitHub's host is `gh`'s business (`GH_HOST`) |
| `forge_host` with a scheme, path or trailing slash | normalised, or refused when unparsable | `https://x/y` is a URL, not a host — the field says host |

The contradiction check is the one that prevents a silent split: `repo_url` drives clone and `forge_host` drives the
API, so a typo in either would clone from one instance and merge in another.

`Rejected:` requiring `repo_url` on every GitLab project — it would refuse working gitlab.com projects on upgrade.

## D4. Three construction sites, one host

`NewGitLab(project, host, runner, budget)` is called from:

- `cmd/fleet-console/main.go:99` — the mirror's tick reader;
- `cmd/fleet-console/main.go:953` — `ForgeFor`, the board's verdict writer;
- `internal/preflight/forge_write.go:269` — the token-write probe.

All three take the resolved host. Missing one is how a box ends up with a green preflight and a mirror that talks to
`gitlab.com`; the epic's acceptance lists all three by name.

`Rejected:` a package-level default host that any of them could fall back to — a silent fallback is exactly the bug.

## D5. Preflight says which host it probed

`forge_write`'s detail names the host it used (`API host: gitlab.horisen.org`) and, when glab refuses to send the
credential, quotes glab's own sentence plus the remedy `glab auth login --hostname <host>`. The probe keeps its
meaning: it asks whether the token can write, not whether the host exists.

`Rejected:` a sixth "forge_host" check — the probe already talks to the host, so a second check would test the same
fact twice and could disagree with the first.

## D6. The board writes the same two fields it reads

`ProjectRequest` / `ProjectResponse` gain `repo_url` and `forge_host`; the dialog renders both under the forge
select, with a hint that `forge_host` defaults to `repo_url`'s host and is only read for GitLab. Round-trip is the
test: a project created by the board must be accepted by `--print-config` unchanged.

`Rejected:` file-only configuration — the registry has been board-writable since `project-registry-web`, and a field
the board cannot set sends the operator back to ssh for a one-word change.

## Tests that prove the promises

| Promise | Test |
|---|---|
| Derivation order | table: explicit host, repo_url host, both absent (gitlab), github |
| Contradiction refused | `forge_host`/`repo_url` disagree → field error naming `forge_host`, nothing written |
| GitHub refuses the field | `forge: github` + `forge_host` → field error |
| The adapter uses the host | fake runner records `Env` containing `GITLAB_HOST=…`; argv unchanged |
| Nothing changes for gitlab.com | no host anywhere → `Env` empty, same argv as today |
| Two projects, two hosts | two adapters in one process, each recording its own `GITLAB_HOST` |
| Board round-trip | POST with `repo_url` + `forge_host` → `--print-config` prints both; edit changes them |
| Preflight names the host | probe detail contains the host; unregistered host → fail with the `glab auth login --hostname` remedy |
