# Design — one guarded write path

## D1. The write reuses the installer's rules, not a second implementation

`configfile.UpsertProject` already does the four things a config write needs: read the current bytes, refuse when
the caller's sha256 no longer matches, splice textually so the operator's comments survive, and validate the merged
document with `config.ValidateContent` before an atomic rename. `UpdateSettings` is the same function with a
different splice target (top-level scalars and the `rig_lifecycle` block) and the same refusal types
(`ErrStaleRead`, `*config.ValidationError`). One implementation of "a write that would leave the fleet unable to
start never reaches the disk" is the whole point.

## D2. The allowlist is derived from the file shape

Editable: `tick_interval`, `notify_interval`, `act`, `rig_lifecycle.{enabled,idle_after,idle_poll_max}`.
Read-only here: `listen`, `database`, `token`, `token_file`, `webhook_secret_file`, `rig_binary`, `gh_binary`,
`glab_binary`, `project_timeout.*`, `projects`.

The list lives beside the `File` struct and a table test asserts that every YAML key of `File` is classified as
editable or read-only — a new key with no classification fails the test instead of being silently writable.

Rejected: validating the JSON body against a hand-written schema in the handler. The file loader is the authority
on what is valid, and it already runs on the candidate bytes.

## D3. `projects` stays out

A project is not a setting: it has its own route (`POST /api/projects`) with its own guards and its own audit
event. Editing the registry through the settings surface would be a second writer for the same list.

## D4. Restart is stated, not faked

The process reads its configuration once at boot (`config.Load` in `main.go`). `PUT /api/config` answers
`{"saved": true, "restart_required": true}` whenever a value changed, and the screen shows a banner naming the unit
command. Values that did not change answer `saved: false, restart_required: false`, the same "unchanged" shape
`--apply-config` uses, so a double save is a no-op.

Rejected: `systemctl restart` from inside the process. The service would be killing the HTTP handler that is
answering the request; the operator's restart is one command and the response already names it.

## D5. Redaction is the default, not a filter

The response is built from the same struct `--print-config` renders, which already redacts credentials and never
prints a token. A field marked secret is omitted rather than masked, and a test asserts that no value of
`token`, `token_file` content or `webhook_secret_file` content appears in the JSON.
