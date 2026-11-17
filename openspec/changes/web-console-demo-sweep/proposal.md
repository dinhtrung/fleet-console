# Proposal — the web console carries no demo scaffolding

## Why

The board's screens read the real API (`src/lib/fleet.ts`), but the shell around them is still the admin-panel
demo it was scaffolded from. The leftovers are not cosmetic: two of them render a fake identity and a fake
database, and the document head says the whole console is synthetic.

Measured in the tree today:

- `src/mock/` — five modules, **1,197 lines**: `api.ts`, `seed.ts`, `accounts.ts`, `permissions.ts`, `types.ts`.
  The seed builds authored people, organizations, roles and audit rows into `localStorage["fleet-console.db"]`.
- `src/auth/session.tsx` — resolves the "operator" from `DEMO_ACCOUNTS` (`usr_owner`,
  `operator@fleet-console.local`), reads roles and permissions from the mock, and the rail prints that name and
  address as if they were the user's.
- `src/access/access.tsx` — a role→permission gate; `AppShell` filters the rail by `can(permission)`, so the
  navigation is decided by a demo role table.
- `src/shell/BoardControls.tsx` — the failure switch, latency switch and "Reset the board" menu over the mock
  database, plus an unused `SeedNotice` ("Synthetic data — N records seeded …").
- `src/shell/ScopeSwitcher.tsx` + `src/shell/scope.tsx` — a `Scope` dropdown of mock organizations. No fleet-console
  route or API reads the selected org; the control filters nothing.
- `src/components/ui/OrgMark.tsx` and `public/mock/` — eight generated org marks (128 KB), referenced only by that
  component.
- `src/shell/AppShell.tsx:200` — the footer "Synthetic demo board · every name, organization and event is authored ·
  not a real service · MIT licensed".
- `index.html:7` — the meta description is "admin-panel — an operable admin surface for users, roles,
  organizations, sessions and audit, running entirely on synthetic data." The head also clears
  `admin-panel.appearance` / `admin-panel.density`, and `public/favicon.svg` labels itself `admin-panel`.
- `src/index.css:284` — the "handover sweep" motion whose only trigger was the scope change.

The existing guard knows about half of this and blesses the other half:
`scripts/check-no-mock-screens.mjs` refuses a mock import **in screens only**, and its comment says the mock
"survives only for the identity scaffold". The owner's reading is the opposite: the identity scaffold is demo
information too.

## What changes

- The mock data layer and every consumer of it are deleted, not hidden behind a dev flag.
- The fake identity, role table and permission-filtered navigation are removed. The console renders no user; the
  server's token stays the only authorization.
- The Scope switcher, the failure/latency/reset controls, the SeedNotice and the demo footer are removed.
- The document head, the favicon label and the CSS motion are rewritten for fleet-console.
- The existing guard is widened to the whole shipped tree plus a copy scan, and runs in the web CI job, so demo
  scaffolding cannot come back silently.

## Losses, named

- **The rail loses the account block** (name + email) and the navigation loses its permission filter. The client
  shows every shipped surface and relies on the server to refuse what the token may not do.
- **No failure/latency switch.** Deliberately breaking the board from the UI was a demo affordance; a real failure
  is now reproduced by stopping the service.
- **No "reset the board"** — the real store is the SQLite database, not `localStorage`.
- **No org marks** in any list that still wants one; those lists show names only until a real mark source exists.
- **The handover sweep animation is gone**; screens change without the 260 ms re-scope motion.

## Capabilities

New: none.

Modified: `board-navigation` (what the shell renders and what the guard refuses).

## Impact

- Delete: `src/mock/**`, `src/auth/session.tsx`, `src/access/access.tsx`, `src/shell/ScopeSwitcher.tsx`,
  `src/shell/scope.tsx`, `src/shell/BoardControls.tsx`, `src/components/ui/OrgMark.tsx`, `public/mock/**`.
- Edit: `src/main.tsx`, `src/shell/AppShell.tsx`, `src/components/ui/index.ts`, `index.html`,
  `public/favicon.svg`, `src/index.css`, `src/router.tsx` (comment), `package.json`, `.github/workflows/ci.yml`,
  `scripts/check-no-mock-screens.mjs` → `scripts/check-no-demo.mjs`.
- No Go change; the embedded `internal/webui/dist` is rebuilt from the same sources.
