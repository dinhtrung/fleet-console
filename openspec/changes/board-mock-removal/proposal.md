# Proposal — ship only the surfaces that are real

## Why

`src/router.tsx` registers eleven screens above and beyond the five real ones
(`src/screens/{Dashboard,UserList,UserDetail,RoleList,RoleDetail,OrganizationList,OrganizationDetail,SessionList,ApiKeys,FeatureFlags,Settings}Screen.tsx`),
and every one of them reads `src/mock/api`: seeded, browser-storage, no backend. The rail
(`src/shell/AppShell.tsx:22-35`) links only Board, Decisions, Seats, Projects, Culture — so the eleven are reachable
by URL only, which makes them worse than dead code: a URL a colleague pastes in a ticket renders a plausible
organisation with users and roles that do not exist.

Two of the eleven are being made real by other changes in this batch (`audit-surface` replaces Audit's mock reads;
`settings-surface` replaces Settings'). The other nine have no backing at all: there is no user store, no role
model, no session table, no API keys, no feature flags (`internal/store/schema.go` has project, repo, item,
dispatch, verdict, event, snapshot, mirror_meta).

The board's credibility is the product. A surface that cannot be true must not be shipped.

## What changes

- Delete the nine mock screens, their `src/router.tsx` entries, and the `src/mock` functions only they used
  (`listUsers`, `listRoles`, `listOrganizations`, `listSessions`, `createApiKey`, `listFeatureFlags`, the dashboard
  summary, …).
- Add `/audit` and `/settings` to the rail once their real reads land, with their own permissions.
- Add a guard: a CI step that fails when a screen under `src/screens` imports `../mock`, so the scaffold cannot
  creep back in.
- Keep `src/auth/session.tsx`'s mock identity for now — it is the shell's own identity, not a screen, and it is
  named as the remaining gap in the docs rather than silently deleted.

## Losses, named

- **No users/roles/organisations UI at all.** Multi-user is not a feature of this product yet; the board is a
  loopback operator console with one bearer token. If multi-user ever lands, those screens are a design problem to
  redo, not code to recover — the deleted screens talked to a mock with its own shape.
- **Deep links to those URLs 404.** Say so in the release note; no redirects, because a redirect to the board
  pretends the page exists.

## Capabilities

### New Capabilities

- `board-navigation`: which routes the shipped board may have, the rule that every screen reads the real API, and
  the check that keeps it that way.

## Impact

- `src/router.tsx`, `src/shell/AppShell.tsx`, `src/screens/*` (9 files deleted), `src/mock/api.ts` and
  `src/mock/types.ts` (functions and types only those screens used).
- `scripts/check-no-mock-screens.mjs` + a `package.json` script + a CI step.
- `PRODUCT.md` — the surface list, and the one documented mock that remains (identity).
