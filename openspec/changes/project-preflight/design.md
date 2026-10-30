# Design — preflight, setup, runtime

## 1. Four checks, one shape

```go
// internal/preflight
type State string // "ok" | "warn" | "fail" | "unknown"

type Check struct {
    ID     string  // "git_url" | "base_dir" | "rig" | "runtime"
    Label  string
    State  State
    Detail string  // the fact: what was found, with the failing output's last line
    Fix    Fix     // how a human fixes it
}
type Fix struct {
    Command string // exact shell command, copy-pasteable; "" when none applies
    Step    string // allowlisted setup step id, "" when the app must not do it
    Why     string // one sentence, plain language
}
type Report struct {
    Project  string
    Checks   []Check
    Ready    bool     // every check is ok or warn
    Runtime  Runtime  // the chosen runtime and whether this host can authenticate it
    CheckedAt time.Time
}
type Runtime struct {
    ID          string // pi | omp | codex | claude
    Supported   bool
    AuthPresent bool   // a credential exists on this host
    LoginHint   string // the command the operator runs themselves
    LoginDoc    string // one line: where it is stored, so they know what to look for
}
```

Every check returns `unknown` rather than guessing: a check that could not run says which command would have
answered. A green lie is worse than an honest unknown, because the operator stops looking.

## 2. The checks

| Check | `ok` means | `fail` means | Fix |
|---|---|---|---|
| `git_url` | `git ls-remote --heads <url>` exits 0 within 20s | non-zero: the URL is wrong, private without a credential, or the host is unreachable — the last stderr line is the `Detail` | `git ls-remote --heads <url>` by hand, then the forge's own auth (`gh auth login`) |
| `base_dir` | `paths.main` exists, is a git checkout whose `origin` matches the project's repo | missing, empty, not a repository, or `origin` is a different repo | step `mkdir_base` (missing), step `clone` (empty) — a wrong `origin` is `fail` with the diagnosis and **no step** |
| `rig` | the rig name is in the specs, the rig is up, its seat count equals builders + 1, every seat's runtime matches | the rig is unknown, down, or has the wrong seats | step `apply_rig` (render + `rig specs sync`), step `up_rig`; a missing rig spec is the same two steps |
| `runtime` | the chosen runtime is supported **and** a credential exists on this host | unsupported value, or no credential | no step: `LoginHint` only — the app never logs in |

`rig` is one check with a `Detail` naming which of the four conditions failed, because the operator's next action
depends on it and one badge with one sentence is what they read.

## 3. The runtime enum

`pi`, `omp`, `codex`, `claude`. `omp` remains the default only when the project row says nothing — the default
stays so an existing config keeps working. Values are compared case-insensitively and stored lowercased.

- Config load: an unknown value is a **load error naming the field and the four accepted values**, because a rig
  that starts with a runtime nobody can authenticate is three wasted seats.
- `RenderRig`: refuses an unknown runtime (`render: runtime %q is not one of pi, omp, codex, claude`). Belt and
  braces, the same way it already refuses a project without a provider credential.
- The UI's picker is driven by `GET /api/runtimes`, so the client never hardcodes the list.

`GET /api/runtimes` answers, per runtime: `supported` (a binary or ref exists on this host), `auth_present` (a
credential file or env var exists), `login_hint`, `login_doc`. It never returns a credential, only a boolean.

## 4. Allowlisted setup steps

`POST /api/projects/{key}/setup` takes `{"steps": ["mkdir_base", "clone", "apply_rig", "up_rig"]}` and runs them in
that fixed order, **only** allowlisted ids, each idempotent:

| Step | Does | Idempotent because |
|---|---|---|
| `mkdir_base` | `mkdir -p <paths.main>` | exists → `skipped` |
| `clone` | `git clone <url> <paths.main>` **only** when the directory is empty or absent | a non-empty directory → `skipped`, never a second clone |
| `apply_rig` | render the spec from the project row, write it under the rig specs dir, `rig specs sync` | same rendered bytes → `skipped` |
| `up_rig` | `rig up <rig>` | already up → `skipped` |

Rules: no step runs a login, no step writes a credential, no step touches an existing non-empty directory, and
every step appends an audit row with the command it ran and its output's last line. The response is one entry per
step: `{step, state: done|skipped|failed, command, output}`. A `failed` step stops the chain — the next step would
only fail worse.

## 5. The surfaces

**New project dialog** — the same form as `project-registry-web`, then a preflight panel under it, live: as the key,
URL, path and runtime fields change, the checks re-run against the *draft* (the URL and the path can be checked
before the project exists; `rig` cannot, and says so: `unknown — the rig is created with the project`). Create is
enabled always, with the failures named in a `warnings` block, because refusing to write a half-configured project
is how the registry stays honest about what exists.

**Project view** — the checklist with four rows, a state badge, the `Detail`, and per row either "Copy command" or
"Set up this step"; one primary "Set up what the app can" button that posts the whole allowlisted list. The runtime
row shows the login hint and a "Re-check" button; it never shows a form.

**Never a silent red**: a failed check renders its `Detail` verbatim (last line of real output), because "fatal:
could not read Username for 'https://github.com'" tells the operator what a cheerful "auth failed" does not.

## 6. Tests

| Promise | Test |
|---|---|
| Each check has four states | table test per check with a fixture for ok/warn/fail/unknown |
| Unknown is not ok | a check whose command cannot run → `unknown`, `Fix.Command` names it |
| Clone is never a second clone | `clone` against a non-empty dir → `skipped`, `git clone` absent from the audit row |
| Steps stop on failure | `clone` fails → `apply_rig` not attempted, response has two entries |
| Login is never run | the whole setup chain audited: no command contains `login`, `auth`, or the token env name |
| Enum at both ends | config load rejects `gpt`, `RenderRig` rejects `gpt`, both name the four values |
| Auth is a boolean | `GET /api/runtimes` body has no string longer than the login hint (a credential would show up here) |
| Draft preflight | a draft with a bad URL → `git_url` fail before any project row exists |
