# Tasks — preflight, setup, runtime

A box is ticked only with the command that proved it, quoted in the pull request.

## 1. The checks (issue: preflight)

- [ ] 1.1 `internal/preflight`: `State`, `Check`, `Fix`, `Report`, `Runtime`, and `Run(ctx, project, eff)` returning the four checks. Every check has a 20s bound and returns `unknown` with the answering command when it cannot decide. Verify: `go test ./internal/preflight` table test with a fixture per state per check.
- [ ] 1.2 `git_url`: `git ls-remote --heads <url>`, `origin` compared for `base_dir`, both with the failing stderr's last line as `Detail`. Verify: a local bare repository as the ok case and a nonexistent path as the fail case; assert the detail is the command's own last line.
- [ ] 1.3 `base_dir`: missing → `mkdir_base`; empty → `clone`; wrong `origin` → `fail` with both origins and **no step**; not a repository → `fail` with the diagnosis. Verify: four fixtures, one assertion on the offered step each.
- [ ] 1.4 `rig`: spec present, rig up, seat count == builders+1, every seat's runtime matches; the `Detail` names the failing condition. Verify: one fixture per condition, asserting the exact detail sentence.
- [ ] 1.5 `runtime`: the enum, the credential presence check per runtime (`pi`, `omp`, `codex`, `claude`), and the login hint + documentation line. Verify: four fixtures with and without a credential in a temp HOME, plus a test that no returned string contains the credential.

## 2. Setup steps and the enum (issue: setup)

- [ ] 2.1 `internal/setup`: the four allowlisted steps in fixed order, each idempotent, each returning `{step, state, command, output}`; a `failed` step stops the chain. Verify: `go test ./internal/setup` — run the chain twice in a temp dir and assert the second run is all `skipped`.
- [ ] 2.2 `clone` refuses a non-empty directory, `mkdir_base` is a no-op when the directory exists, `apply_rig` compares rendered bytes before writing, `up_rig` treats an already-up rig as `skipped`. Verify: one test per step, asserting `git clone` never appears twice in the audit trail.
- [ ] 2.3 Audit: every step appends a row with its command and the last output line. Verify: the audit trail of a full run has four rows and none contains `login`, `auth`, or a token variable name.
- [ ] 2.4 `runtime` in the project row: `internal/config` accepts `pi|omp|codex|claude` (case-insensitive, stored lowercase, `omp` as the default when absent) and refuses anything else naming the four. `RenderRig` refuses the same. Verify: config load test and a render test with `claud`, `gpt` and an empty value.

## 3. API and the surfaces (issue: web)

- [ ] 3.1 `GET /api/projects/{key}/preflight` and `POST /api/projects/{key}/setup` inside the authenticated group, plus `GET /api/runtimes`. Bodies exactly as `design.md` §1, §3, §4; nothing that returns a credential. Verify: handler tests with a fake host state, and a test asserting `/api/runtimes` carries no value longer than a login hint.
- [ ] 3.2 Preflight of a **draft**: the same checks against a body that is not yet a project row (URL and path checkable, `rig` `unknown`). Verify: handler test with a draft body.
- [ ] 3.3 Project view: four rows with badge, detail, "Copy command", and "Set up this step" where a step exists; one primary "Set up what the app can" that posts the allowlisted list and renders per-step results. Verify: screenshots, before and after a setup run.
- [ ] 3.4 Create dialog: the runtime picker driven by `GET /api/runtimes`, the preflight panel under the form, and creation allowed with `warnings` naming the failing checks. Verify: screenshots of a create with a bad URL (git_url red, warnings listed) and of a create that succeeds with the setup offer.
- [ ] 3.5 Runtime row: credential state, login hint, "Re-check" — and no form, no input, no masked field. Verify: screenshot plus a grep of `src/` proving no password/token input exists on this surface.
- [ ] 3.6 Rebuild `internal/webui/dist` in the same commit; docs: `docs/quickstart.md` gains a "What the board checks before it dispatches" section and the four runtimes with their login commands.

## 4. Gate

- [ ] 4.1 `go build ./... && go vet ./... && go test ./...`, `npm run typecheck && npm run lint && npm run build`, then `openspec validate --all --strict`. Verify: real output quoted in the pull request.
