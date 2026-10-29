# Tasks — terminal surface

Each task leaves `go build ./... && go test ./...` green. No task changes the daemon's behaviour.

## 1. Skeleton (pinned deps)

- [ ] 1.1 `go get github.com/charmbracelet/bubbletea@v1.3.10 github.com/charmbracelet/bubbles@v1.0.0 github.com/charmbracelet/lipgloss@v1.1.0`
- [ ] 1.2 `cmd/fleet-console tui` subcommand: resolves config + token, exits with a clear error when either is missing
- [ ] 1.3 `internal/tui/api.go`: typed client for `/api/board` and `POST /api/decisions/<id>` with a 5 s timeout
- [ ] 1.4 CI asserts the release build stays `CGO_ENABLED=0` and the binary is static

## 2. Screens

- [ ] 2.1 Projects screen: one line per project — key, counts by state, active tier and rig, last read time
- [ ] 2.2 Items screen: `bubbles/table` over the selected project, arrow keys, sort by number
- [ ] 2.3 Drawer: `bubbles/viewport` with body, live rows, verdict (with both shas when stale), decision buttons
- [ ] 2.4 Navigation: `enter`/`esc`/`?`/`q`, footer renders the keys of the current screen
- [ ] 2.5 Empty and error states: no projects, project paused, daemon down, token rejected

## 3. Style

- [ ] 3.1 `internal/tui/style.go` maps the six semantic states to lipgloss, one place only
- [ ] 3.2 256-colour fallback when `COLORTERM` is not truecolor; state also shown as a word, never colour alone
- [ ] 3.3 Width/height handling: layout survives 80x24 and 200x60 without wrapping artefacts

## 4. Writes

- [ ] 4.1 `a` / `r` + confirm on a pending decision; `POST /api/decisions/<id>` with the same body the web sends
- [ ] 4.2 409 and 5xx render as errors in the drawer, with a refresh, never a retry
- [ ] 4.3 No other write key exists in v1; the footer proves it

## 5. Freshness

- [ ] 5.1 2 s poll of `/api/board` via `tea.Tick`, cancelled on quit
- [ ] 5.2 Two consecutive failures mark the frame stale and disable write keys until a good read
- [ ] 5.3 Last successful read time in the header

## 6. Tests

- [ ] 6.1 Fake API serving two projects (one paused) and one stale-verdict item
- [ ] 6.2 Golden `View()` at 100x30 for projects, items and drawer
- [ ] 6.3 Update tests: navigation, confirm flow, 409 path, daemon-down path

## 7. Ship

- [ ] 7.1 README section: keys, what it does not do, the daemon requirement
- [ ] 7.2 Release notes mention the binary grew; `stack.lock` sha updated by the normal pin step
