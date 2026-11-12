# Tasks — a project names its forge host

A box is ticked only with the command that proved it, quoted in the pull request.

## 0. Ground rules

**Config key:** `forge_host` on a project (host[:port], no scheme, no path). `repo_url` already exists and keeps its
meaning (clone/setup/display). Resolution order: `forge_host` → `repo_url`'s host → `gitlab.com` (GitLab) / empty
(GitHub).

**Go names:** `config.Project.ForgeHost string \`yaml:"forge_host"\``; `config.ForgeHost(p Project) (host, source
string, err error)` where `source` is `"forge_host"`, `"repo_url"` or `"default"`; `config.Sources["projects"]`
unchanged.

**Adapter:** `forge.NewGitLab(project, host string, runner Runner, budget *Budget)`; `forge.CLIRunner.Env []string`
applied as `append(os.Environ(), r.Env...)` for the child only. `GITLAB_HOST` is the only variable this change sets.

**Error identities:** a contradiction or a GitHub host answers `*config.ValidationError` with the field
`forge_host`, so the loader and the board's `422` say the same sentence.

**Normalisation:** trim space, drop a trailing `/`, lower-case. A value containing `://` or `/` is refused with
`forge_host must be a host, not a URL`.

**Build gate:** `go build ./... && go vet ./... && go test ./...`; `npm run typecheck && npm run lint && npm run
build` only if `src/` changed; `openspec validate --all --strict`.

## 1. Config — resolve and validate (one issue: `internal/config`)

- [ ] 1.1 `forge_host` on `Project`, normalised and carried into the effective config. Verify:
      `go test ./internal/config -run TestForgeHost` — explicit, derived from `repo_url`, neither, and the GitHub case.
- [ ] 1.2 `ForgeHost(p)` resolver returning host + source, used by `--print-config`. Verify: the fixture run quoted
      in the PR shows `host: gitlab.horisen.org (forge_host)` and `host: gitlab.com (default)`.
- [ ] 1.3 Validation: contradiction between `forge_host` and `repo_url`'s host, and `forge_host` on `forge: github`,
      both answer a `FieldError` on `forge_host`; a URL-shaped value is refused with the sentence above. Verify: the
      validation table gains three rows; the loader refuses the same fixtures.
- [ ] 1.4 The existing "repo_url is not set" warning is kept verbatim — a self-hosted project that forgot both is not
      distinguishable, and pretending otherwise would break working boxes. Verify: the warning still appears for a
      GitLab project with neither field.

## 2. Adapter and wiring (one issue: `internal/forge` + `cmd/fleet-console`)

- [ ] 2.1 `CLIRunner.Env []string` (`internal/forge/cli.go`), applied to the child's environment only, on top of
      `os.Environ()`. Verify: a test runner records `Env`; nothing changes when `Env` is empty.
- [ ] 2.2 `NewGitLab(project, host, runner, budget)` sets `GITLAB_HOST=<host>` when the host is non-empty and
      `gitlab.com`-different; argv is untouched. Verify: the `forge` tests that compare whole command strings still
      pass unmodified — that is the proof the flag-free route held.
- [ ] 2.3 Update **all three** construction sites with the resolved host: `cmd/fleet-console/main.go:99` (tick),
      `cmd/fleet-console/main.go:953` (`ForgeFor`), `internal/preflight/forge_write.go:269` (write probe). Verify:
      `rg -n 'NewGitLab\(' --glob '!*_test.go'` shows no call without a host argument, quoted in the PR.
- [ ] 2.4 Two projects, two hosts: one unit test builds both adapters and asserts each runner's `Env` names its own
      host. Verify: `go test ./internal/forge -run TestTwoHostsOneProcess`.

## 3. Board — carry both fields (one issue: `internal/web` + `src/`)

- [ ] 3.1 `ProjectRequest`/`ProjectResponse` gain `repo_url` and `forge_host`; the handler passes them through
      `config.ValidateProject` (so §1.3's errors land as `422` on `forge_host`). Verify: `httptest` table with the
      contradiction and the GitHub cases.
- [ ] 3.2 `src/lib/fleet.ts` types + `ProjectsScreen.tsx`: two inputs under the forge select, with the hint that
      `forge_host` defaults to `repo_url`'s host and is read only for GitLab. Verify: create + edit a project from the
      dialog, then `--print-config` on the same file shows both fields.
- [ ] 3.3 Rebuild the embedded board in the same commit (`npm run build`, copy `dist/` into `internal/webui/dist/`).
      Verify: `make build`, `GET /` answers 200 from the built binary.

## 4. Preflight and docs (one issue: `internal/preflight` + `docs/`)

- [ ] 4.1 `forge_write`'s detail names the probed host (`API host: <host>`), and an unregistered host fails with
      glab's own sentence plus `glab auth login --hostname <host>`. Verify: test with a fake runner returning glab's
      refusal, asserting both the state and the detail text.
- [ ] 4.2 `docs/quickstart.md`: the self-hosted block (`forge_host`, `repo_url`, the `glab auth login --hostname`
      prerequisite) and one sentence that omitting both keeps `gitlab.com`. `README.md`: `forge_host` in the project
      field list. Verify: `git diff`, and every claim checked against the shipped field names with `rg`.

## 5. Gate

- [ ] 5.1 `go build ./... && go vet ./... && go test ./...` — full output in the PR.
- [ ] 5.2 `openspec validate forge-host --type change --strict` and `openspec validate --all --strict`, totals
      quoted.
- [ ] 5.3 `make build`, then a spare-port instance with a throwaway config carrying a self-hosted GitLab project:
      `--print-config` prints the resolved host, and a contradictory pair refuses to start. Never on 8787.
