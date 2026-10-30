# Proposal — a project that cannot work should say so before it is created

## Why

`project-registry-web` lets the board write a project into `config.yaml`. Written, not working. Three things have
to be true before the first item can dispatch, and the app knows none of them at write time:

1. **the Git URL answers** — a typo or a token-less clone fails at the first `git fetch`, three ticks later, as
   `fatal: not a git repository` or a 403 in a log nobody is reading;
2. **the base directory exists** — `paths.main` missing means the worktree add fails inside the dispatch, and the
   only trace is a skip line;
3. **the rig exists, runs, and its seats can log in** — a rig name that is not in the specs, a rig that is down,
   or a runtime nobody signed into produces three idle seats and no explanation.

And the runtime itself is a hidden constant: `RenderRig` defaults it to `omp`, so a project that wants `claude`,
`codex` or `pi` has no way to say so from the board, and no way to learn that the chosen runtime needs the
operator's own login — which the app must never perform, never request, and never store.

## What changes

- **A preflight per project**, computed on demand and shown wherever the project is: the create dialog and the
  project view. Four checks — git URL, base directory, rig, runtime — each `ok` / `warn` / `fail` / `unknown`,
  each with the exact command that fixes it.
- **A "Set up what the app can" action**: the automatable subset runs as allowlisted steps — make the base
  directory, clone the repository into it, render and apply the rig spec, launch the rig — each idempotent, each
  audited, each reporting the command it ran.
- **Runtime is a choice**: `pi`, `omp`, `codex`, `claude`, validated at write time and again at launch, surfaced in
  the rig spec and in the form.
- **Login is the operator's**: the preflight reports whether a credential is present and prints the login command;
  the app never prompts for one, never runs `login`, and never reads a credential into a response body.

## Capabilities

### New Capabilities

- `project-preflight` — the four checks, their states, the fix text, the allowlisted setup steps and their
  idempotency, the runtime enum, and the credential rule (report, never handle).

### Modified Capabilities

- `project-registry` — creating or viewing a project shows its preflight; a project that fails a check can still be
  written, but it is written with its failures named rather than silently.
- `rig-lifecycle` — the runtime is chosen, not defaulted: `omp` stays the default only when the project says
  nothing, and an unsupported value is refused at config load and at render.

## Impact

- `internal/preflight`: the checks (each with a timeout), the states, and the fix text. No mutation here.
- `internal/setup`: the allowlisted steps, run one at a time, idempotent, audited; it may create directories and
  clone, and nothing else.
- `internal/web`: `GET /api/projects/{key}/preflight`, `POST /api/projects/{key}/setup`, `GET /api/runtimes`.
- `internal/config` + `internal/rig`: `runtime` in the project row, the enum, and the validation errors.
- `src/`: the checklist component, the setup button, the runtime picker, and the login panel.
- Depends on `project-registry-web` (the project routes and the dialog must exist first).

## Non-goals

- No credential entry, no OAuth flow, no token storage: a login is a human act on the box, and the message says
  which command performs it.
- No repair of a repository that exists but is broken (wrong origin, detached worktrees): that is reported as a
  `fail` with the diagnosis, not rewritten under the operator's feet.
- No automatic creation of a remote repository, and no forge credentials beyond what `gh`/`glab` already hold.
