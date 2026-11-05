# Tasks — remove the mock surfaces

## 0. Ground rules

- Gate: `npm run typecheck && npm run build`, `npm run check:screens`, `go build ./...`,
  `openspec validate --all --strict`. Real output in the PR body.
- Blocked by `audit-surface` and `settings-surface`: Audit and Settings must read real data before this PR adds them
  to the rail.
- `src/auth/session.tsx` and the mock module it needs stay. Do not delete `src/mock` wholesale.

## 1. Delete

- [x] 1.1 Delete the nine screens and their route registrations: Dashboard, UserList, UserDetail, RoleList,
      RoleDetail, OrganizationList, OrganizationDetail, SessionList, ApiKeys, FeatureFlags (Dashboard is already
      unreachable — `path: "/"` renders `BoardScreen`).
- [x] 1.2 Remove the `src/mock/api.ts` functions and `src/mock/types.ts` types that only those screens used; the
      build must prove what is left is reachable from `auth/session.tsx`.
- [x] 1.3 Delete any component under `src/components` that only a deleted screen imported.

## 2. Rail

- [x] 2.1 `src/access/access.tsx`: declare `audit.read` and `settings.write`.
- [x] 2.2 `src/shell/AppShell.tsx`: add the two nav items.
- [x] 2.3 Evidence: a screenshot of the rail with seven items and one of `/audit` open from it.

## 3. Guard

- [x] 3.1 `scripts/check-no-mock-screens.mjs` — walk `src/screens`, fail on `from "../mock"`, print offenders; add
      `"check:screens"` to `package.json` and a CI step in `.github/workflows/ci.yml` beside `npm run lint`.
- [x] 3.2 Prove it fails: temporarily add a mock import, run the script, paste the failing output in the PR body,
      then remove it.
- [x] 3.3 `PRODUCT.md`: the surface list, the nine dead URLs in one line, and the one sentence that identity is
      still the scaffold's.
