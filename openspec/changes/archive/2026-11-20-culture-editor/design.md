# Design — culture editor

## 1. The file, and who may say where it is

A rig's culture is `culture_file: CULTURE.md` inside the rig's spec directory, i.e.
`~/.openrig/specs/rigs/<rig>/CULTURE.md` on this box. The board must never accept a path from a request: a write path
steered by a query string is a remote file write with extra steps.

Resolution order, once per project, in `internal/culture.Resolve`:

1. `projects[i].culture_file` from `config.yaml`, with `~` expanded.
2. Otherwise the first entry of `projects[i].rigs` → `~/.openrig/specs/rigs/<rig>/CULTURE.md`.
3. `OPENRIG_SPECS_DIR` (default `~/.openrig/specs`) overrides the root in both cases, because the app must work when
   the specs live elsewhere.

The resolved path carries its provenance (`source: config | derived`, `rig: <name>`) so the screen can say why it is
looking where it looks. A **derived** path whose rig directory does not contain `rig.yaml` is reported as
`not_configured` with the expected path — never created silently. This applies to derived paths only: an explicit
`culture_file` is the owner naming a location, and it need not sit under a rig directory at all, so it is never
second-guessed this way.

## 2. Reading

`GET /api/projects/{key}/culture` answers:

```json
{ "path": "/home/dinhtrung/.openrig/specs/rigs/fleet-console/CULTURE.md",
  "source": "derived", "rig": "fleet-console",
  "exists": true, "writable": true, "bytes": 5445,
  "sha256": "…", "mtime": "2026-10-30T13:20:11Z", "content": "# fleet-console issue fleet\n…" }
```

`exists: false` is a normal answer (200), not a 404: the screen needs the path to explain itself. A file over 256 KiB
is `413` on read as well as on write, so a hand-mangled file cannot be streamed into a textarea.

`sha256` is over the bytes on disk, exactly as read. It is the concurrency token and the thing a verdict-less audit
row points at.

## 3. Writing

`PUT /api/projects/{key}/culture` with `{ "content": "...", "base_sha": "<sha or null>" }`.

Order of checks, first failure wins, each with its own status:

| check | status | body |
|---|---|---|
| content is not a string, or `len > 256 KiB` | `413` | `{"error":"content too large","limit":262144}` |
| content contains NUL or is not valid UTF-8 | `422` | `{"error":"content must be UTF-8 text"}` |
| the path is a symlink, is not a regular file, or resolves outside the rig spec dir | `403` | `{"error":"refused","path":"…","resolved":"…"}` |
| file absent and `base_sha` is not `null` | `409` | `{"error":"no culture file","sha":null}` |
| file present and `base_sha` != current sha | `409` | `{"error":"stale","sha":"<current>","mtime":"…"}` |
| file present but not writable, or its directory is not writable | `403` | `{"error":"not writable","path":"…","mode":"0444"}` |

The guard is third, not last. Ordering it after the base rules was wrong in two ways: a symlink with
`base_sha: null` answered `409 stale` carrying the sha and mtime of a file outside the rig, and a FIFO in the
target's place made the write block forever — opening a FIFO for reading waits for a writer. Both are
preconditions about *which file this is*, not judgements about its content, so they are settled before anything
opens the path. The absence and staleness rules keep their relative order, so `base_sha: null` still means
"create".

On success:

1. copy the current bytes (if any) to `<name>.bak-<UTC yyyymmddThhmmssZ>`, keeping the five newest backups;
2. write the new content to `.culture.tmp` in the same directory, `fsync` the file, `rename` over the target;
3. restore the previous mode (a `0644` file stays `0644`);
4. append a `culture.edited` event with `old_sha`, `new_sha`, `bytes`, `path`;
5. answer `200 {"sha256","bytes","mtime","backup"}`.

Atomic rename in the same directory is the whole trick: a seat launching mid-save sees either the old file or the new
one, never a half-written rule.

Concurrency is deliberate and cheap. The owner is the only writer in practice, but the failure we are preventing is
not two people — it is one person with two tabs, or a page left open for an hour while the fleet is redrawn. A
`base_sha` mismatch returns the current sha and mtime; the screen then offers "load latest" (discard local) or
"overwrite" (re-send with the current sha as base). No silent last-write-wins.

## 4. The screen

`/culture`, a sibling of Board / Decisions / Seats, one project at a time (the board is single-project today; the
screen is written against a project key so it does not have to be).

- Read state: a header line `rig fleet-console · derived · /home/…/CULTURE.md · sha 3f2a91c4 · 5.4 KB · 2026-10-30
  13:20Z`, the markdown rendered with the board's existing typography, and an **Edit** button.
- Edit state: a monospace `<textarea>` prefilled with the raw content, the byte count against the limit, `Save` and
  `Cancel`, and a dirty indicator. Leaving the route or the mode with unsaved changes asks first.
- `409`: a banner naming the sha on disk and when it changed, with `Load latest` and `Overwrite`.
- Success: the new sha, the backup name, and one sentence that matters — *running seats keep the copy they were
  launched with; this applies the next time a seat starts.*
- `not_configured`: the expected path, the `culture_file` key that would fix it, and a copy button. No editor, no
  accidental creation.

The screen is a plain form over `src/lib/fleet.ts`, like every other screen; no screen calls `fetch` directly.

## 5. What we are not doing, and why

- **No rich text.** The file is read by models; markdown source is the artifact. A WYSIWYG layer would be a second
  representation to keep honest.
- **No diff view, no history UI.** Five rolling `.bak` files and the event row answer "what changed and when" without
  building a versioned store. If this file ever needs real history, it belongs in git, not in the board.
- **No restart button.** Restarting a rig kills seats mid-turn. That decision stays with the owner on the CLI, where
  the blast radius is visible.
- **No editing of `guidance/*.md`.** Same mechanics, but each seat's charter is currently stable; adding a second
  editor before the first is used would be building for symmetry.
