# Tasks — the web console carries no demo scaffolding

## 0. Ground rules

- **Build gate:** `npm run typecheck && npm run lint && npm run build`, plus
  `CGO_ENABLED=0 go build -trimpath -tags embedboard ./cmd/fleet-console` and `go test ./...`. The web job in
  `.github/workflows/ci.yml` must run the new guard.
- **Fixed vocabulary — do not invent a second name:**
  - Guard: `scripts/check-no-demo.mjs`, npm script `check:demo`, CI step `npm run check:demo`.
  - Token list, case-insensitive and word-bounded where noted: `synthetic`, `demo`, `admin-panel`,
    `not a real service`, `authored`, and any import path reaching `/mock`.
  - "Demo scaffolding" means exactly: the mock modules, the demo identity/role model, the scope switcher, the
    failure/latency/reset controls, the SeedNotice, the demo footer, the mock org marks and the demo copy in the
    document head/CSS.
- **Deletion allowlist:** `src/mock/`, `src/auth/session.tsx`, `src/access/access.tsx`,
  `src/shell/ScopeSwitcher.tsx`, `src/shell/scope.tsx`, `src/shell/BoardControls.tsx`,
  `src/components/ui/OrgMark.tsx`, `public/mock/`. Nothing under `src/screens/` or `src/lib/` is deleted.
- **Keep:** `fleet-console.density`, the toast provider, every real screen and `src/lib/fleet.ts`.
- **Stop and report instead of inventing.** If a shipped screen imports `src/mock`, do not delete the screen and do
  not wire it to fixtures: stop and write the finding on the issue — that is a fake screen, a separate defect. If a
  deletion breaks a real screen's type, fix the import to the real client, never the other way.

## 1. Remove the demo shell (T1)

- [x] 1.1 `src/main.tsx`: drop `AuthProvider` and `ScopeProvider`; keep `QueryClientProvider` and
      `AppToastProvider`.
- [x] 1.2 `src/shell/AppShell.tsx`: drop `useAuth`, `useAccess`, `BoardControls`, `ScopeSwitcher` and the
      `IdentityBlock` function; remove the rail's scope strip and the mobile header's scope span; render every nav
      item unconditionally (delete `NavItem.permission` and the `can(...)` filter); remove the whole `<footer>`
      block.
- [x] 1.3 Delete `src/auth/session.tsx`, `src/access/access.tsx`, `src/shell/ScopeSwitcher.tsx`,
      `src/shell/scope.tsx`, `src/shell/BoardControls.tsx`. Fix every import a delete leaves behind.
- [x] 1.4 Evidence: `npm run typecheck`, `npm run lint`; `grep -rn "useAuth\|useAccess\|ScopeSwitcher\|BoardControls" src`
      returns nothing.

## 2. Delete the mock layer and its assets (T2 — blocked by T1)

- [x] 2.1 `rm -rf src/mock public/mock`; delete `src/components/ui/OrgMark.tsx` and its export in
      `src/components/ui/index.ts`.
- [x] 2.2 Grep for surviving references: `grep -rn "/mock\|mock/" src` must return nothing outside comments that
      2.2 also fixes.
- [x] 2.3 Evidence: `npm run typecheck && npm run build`; `find src public -iname '*mock*'` prints nothing;
      `git grep -n "fleet-console.db" -- src public` prints nothing. The unscoped grep also matches the SQLite
      file name the Go service and its docs use; the demo localStorage key is gone from the web tree.

## 3. Rewrite the document head and the demo copy (T3)

- [x] 3.1 `index.html`: replace the meta description with a fleet-console sentence (no "admin-panel", no
      "synthetic"); remove the `admin-panel.appearance` / `admin-panel.density` cleanup lines; keep the
      `fleet-console.density` read and the `data-density` attribute.
- [x] 3.2 `public/favicon.svg`: `aria-label="fleet-console"`.
- [x] 3.3 `src/index.css`: delete the `.sweep` rule and the `@keyframes handover-sweep` block. `src/router.tsx`:
      drop the "permission gate" sentence. Any comment that says demo, synthetic or admin-panel is rewritten or
      removed.
- [x] 3.4 Evidence: `grep -rniE "synthetic|admin-panel|not a real service|authored" index.html public/favicon.svg src`
      prints nothing (this is the manual check until T4's guard exists). T3 landed before T2; T2 then deleted the
      last four matching lines with `src/mock/**` and `src/components/ui/OrgMark.tsx`, and the command is clean.

## 4. A guard that refuses demo scaffolding (T4 — blocked by T1–T3)

- [x] 4.1 Rename `scripts/check-no-mock-screens.mjs` to `scripts/check-no-demo.mjs`. Walk all of `src/` (not only
      `src/screens`), and scan `index.html` and `public/` for the token list in §0. Report every offence as
      `file:line  text` and exit 1; exit 0 with a one-line count when clean.
- [x] 4.2 `package.json`: replace `check:screens` with `check:demo`, add it to `gate` beside the other gates.
      `.github/workflows/ci.yml`: replace the `npm run check:screens` step with `npm run check:demo` and update its
      comment.
- [x] 4.3 Prove both directions: on the clean tree the script exits 0; add one temporary line containing
      `Synthetic data` to a file under `src/`, see exit 1 naming that file and line, then remove the line.
- [x] 4.4 Evidence: paste both runs in the PR body, plus the gate's steps run individually — `typecheck`, `lint`,
      `check:demo`, `gate:spec` and `build` exit 0; `gate:design` exits 2 on a pre-existing `transition: height`
      finding in the bundled output of unchanged source, outside this task's scope. Go gates: `go test ./...` and
      the `embedboard` build are green.

## 5. Ship note

- [x] 5.1 The board is embedded in the Go binary (`internal/webui/dist`), so the web job's `npm run build` is not
      enough for the running service. Rebuild `internal/webui/dist` from `dist/`, then
      `CGO_ENABLED=0 go build -trimpath -tags embedboard ./cmd/fleet-console`; the operator restarts the unit. No
      config, schema or API change.
