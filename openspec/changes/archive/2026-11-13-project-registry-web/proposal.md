# Proposal — project registry written from the board

## Why

The registry is a hand-written file. Adding a project today means ssh to the box, edit
`~/.config/fleet-console/config.yaml` by hand, remember that `--apply-config` exists, restart, and read the journal
to learn whether the file was accepted. The board already mirrors every project it knows about, but it cannot add
one, and the only record of a change is the operator's shell history.

## What changes

- Two routes: `GET /api/projects` (the registry as the running service sees it) and `POST /api/projects` (create).
- The write lands in `config.yaml`; the file stays the source of truth. The row is upserted into the DB in the same
  request, so the next tick mirrors the new project with no restart.
- The write preserves the operator's file: comments, key order and every other project survive, because the change
  is a targeted upsert of one entry rather than a re-render of the document from a struct.
- A Projects surface on the board: a table of projects and a New project dialog.

## Capabilities

### New Capabilities

- `project-registry` — create and list projects from the board, persisted to the config file, with a round-trip
  guarantee against `--print-config` and `--apply-config`.

## Impact

- `internal/config`: a comment-preserving writer for the `projects:` sequence, and one exported validation function
  shared by the loader and the handler.
- `internal/configfile`: the atomic, single-writer upsert.
- `internal/web/projects.go`: the two handlers, under the same auth group as the rest of `/api`.
- `src/screens/ProjectsScreen.tsx`, `src/lib/fleet.ts`, `internal/webui` (embedded board rebuild).
- Docs: `docs/quickstart.md` names the form; the README's configuration section says the board can add an entry.

## Out of scope

- Edit and delete. Delete is not a file edit and a delete: the act loop iterates the DB, so a project disappears only
  through `--prune`, which refuses while a live dispatch exists. That needs its own change.
- `onhold` from the board: it belongs to the DB, not the file, and a restart must never resume a paused project.
- Launching the new project's rig. The registry names a rig; starting it is operator work with `rig up`.
