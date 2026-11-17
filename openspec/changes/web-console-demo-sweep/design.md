# Design — the web console carries no demo scaffolding

## D1. Delete the mock layer; do not keep it behind a flag

The mock layer is 1,197 lines and eight images that no shipped screen reads. A `DEV`-only import still ships in
the bundle unless the bundler is trusted to drop it, still appears in review as available data, and still invites
the next screen to import it.

Rejected: keep `src/mock` behind `import.meta.env.DEV` — dead weight in the tree, a second data shape to keep in
sync, and a guard that has to allow it.

## D2. Remove the identity and permission fiction rather than invent a new one

`src/auth/session.tsx` resolves `usr_owner` / `operator@fleet-console.local` from the seed, and `useAccess` filters
the rail by a role template. fleet-console serves one operator behind a token; the client knows no user, and a
static "local operator" block would be the same fiction with fewer pixels.

The shell renders navigation for every shipped surface; the server's token remains the only authorization.

Rejected: keep a static identity block — it re-introduces a name and email that are not real. Rejected: keep the
permission filter with a hard-coded all-permissions role — a client-side gate that can only agree with itself.

## D3. Remove the failure, latency and reset controls with no replacement

`BoardControls` writes `localStorage["fleet-console.db"]`, which the real app never reads. The switch that made a
request fail on purpose was a demo affordance; a real 500 comes from stopping the service or breaking the token,
and the board already has error states.

Rejected: keep a "clear local state" button — the console stores only display preferences, and clearing them is a
browser setting, not a board control.

## D4. Remove the scope stack and its assets, including the motion it triggered

`ScopeSwitcher` selected a mock organization; no route read it, so the control and its provider go. With the
provider go `sweepKey`, the `.sweep` class and the `handover-sweep` keyframes whose only trigger was a scope
change, plus `OrgMark` and `public/mock/**` — referenced only by that component.

Rejected: keep the org marks for future use — an unused asset is demo information that still ships.

## D5. Rewrite the document head and the visible copy

`index.html` describes an admin panel on synthetic data; the favicon's `aria-label` says `admin-panel`; comments in
`router.tsx` and `index.css` still describe permission gates and re-scoping. All of it is rewritten or removed.
The two `admin-panel.*` localStorage cleanups go with the retired appearance build they served; the
`fleet-console.density` preference stays, because density is a real setting.

Rejected: leave the meta description — it is the first thing a link preview and a screen reader see, and it
describes a different product.

## D6. Widen the existing guard instead of adding a second one

`scripts/check-no-mock-screens.mjs` already runs in CI and already walks TypeScript sources. It becomes
`scripts/check-no-demo.mjs`: the walk covers all of `src/`, and a copy scan covers `src/`, `index.html` and
`public/` for the demo token list. The exception that blessed `src/auth/session.tsx` dies with the file.

Token list (case-insensitive, word-bounded where the word is common): `synthetic`, `demo`, `admin-panel`,
`not a real service`, `authored`, and any import path reaching `/mock`.

Rejected: leave the guard screen-only — it is the exact shape that let the shell keep its mock for two changes.

## D7. Keep what is real

The toast provider, the density preference, the real screens and the API client are untouched. This change removes
scaffolding; it does not redesign a screen.
