# forge-trigger Specification

## Purpose
A verified forge delivery wakes the mirror's loop instead of waiting for the next poll, and an unverified one changes nothing.

## Requirements

### Requirement: A verified forge delivery wakes the loop

`POST /api/hooks/forge` SHALL verify `X-Hub-Signature-256` as HMAC-SHA256 over the raw request body, and on success
SHALL send one wake to the mirror loop and answer 202. It SHALL NOT read the store, dispatch, or change any row.

#### Scenario: A valid delivery

- **WHEN** a signed `issues` delivery arrives and no wake is pending
- **THEN** the response is 202 with `woke: true`, one log line names the event, and the loop runs a tick before its
  timer would have fired

#### Scenario: A delivery during a pending wake

- **WHEN** two valid deliveries arrive before the loop consumes the first wake
- **THEN** both answer 202 and exactly one extra tick runs

### Requirement: An unverified delivery does nothing

A request with a missing, malformed or wrong signature SHALL answer 401 before any body parsing beyond the HMAC,
SHALL NOT send a wake, and SHALL NOT log the body.

#### Scenario: No signature

- **WHEN** the request carries no `X-Hub-Signature-256`
- **THEN** the response is 401, the loop receives no wake, and no row is written

#### Scenario: The wrong secret

- **WHEN** the signature is well-formed but computed with a different secret
- **THEN** the response is 401 and the loop receives no wake

#### Scenario: An oversized body

- **WHEN** the body exceeds the configured limit
- **THEN** the response is 413 and no HMAC comparison is attempted

### Requirement: The receiver is off until it is configured

With no `webhook_secret_file` configured, the route SHALL answer 404 and SHALL NOT read the body. A configured
secret SHALL never appear in `--print-config` or in any response.

#### Scenario: An upgraded box

- **WHEN** the configuration has no `webhook_secret_file`
- **THEN** `POST /api/hooks/forge` answers 404 and the poll cadence is unchanged

#### Scenario: Reading the effective configuration

- **WHEN** `--print-config` runs with a secret configured
- **THEN** the key appears with its source and its value is redacted

### Requirement: A wake ends the idle cadence

A wake SHALL reset the poll cadence to `tick_interval`, and the tick it triggers SHALL be an ordinary tick: the
compat gate is checked first, the plan is recomputed, and no write happens that the same tick would not have made
on its own.

#### Scenario: Waking from a ten-minute wait

- **WHEN** the loop is waiting at `idle_poll_max` and a verified delivery wakes it
- **THEN** one tick runs immediately and the next wait is `tick_interval`

#### Scenario: A wake while the gate is closed

- **WHEN** the compat gate is refusing writes and a delivery arrives
- **THEN** the tick still runs, still refuses to write, and the log names the refusal as it always does
