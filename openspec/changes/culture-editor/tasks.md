## 1. Resolve and read (`internal/culture`)

- [x] 1.1 `Resolve(project, specsDir)` → `Target{Path, Source, Rig}`: `culture_file` if set (with `~` expanded), else
      `rigs[0]` → `$OPENRIG_SPECS_DIR/rigs/<rig>/CULTURE.md`; `OPENRIG_SPECS_DIR` defaults to `~/.openrig/specs`.
      Unit-tested for: config wins over derived, derived from the first rig, no rigs and no key → `not_configured`.
- [x] 1.2 `Read(target)` → `Snapshot{Exists, Writable, Bytes, SHA256, MTime, Content}`; `exists: false` is not an
      error; content over 256 KiB is `ErrTooLarge`.
- [x] 1.3 `internal/culture/culture_test.go` covers read of a real temp file (sha matches `sha256sum`), missing file,
      and the size refusal.

## 2. Guard and write (`internal/culture`)

- [x] 2.1 `Guard(target)` → `403` when the path is a symlink (`os.Lstat`) or when `filepath.EvalSymlinks(dir)+name`
      escapes the rig spec dir; unit test with a symlink to `/tmp`.
- [x] 2.2 `Write(target, content, baseSHA)` in the order design §3 lists: size → UTF-8/NUL → absence/base rules →
      stale → writability → guard; each failure returns a typed error the handler maps to a status.
- [x] 2.3 Backup `<name>.bak-<UTC yyyymmddThhmmssZ>` of the previous bytes, keep the five newest, remove older ones.
- [x] 2.4 Atomic write: temp file in the same directory, `fsync`, `rename`, restore the previous mode (default `0644`
      when creating). Test that an interrupted write never leaves a partial file (write to a temp dir, assert the
      target is either old or new).
- [x] 2.5 Tests for: stale sha → typed error carrying current sha/mtime; `base_sha: null` creates; `0444` file → not
      writable; five-backup rotation.

## 3. API (`internal/web`)

- [ ] 3.1 `GET /api/projects/{key}/culture` → the design §2 body; `413` above the limit; unknown project → `404`.
- [ ] 3.2 `PUT /api/projects/{key}/culture` → design §3 status table, success body `{sha256, bytes, mtime, backup}`.
- [ ] 3.3 Route registration beside the existing project routes, under the same loopback session middleware.
- [ ] 3.4 `culture.edited` event with `old_sha`, `new_sha`, `bytes`, `path` on every successful write.
- [ ] 3.5 Handler tests: read, stale `409`, `413`, `422` (NUL and invalid UTF-8), `403` (mode `0444`), symlink `403`,
      and one end-to-end save that leaves a `.bak` and an event row.
- [x] 3.6 Config: optional `culture_file` on a project, validated at load (absolute or `~`, `.md` suffix) with a unit
      test that a directory or a non-markdown path is refused.

## 4. Culture screen (`src/`)

- [ ] 4.1 `src/lib/fleet.ts`: `getCulture(key)` and `putCulture(key, content, baseSHA)`, with `409` surfaced as a
      typed conflict carrying the current sha and mtime (the same shape `createProject` uses for `422`).
- [ ] 4.2 `src/screens/CultureScreen.tsx`: header (`rig`, `derived|config`, path, short sha, size, mtime), rendered
      markdown, **Edit**.
- [ ] 4.3 Edit mode: monospace textarea, byte counter against 256 KiB, Save/Cancel, dirty indicator, confirm before
      leaving with unsaved changes.
- [ ] 4.4 `409` banner: current sha + mtime, `Load latest`, `Overwrite` (resend with the current sha).
- [ ] 4.5 Success: new sha, backup name, and the sentence about running seats + no restart button.
- [ ] 4.6 `not_configured`/`exists: false` state: expected path, the `culture_file` key, copy button, no editor.
- [ ] 4.7 Router and `AppShell` nav entry for `/culture`, beside Board / Decisions / Seats.

## 5. Gates

- [ ] 5.1 `make build`, `make test`, `npm run typecheck`, `npm run lint` green in the PR body with their real output.
- [ ] 5.2 `openspec validate culture-editor --strict` passes.
- [ ] 5.3 Manual pass recorded in the PR body: load → edit → save → sha changes on disk; a second tab provokes a `409`
      and both recovery actions work; a `0444` file is refused with `403`; the running fleet keeps the old text until
      a seat relaunches.
