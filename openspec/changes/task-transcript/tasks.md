# Tasks — the transcript panel

## 0. Ground rules

- Gate: `npm run typecheck && npm run build`, plus `go build ./...` so the tree stays green. No Go file is expected
  to change; if one does, say why in the PR body.
- Reuse `GET /api/seats/:session/tail` exactly as it is. Do not add a route, do not add a `lines` clamp.
- The panel never polls and never renders markdown.

## 1. The panel

- [x] 1.1 `src/lib/fleet.ts`: `seatTail(session: string, lines: number)` returning `{session, lines, tail}`, with
      the session `encodeURIComponent`-escaped because it contains `@`.
- [x] 1.2 `TaskDrawerScreen.tsx`: the transcript section inside the dispatch panel, `enabled` only when expanded and
      a seat exists, `staleTime: Infinity`, 40 → 200 lines, `<pre className="whitespace-pre-wrap">`.
- [x] 1.3 The three states: empty pane, 502 with retry, and no panel without a dispatch.
- [x] 1.4 Evidence: `npm run build` output and a screenshot of the drawer with a real tail from the running rig on a
      disposable instance (`act: false`, spare port), plus one screenshot of the no-dispatch state.
