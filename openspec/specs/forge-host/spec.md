# forge-host Specification

## Purpose
Which host a project's forge lives on: resolved once per project and carried into every GitLab call, the preflight probe and the printed configuration.

## Requirements

### Requirement: A project names the host its forge lives on
A project SHALL accept an optional `forge_host` (host[:port], no scheme or path), and the effective config SHALL
resolve one host per project: `forge_host`, else `repo_url`'s host, else `gitlab.com` for GitLab and empty for
GitHub.

#### Scenario: An explicit host
- **WHEN** a GitLab project sets `forge_host: gitlab.horisen.org`
- **THEN** the resolved host is `gitlab.horisen.org` and `--print-config` prints it

#### Scenario: The host comes from the clone URL
- **WHEN** the project sets no `forge_host` but `repo_url` is `https://gitlab.horisen.org/group/app.git`
- **THEN** the resolved host is `gitlab.horisen.org` with no new key required

### Requirement: An unspecified host keeps today's behaviour
A GitLab project with neither `forge_host` nor `repo_url` SHALL resolve to `gitlab.com`, and a GitHub project SHALL
resolve to no host at all.

#### Scenario: An untouched gitlab.com project
- **WHEN** a project sets only `forge: gitlab` and `repo: group/app`
- **THEN** the resolved host is `gitlab.com` and the loader's behaviour is unchanged

### Requirement: Two different hosts are refused at load
A `forge_host` that contradicts the host of `repo_url` SHALL be a load error naming `forge_host`, and nothing SHALL
start.

#### Scenario: A typo in one of the two
- **WHEN** `repo_url` names `gitlab.horisen.org` and `forge_host` names `gitlab.example.org`
- **THEN** the loader answers a field error on `forge_host` and the service does not start

### Requirement: GitHub refuses a forge host
`forge_host` on a `forge: github` project SHALL be a load error naming `forge_host`, because GitHub's host is the
`gh` CLI's own setting.

#### Scenario: A host on a GitHub project
- **WHEN** a project sets `forge: github` and `forge_host: github.example.org`
- **THEN** the loader answers a field error on `forge_host`

### Requirement: Every GitLab call goes to that host
The GitLab adapter SHALL run every `glab` call with `GITLAB_HOST` set to the project's resolved host, per adapter,
so two projects on two instances coexist in one process.

#### Scenario: Two instances on one box
- **WHEN** one project resolves `gitlab.example.org` and another `gitlab.com`
- **THEN** each adapter's environment carries its own `GITLAB_HOST` and neither sees the other's

#### Scenario: The argv does not change
- **WHEN** the adapter builds any `glab` call for a self-hosted project
- **THEN** the recorded argv is the same as for `gitlab.com`, and the host travels only in the environment

### Requirement: Preflight probes the configured host and names it
The `forge_write` check SHALL probe the project's resolved host and SHALL name that host in its detail; when `glab`
refuses to send the credential, the check SHALL fail quoting glab's sentence and the `glab auth login --hostname`
remedy.

#### Scenario: An unregistered host
- **WHEN** the host is not registered with `glab auth login`
- **THEN** `forge_write` fails, its detail names the host and the login command to run

### Requirement: The board sets and edits both fields
`GET`/`POST`/`PUT` on `/api/projects` SHALL carry `repo_url` and `forge_host`, and a project created from the board
SHALL round-trip through `--print-config` unchanged.

#### Scenario: A self-hosted project added from the board
- **WHEN** the operator submits `forge: gitlab`, `repo_url` and `forge_host`
- **THEN** the answer contains both and `fleet-console --print-config` prints the same host

### Requirement: The resolution is visible in print-config
`--print-config` SHALL print the resolved host per project with its source, so an operator can prove which instance
the running process will talk to.

#### Scenario: Reading the effective host
- **WHEN** one project derives its host from `repo_url` and another sets it explicitly
- **THEN** each line prints its host and where the value came from
