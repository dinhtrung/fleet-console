# Tasks — the settings surface

## 0. Ground rules

- Gate: `go build ./... && go vet ./... && go test ./...`, `npm run build`, `npm run typecheck`,
  `openspec validate --all --strict`.
- Fixed vocabulary: route `/api/config`, response keys `settings`, `source`, `saved`, `changed`, `restart_required`;
  refusal 409 for a stale read, 400 for a key.
- `internal/config/config.go` is shared with #157: this task starts after that PR merges and rebases on it. Never
  re-declare the lifecycle fields.
- The write path is `configfile`; a handler that writes the file itself is a review reject.

## 1. Config write path (builder 1)

- [x] 1.1 `internal/config/config.go`: `EditableKey()` (a classification table over `File`) and a test that every
      YAML key of `File` is classified editable or read-only, so a new key cannot slip in unclassified.
- [x] 1.2 `internal/configfile`: `UpdateSettings(path string, values map[string]string, expectedHash *[32]byte)` —
      textual splice for top-level scalars and the `rig_lifecycle` block, `ErrStaleRead` on a hash mismatch,
      `config.ValidateContent` on the merged bytes, atomic replace, comments preserved.
      Evidence: tests for the splice keeping a header comment and an untouched project block, the stale-read
      refusal, the validation refusal, and the unchanged no-write.
- [x] 1.3 `internal/web/settings.go`: `GET /api/config` from the effective settings (reuse the `--print-config`
      view; redact), `PUT /api/config` with the allowlist, 400 naming the key, 409 on `ErrStaleRead`.
      Evidence: handler tests, including "no credential value appears in the JSON".

## 2. Screen (builder 2, blocked by 1)

- [x] 2.1 `src/lib/fleet.ts`: `getSettings`, `putSettings` (sends the hash the read returned).
- [x] 2.2 `SettingsScreen.tsx`: real fields with source badges, read-only rows visibly disabled with the reason, the
      restart banner, the conflict state (409 ⇒ reload), and no `../mock` import.
- [x] 2.3 `src/mock/api.ts`: delete `getSettings`, `updateSettings`, `resetDatabase` and the workspace-settings type
      when nothing else reads them. (`getSettings` and `updateSettings` are deleted. `resetDatabase` stays:
      `BoardControls`, the shell's dev seam, still reads it. The workspace-settings type stays: the mock's own
      seed and its user-creation default still read it.)
- [x] 2.4 Evidence: build output plus a screenshot of the saved/restart banner against a throwaway config file and
      `act: false` on a spare port.
