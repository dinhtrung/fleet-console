# Design — the terminal surface

## Same binary, or a second one

A second binary (`fleet-tui`) was rejected: `stack.lock` pins one tarball sha, `install.sh` installs one artifact, and a
second binary would double the release surface for a surface that is worth ~1 500 lines. `fleet-console tui` is a
subcommand that reads the same config, resolves the same token, and opens the same API. Cost: the Bubble Tea stack adds
roughly 2 MB to a 15 MB binary, and `go build` with `CGO_ENABLED=0` still produces a static file.

## Where it reads from

The daemon's HTTP API (`http://127.0.0.1:8787`), with the token from the same file the daemon checks. Not the SQLite
file, for three reasons: the single-writer rule (D1) exists so nothing else mutates the database; the API returns the
board already shaped the way the web board draws it, so the TUI has no second interpretation of an item; and a TUI that
reads the file would keep showing a coherent picture while the daemon is dead, which is the one thing it must not do.

Consequence: the TUI needs the daemon. With the daemon down it starts, shows the last successful read, and marks the
frame `stale: <error>` instead of pretending.

## Polling, not streaming

The API has no event stream and one operator is looking at this screen. A 2 s poll of `/api/board` is one cheap GET; it
also gives freshness for free (the timestamp of the last good read). Server-sent events were rejected as a second
protocol to test for a payload that changes at most every 60 s.

## Reading and writing

Read-only everywhere except decisions. A decision is the one record whose next actor is human, so the TUI answers it:
`a` approve, `r` reject, then a confirm key. The write is the same `POST /api/decisions/<id>` the web board sends, so
the rig stays the record and the app keeps its one write path. Dispatching, pausing and answering a question with
free text stay on the web board — not because the TUI could not, but because they are rare and deserve a form.

## Layout

Three screens, no tabs: projects (one line per project with counts and the active tier) → items (a `bubbles/table` of
the selected project) → drawer (viewport with the item body, its rows, verdict and the decision buttons). `enter`
descends, `esc` ascends, `?` overlays the keys, `q` quits. The footer always shows the keys valid in that screen, because
a TUI whose keys are only in the docs is a TUI nobody uses twice.

## Sage in a terminal

The web board's semantic tokens map to lipgloss styles in one place (`internal/tui/style.go`): `building`, `review`,
`waiting`, `parked`, `blocked`, `stale` get the same hues as the board, with a 256-colour fallback when `COLORTERM` is
not truecolor. No second palette, no per-user theming — the same decision as the web (§15.9).

## Testable without a terminal

`tea.Model` is a pure function of (model, message). Tests drive `Update` with key messages and compare `View()` output
against golden strings with the width/height forced; a fake API serves fixtures. No PTY tests: they are flaky in CI and
they test the terminal, not the app.
