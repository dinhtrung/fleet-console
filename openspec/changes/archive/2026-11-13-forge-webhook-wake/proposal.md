# Proposal — let the forge wake the loop

## Why

The idle cadence (`rig-lifecycle`, #158) buys quota by waiting longer: up to `idle_poll_max` (10m default) between
reads when nothing exists. The price is detection latency, and it is paid exactly when an issue is opened and
nobody is looking.

A forge already knows the moment an issue or a pull request changes. The app does not listen: `mirror.Loop` sleeps
on a `time.Timer` with no way in (`internal/mirror/mirror.go:478-491`), and the only routes that need no bearer are
`/api/health` and `/login` (`internal/web/app.go:65-70`).

Result: an operator opens an issue and waits, or the poll rate must stay high — one of the two is wrong every day.

## What changes

- `mirror.Loop` gains a wake channel: a signal short-circuits the timer, runs one tick, and resets the cadence.
- `POST /api/hooks/forge` accepts a GitHub (or GitLab) delivery, verifies an HMAC-SHA256 signature over the raw
  body, and sends one wake. Coalesced: three deliveries during one tick produce one extra tick.
- `webhook_secret_file` (0600) in the config; no secret configured means the route answers 404, so an existing box
  exposes nothing new.
- Every verified wake is one log line naming the forge and the delivery's event; an unverified one is a 401 with no
  log of the body.

## Losses, named

- **A webhook is a public write path.** It needs a real secret, an HMAC comparison in constant time, and a body
  limit; it does not authenticate the sender beyond that, and GitHub's IP ranges are not a substitute.
- **Wake is not dispatch.** It only makes the next tick happen now; every write still goes through the gate, the
  plan and the merge rules. A forged wake with a valid secret could spin the loop; the coalescing and the tick's
  own forge budget bound it.
- **The tunnel must expose the route.** On homebox that means the public port forwards to 8787; the operator opens
  it when configuring the hook. No tunnel, no webhook — the poll still works.

## Capabilities

### New Capabilities

- `forge-trigger`: the verified webhook, the wake channel, and the rule that a wake resets the cadence without
  bypassing the gate.

## Impact

- `internal/mirror/mirror.go` — `Wake <-chan struct{}` on the loop, and the cadence reset it implies.
- `internal/web/` — a new route file, registered outside the bearer group but with its own HMAC check.
- `internal/config/config.go` — `webhook_secret_file`, redacted like `token_file`.
- `cmd/fleet-console/main.go` — the channel and the route wiring.
- `docs/` — how to point a GitHub webhook at the box (payload URL, content type, secret, which events).
