## ADDED Requirements

### Requirement: The shell renders no demo scaffolding

The console SHALL NOT render a scope switcher, demo identity or account block, permission-filtered navigation,
failure/latency/reset control, synthetic-data notice, demo footer or demo attribution copy.

#### Scenario: The rail

- **WHEN** the board loads
- **THEN** the rail shows navigation and real board controls only — no Scope dropdown, no database menu, no account
  block

#### Scenario: The footer

- **WHEN** any screen is open
- **THEN** no line says the board is synthetic, demo, authored or not a real service

### Requirement: The console ships no mock data

Every shipped module SHALL read data from `src/lib/fleet.ts` or a local display preference. `src/mock/**` and
`public/mock/**` SHALL NOT exist, and nothing SHALL import a path reaching `/mock`.

#### Scenario: A clean build

- **WHEN** `npm run build` runs
- **THEN** no module resolves a path under `src/mock`, and the built board reads the real API

#### Scenario: The tree

- **WHEN** the source tree is listed
- **THEN** neither `src/mock` nor `public/mock` is present

### Requirement: No identity or permission fiction

The console SHALL NOT render a user name, email or role-derived permission gate. The rail SHALL list every shipped
surface; the server's token SHALL remain the only authorization.

#### Scenario: Every surface is reachable

- **WHEN** the board loads
- **THEN** every shipped route appears in the rail and no account block is drawn

#### Scenario: The token is refused

- **WHEN** a request reaches the server without a valid token
- **THEN** the server answers its auth error and the screen shows its error state; the client did not pre-hide the
  surface

### Requirement: A guard refuses demo scaffolding

A repository check SHALL fail when a demo token or a mock import appears in `src/`, `index.html` or `public/`, and
the web CI job SHALL run it. The check SHALL name every offending file and line.

#### Scenario: Scaffolding returns

- **WHEN** a change adds `import … from "../mock/api"` or the string `Synthetic data`
- **THEN** the check exits non-zero naming that file and line

#### Scenario: A clean tree

- **WHEN** the tree carries no demo token and no mock import
- **THEN** the check exits 0 and the web job passes
