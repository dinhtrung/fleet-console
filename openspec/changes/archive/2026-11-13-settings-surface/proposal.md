# Proposal — settings the operator can actually change

## Why

`/settings` is the imported admin-panel mock (`src/screens/SettingsScreen.tsx:12` imports `../mock/api`): it edits
workspace settings that exist only in browser storage. The real configuration is
`~/.config/fleet-console/config.yaml`, and the only write route in the app is `POST /api/projects` (a project's
entry). Every other key — `tick_interval`, the new `rig_lifecycle` block, `notify_interval`, `act` — needs ssh, an
editor, and a restart.

That gap just became expensive: #157 adds the lifecycle block, and the operator's only way to turn it on is a shell.
The screen that looks like it does this is the one screen that cannot.

## What changes

- `GET /api/config` returns the effective settings, redacted, with each value's source — the `--print-config` view
  as JSON.
- `PUT /api/config` writes an allowlisted subset back to the file with the same guards `UpsertProject` already
  enforces: sha256 expected-hash for a stale read, whole-file validation before the write, atomic replace,
  comments preserved.
- `/settings` renders those fields, marks a change as **needs restart**, and shows which keys are read-only here
  (tokens, binaries, listen address, database path, projects).

## Losses, named

- **No hot reload.** A saved value takes effect when the service restarts; the response says so and the screen
  shows the banner. Hot reloading `act` or `listen` mid-tick is a second lifecycle, and the operator already has a
  restart in the unit.
- **No arbitrary YAML editing.** The textarea-in-a-browser idea is tempting and wrong: the file holds comments the
  operator wrote, and a full re-encode loses them. Only allowlisted scalars are spliced.

## Capabilities

### New Capabilities

- `settings`: the read view of the effective configuration, the allowlisted write path, its guards, and the
  restart contract.

## Impact

- `internal/configfile/configfile.go` — `UpdateSettings` with the same hash-guard and validation as `UpsertProject`.
- `internal/web/settings.go` (new) — the two handlers and the allowlist.
- `internal/config/config.go` — the allowlist as a function of the file shape, so a new key is a compile-time
  omission rather than a forgotten one.
- `src/lib/fleet.ts`, `src/screens/SettingsScreen.tsx` — real fields; the mock settings functions go.
