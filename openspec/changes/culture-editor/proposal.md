# Change: edit a project's rig culture from the board

## Why

`CULTURE.md` is the document every seat is handed at launch: who merges, which gates run, what a worktree is, which
mistakes have already cost production time. Today it can only be edited by ssh into the box, in a heredoc, with no
idea what the seated fleet currently believes — and twice this week a wrong rule (a stale copy from another fleet, a
cwd that is not a worktree) taught seats the wrong procedure for hours before a human noticed.

A rule that steers every seat should be readable and correctable from the one surface the owner already watches.

## What changes

- A **Culture** surface per project on the board: the file rendered, its path, its sha256 and mtime, and an edit mode
  with a plain monospace textarea.
- `GET /api/projects/{key}/culture` and `PUT /api/projects/{key}/culture`, both reachable through the loopback
  session like the rest of the board.
- The write is conditional on the sha the editor saw (`base_sha`), atomic, mode-preserving, and leaves a timestamped
  backup of the previous content. A save that would silently revert someone else's edit is a `409`, not a success.
- The project entry in `config.yaml` gains an optional `culture_file`; when it is absent the app derives
  `~/.openrig/specs/rigs/<rig>/CULTURE.md` from the project's first rig and says so on the screen.
- Every write records a `culture.edited` event carrying both shas, so the board's history explains a fleet that
  suddenly behaves differently.

## What this is not

- Not an editor for `guidance/*.md` (the per-seat startup files) — same shape, later.
- Not an editor for `config.yaml` or the board database; the app stays their only writer and they stay out of a
  browser form.
- Not a rig restart. A running seat keeps the copy it was launched with; the screen says exactly that and the relaunch
  stays a CLI action.

## Impact

- Affected specs: `culture-editor` (new).
- Affected code: `internal/culture` (new: resolve, read, guard, write), `internal/web` (two handlers, route, event),
  `src/screens/CultureScreen.tsx` + a route and a nav entry, `src/lib/fleet.ts`.
- Affected config: one optional key per project.
- Risk: this is a write path into a file that steers agents. Small, deliberate, and reversible: the base sha, the
  backup, and the event row are the three things that keep it honest.
