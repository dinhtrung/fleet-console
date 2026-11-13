# Proposal — a project names the forge host it lives on

## Why

A self-hosted GitLab cannot be driven today, and the failure is silent in the worst way:

- `forge` takes exactly two values (`github`, `gitlab`, `internal/config/config.go:756`) and no field names a host.
  `forge.NewGitLab(project, runner, budget)` takes only the path (`internal/forge/gitlab.go:23`), so every
  `glab api` call resolves its host from the environment: `gitlab.com` unless `GITLAB_HOST` is exported for the whole
  service.
- `repo_url` exists and is used — but only for clone, setup and display (`internal/web/preflight.go:413`,
  `internal/setup/setup.go:210`). The loader already knows this is the wrong place to stop:
  `project %q: repo_url is not set; deriving https://gitlab.com/%s — set repo_url for a self-hosted GitLab`
  (`config.go:513-517`) is a **warning**, and nothing acts on it.
- So a self-hosted project mirrors, plans and merges against the wrong instance. Preflight's `git_url` check goes
  green because it clones from `repo_url`, while `forge_write` and every tick's API read answer 404 from
  `gitlab.com`. The operator learns it from a `stale` project with no items.
- The only working knob today is `GITLAB_HOST=<host>` in the unit's environment file: it is global (one host per
  process, no mixing `gitlab.com` with self-hosted), invisible in `--print-config`, and it requires that host to be
  registered with `glab auth login --hostname <host>` or glab refuses to send the token at all — measured:
  `! GITLAB_TOKEN is set, but "<host>" is neither the default host nor a configured host, so glab did not send the
  credential to it.`

## What changes

- A project may name `forge_host: gitlab.horisen.org`. When it is absent it is derived from `repo_url`'s host; when
  both are absent a GitLab project stays on `gitlab.com`, exactly as today.
- The GitLab adapter carries the host and every `glab` call runs with `GITLAB_HOST=<host>` in its own environment —
  per adapter, so two projects on two instances coexist on one box.
- `host` on a GitHub project, and a `forge_host` that contradicts `repo_url`'s host, are load errors with a field
  error, not warnings. The existing "repo_url is not set" warning stays: a self-hosted project that forgot it is
  indistinguishable from a gitlab.com one, and pretending otherwise would break working boxes.
- The board's project form accepts `repo_url` and `forge_host`, so the registry is configurable without ssh.
- Preflight's `forge_write` probes the resolved host and names it in the check's detail, so "wrong host" is a red
  check with a remedy instead of a 404 nobody reads.
- `--print-config` reports the resolved host per project.

## Losses, named

- **A host is a project property, not a box property.** Two projects on one instance repeat the host in the file;
  that is the price of mixing instances, and the alternative (a global default with per-project exception) has the
  same line count and one more indirection.
- **`glab` still owns the credentials.** The app passes a host and never a token; an unregistered host fails with
  glab's own message, surfaced by preflight rather than pre-empted.
- **One more field two surfaces must agree on.** The handler, the loader and the dialog each have to know
  `forge_host`; the round-trip test is what keeps them honest.

## Capabilities

### New Capabilities

- `forge-host` — a project names the GitLab instance it lives on, and every read, write and probe goes there.

## Impact

- `internal/config`: `forge_host` on `Project`, a `ForgeHost(project, home)` resolver, validation rules,
  `--print-config` output.
- `internal/forge`: `CLIRunner.Env`, `NewGitLab(project, host, runner, budget)`.
- `cmd/fleet-console/main.go` (two construction sites) and `internal/preflight/forge_write.go` (the write probe).
- `internal/web/projects.go`, `src/lib/fleet.ts`, `src/screens/ProjectsScreen.tsx`, `internal/webui/dist`.
- Docs: `docs/quickstart.md` (the self-hosted block), `README.md` (the project field list).

## Out of scope

- GitHub Enterprise (`gh` resolves its own host from `GH_HOST`; a second host story for GitHub is a separate change).
- Per-project credentials, proxy settings or TLS configuration.
- Making `repo_url` mandatory for GitLab.
