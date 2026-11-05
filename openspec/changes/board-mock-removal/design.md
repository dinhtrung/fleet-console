# Design — deleting a scaffold, and keeping it deleted

## D1. Delete, do not hide

A route whose screen reads a mock is removed from `router.tsx` and the file is deleted. Hiding it behind a feature
flag would keep the mock module alive, keep its types in the tree, and leave the URL working for whoever flips the
flag. The release note names the nine dead URLs.

## D2. The mock module survives only for identity

`src/auth/session.tsx` imports `bindCurrentSession`, `listRoles`, `setActor` from `../mock/api`, and the shell's
`useAuth()` depends on it. That is the one remaining fiction and it is out of scope here: replacing it means
deciding what identity a loopback console has (the Go side has one token and no user row). The docs say so in one
sentence rather than leaving a reader to discover it.

Consequence: the guard in D4 targets **screens**, not the module. When identity is replaced, the module goes too,
and that is a separate change.

## D3. Audit and Settings enter the rail only when they are real

The rail gains `{ to: "/audit", label: "Audit", permission: "audit.read" }` and
`{ to: "/settings", label: "Settings", permission: "settings.write" }` in the same PR that removes the screens —
but only after `audit-surface` and `settings-surface` have merged, which is why this task is blocked by both. Adding
a link to a screen that still reads mocks is the exact failure this change removes.

Permissions are added where `src/access/access.tsx` declares them; a permission the module does not know fails
closed (the item is hidden), which is the safe direction.

## D4. The guard is a grep, and it runs in CI

`scripts/check-no-mock-screens.mjs` walks `src/screens/*.tsx`, fails on any `from "../mock` or `from "../mock/"`,
and prints the offending files. Wired as `npm run check:screens` and as a CI step beside `npm run lint`. A grep is
enough because the rule is syntactic; an ESLint rule would need the whole mock module to be typed as forbidden,
which is more machinery for the same answer.

Rejected: deleting `src/mock` wholesale. It would break the build through `auth/session.tsx` and turn a contained
change into an identity redesign.

## D5. No redirects, and no "coming soon"

A deleted URL 404s through the router's existing `notFoundComponent`, which offers "Back to the overview". That is
honest. A placeholder page saying "not available yet" is a screen that reads nothing and still costs a route.
