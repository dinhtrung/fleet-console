# Design — waking a sleeping loop

## D1. One buffered channel, not a condition variable

`Loop` selects on `ctx.Done()`, the timer, and `wake`. The channel has capacity 1 and the sender uses a
non-blocking send: a delivery that arrives while another is pending is *coalesced*, because the tick it would
trigger is already about to run and reading the forge twice in one second is exactly the cost this route exists to
avoid. Capacity 0 would block the HTTP handler; an unbuffered wait with a mutex would be a second scheduler.

The wake resets the cadence to `tick_interval` (the idle streak ends), because something changed — that is the same
verdict a busy tick gives.

## D2. HMAC over the raw body, constant time, no alternatives

`X-Hub-Signature-256: sha256=<hex>` over the bytes as received. The handler must read the raw body **before** any
JSON decoding (a re-marshalled body hashes differently — the classic bug), then `hmac.Equal`. No
`X-Hub-Signature` (sha1) fallback, no token-in-query-string, no IP allowlist as an alternative: one scheme, and a
request without a valid signature is a 401 that never reaches the store or the loop.

## D3. No secret, no route

`webhook_secret_file` is read at boot and held in memory; when it is empty the route is registered as a 404 handler
that does not read the body. An upgraded box therefore gains no public surface until the operator configures it —
the same rule as the token: a missing credential disables the capability rather than half-enabling it.

The secret lives in a file next to the token (`~/.config/fleet-console/webhook_secret`, 0600), never in
`config.yaml`, for the same reason the token does not: config files get copied into tickets.

## D4. The wake is not an instruction

The handler does not parse the delivery's action beyond logging it, does not dispatch, and does not touch the store.
Its whole output is one `wake` on the channel. That keeps the one-writer rule intact: `act` is still the only thing
that dispatches, and the gate (`compat`) is still checked before any write. A webhook that could dispatch would be a
second writer with an unauthenticated-ish credential.

## D5. The response is fast and boring

`202 accepted` with `{"woke": true|false}` (`false` when a wake was already pending). GitHub retries a slow hook, and
a retry is another wake; answering in microseconds keeps that rare. A malformed signature is `401`; a body over 1 MB
is `413` before the HMAC is computed.

## D6. Only the events that can change the plan are documented as useful

`issues`, `pull_request`, `issue_comment` cover the three things the plan reads: an item opening or closing, a
change request moving, a verdict being posted. The route accepts any event with a valid signature — the filter is
the operator's hook configuration, not a second allowlist in code that would silently drop a new event type.
