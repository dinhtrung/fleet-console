## ADDED Requirements

### Requirement: A project reports four preflight checks
The app SHALL produce, on demand, a preflight report for a project with exactly four checks — git URL, base
directory, rig, runtime — each with a state of `ok`, `warn`, `fail` or `unknown`, a detail naming the fact found,
and a fix.

#### Scenario: A project whose repository does not answer
- **WHEN** `git ls-remote --heads <url>` fails for a registered project
- **THEN** the `git_url` check is `fail`, its detail carries the last line of that command's error, and its fix
  names the command and the forge login

#### Scenario: A project that is ready
- **WHEN** all four checks pass
- **THEN** the report's `ready` field is true and every state is `ok` or `warn`

### Requirement: A check that cannot run is unknown, never ok
When a check cannot determine its answer — the command is missing, times out, or the project does not exist yet —
the app SHALL report `unknown` with the command that would answer it.

#### Scenario: The rig of a project that is still a draft
- **WHEN** preflight runs against a draft that has no row yet
- **THEN** the `rig` check is `unknown` and explains that the rig is created with the project

### Requirement: The base directory check distinguishes fixable from broken
The app SHALL report a missing base directory as `fail` with a `mkdir_base` step, an empty one as `fail` with a
`clone` step, and a directory that is a checkout of a **different** repository as `fail` with no step and the
diagnosis.

#### Scenario: The wrong repository in the right place
- **WHEN** `paths.main` exists as a git checkout whose `origin` is not the project's repository
- **THEN** the check is `fail`, its detail names both origins, and no setup step is offered

### Requirement: The rig check names which condition failed
The rig check SHALL report `ok` only when the rig name exists in the specs, the rig is up, its seat count equals
builders plus one, and every seat's runtime matches the project; otherwise its detail SHALL name the failing
condition and the fix SHALL offer the steps that apply.

#### Scenario: A rig that is not up
- **WHEN** the rig spec exists but the rig is not running
- **THEN** the check is `fail`, the detail says the rig is not up, and the fix offers `up_rig`

### Requirement: The runtime is one of four, chosen by the project
The app SHALL accept exactly `pi`, `omp`, `codex` and `claude` as a project's runtime, case-insensitively, SHALL
keep `omp` as the default only when the project says nothing, and SHALL refuse any other value at config load and
at rig render with an error naming the four.

#### Scenario: A typo in the runtime
- **WHEN** a project row says `runtime: claud`
- **THEN** config load fails naming the field and the four accepted values, and no rig is rendered

### Requirement: The runtime's login belongs to the operator
The app SHALL report whether a credential for the chosen runtime exists on this host and SHALL show the command
that performs the login, and SHALL NOT run a login, prompt for a credential, accept one over the API, or return
one in any response.

#### Scenario: Nobody has logged in
- **WHEN** the chosen runtime has no credential on this host
- **THEN** the runtime check is `fail`, its hint is the login command, and no setup step is offered for it

#### Scenario: The runtime list is read by the client
- **WHEN** the client calls `GET /api/runtimes`
- **THEN** it receives, per runtime, support, a boolean for credential presence, the login hint and the login
  documentation line — and no credential value

### Requirement: Setup runs only allowlisted, idempotent steps
`POST /api/projects/{key}/setup` SHALL accept only the step ids `mkdir_base`, `clone`, `apply_rig` and `up_rig`,
SHALL run them in that order, and SHALL report for each `done`, `skipped` or `failed` with the command it ran.

#### Scenario: Running setup twice
- **WHEN** the same setup request is sent twice on a project that is already set up
- **THEN** every step answers `skipped` the second time and nothing is cloned, rendered or launched again

#### Scenario: The clone step on a non-empty directory
- **WHEN** `paths.main` is non-empty
- **THEN** `clone` answers `skipped` and never runs `git clone`

#### Scenario: A step fails
- **WHEN** `clone` fails
- **THEN** the remaining steps are not attempted and the response carries `clone` as `failed` with its output

### Requirement: Setup never touches credentials
No setup step SHALL run a login, write a credential file, or print a secret; every step SHALL be recorded in the
audit log with its command and the last line of its output.

#### Scenario: The audit trail of a full setup
- **WHEN** every allowlisted step has run
- **THEN** the audit rows name four commands and none of them contains `login`, `auth`, or a token variable

### Requirement: The preflight is visible where the project is
The create dialog and the project view SHALL both show the four checks with their states, details and fixes, and
SHALL offer a single action that runs every allowlisted step that applies.

#### Scenario: Creating a project against a repository that answers
- **WHEN** the operator fills the dialog with a reachable URL and a new path and submits
- **THEN** the project is created with `warnings` naming `base_dir` and `rig`, and the view offers to make the
  directory, clone, and launch the rig

#### Scenario: A draft is checked before it exists
- **WHEN** the operator types a URL in the create dialog
- **THEN** the `git_url` check runs against the draft, and `rig` stays `unknown` until the project exists

### Requirement: A failure shows the real output
The app SHALL render a failed check's detail as the last line of the command's own output, not a paraphrase.

#### Scenario: A private repository without a credential
- **WHEN** `git ls-remote` fails because the host cannot read the URL
- **THEN** the detail contains that command's own error line, so the operator can tell a typo from a missing token

### Requirement: The registry row carries its runtime
A project row SHALL carry the runtime it runs on, written by the create form from a validated list, and the
project view SHALL show it next to the rig name.

#### Scenario: A project created for a different runtime
- **WHEN** a project is created with `runtime: claude`
- **THEN** the row, the rendered rig spec and the project view all say `claude`, and the runtime check reports
  whether this host has a Claude credential
