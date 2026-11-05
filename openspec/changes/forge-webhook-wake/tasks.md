# Tasks — waking the loop

## 0. Ground rules

- Gate: `go build ./... && go vet ./... && go test ./...` and `openspec validate --all --strict`. Real output in
  the PR body.
- Fixed vocabulary: route `/api/hooks/forge`, header `X-Hub-Signature-256`, config key `webhook_secret_file`,
  response `{"woke": bool}`.
- No new dependency: `crypto/hmac`, `crypto/sha256`, `encoding/hex` are stdlib.
- The handler never writes to the store and never dispatches.

## 1. Wake channel and cadence (builder 1)

- [ ] 1.1 `internal/mirror`: `Loop` takes `wake <-chan struct{}`; the select gains the wake arm; a wake resets the
      cadence to base exactly as a busy tick does. A nil channel means "no receiver", so every existing caller and
      test compiles unchanged.
      Evidence: a loop test with a fake tick source asserting the tick runs before the timer, and that the next wait
      is `tick_interval` after a wake from the ceiling.
- [ ] 1.2 Coalescing lives in the caller: a `Waker` with a capacity-1 channel and `func (w *Waker) Wake() bool`
      that never blocks and reports whether one was already pending.
      Evidence: unit test — two wakes, one pending, second returns false; after consumption the next returns true.

## 2. The route (builder 1, blocked by 1)

- [ ] 2.1 `internal/config`: `webhook_secret_file`, resolved like `token_file`, redacted in `--print-config`,
      absent by default.
- [ ] 2.2 `internal/web`: `POST /api/hooks/forge` registered **outside** the bearer group, with a 1 MB body limit,
      raw-body read, HMAC-SHA256 compare in constant time, 202/401/413, one log line per accepted delivery. Absent
      secret ⇒ 404 without reading the body.
      Evidence: handler tests for 202 (and that the waker was called once), 401 (missing and wrong), 413, 404 when
      unconfigured, and `--print-config` redaction.
- [ ] 2.3 `cmd/fleet-console/main.go`: construct the `Waker`, pass it to both the loop and `web.Config`.
- [ ] 2.4 `docs/`: one page — payload URL, content type `application/json`, secret, events `issues`,
      `pull_request`, `issue_comment`, and the tunnel requirement. No screenshot.
