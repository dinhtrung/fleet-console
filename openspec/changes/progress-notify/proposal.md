# Proposal — progress on the phone: one Telegram message when code lands

## Why

The board answers "why is it not moving" only for someone looking at the board. The operator is not at the
machine when a builder finishes: a merge lands, the seat frees up, and the next decision waits until somebody
opens a browser. Telegram is already the owner's channel (`internal/alert` sends one message per decision), but
nothing tells it that **work finished**, and nothing carries the state of the fleet: which issues are still
pending, what is being built right now, what is on hold, which epic is in flight, on which rig and — in full —
which project.

## What changes

- A **completion notice**: the applied tick sends one Telegram message at the moment a merged change closes its
  issue — what landed, the whole project record, and the progress block.
- A **progress report** on demand: `fleet-console --notify-report` sends the same message with no completion
  event, for the "where do we stand" question without opening the board.
- The message carries **the project in full, not a name**: key, name, forge, repo slug, repo URL, default
  branch, concurrency cap, on-hold, act on/off, `paths.main`, `paths.worktrees` — every field of the project row
  as the service reads it, so the message is enough to reconstruct what is running where.
- Then the **rig**, by name, with its live roster (which seats exist, how many are busy) and — when nothing is
  in flight — **why it is idle**, one sentence naming the cause, plus the same for each idle seat, and then
  the work:
  **in progress** (issue, seat, branch, age), **pending**, **on hold**, **epic in progress**.
- Delivery reuses `internal/alert`'s Telegram client. Token and chat id come from the environment only.

## Capabilities

### New Capabilities

- `progress-notify` — the message contract (project record, rig with every seat and the idle reason, the four
  work buckets; order and caps), the
  triggers (merge, on-demand, optional digest), the dedupe rule, the disabled-without-credentials path, and the
  never-fail-a-tick rule.

### Modified Capabilities

- `audit-and-alerts` — a completion notice is a second, non-escalating kind of alert; it never edits the
  decision messages and never creates a decision.

## Impact

- `internal/progress`: builds the snapshot from the store, the mirror read and the project row, and renders the
  text. Pure apart from the store read, so the render is a golden file.
- `internal/alert`: a `Notifier` that sends the rendered block once per state (hash in the event log).
- `cmd/fleet-console`: after-merge trigger, `--notify-report`, and the boot line when credentials are absent.
- `internal/config`: `repo_url` per project (a self-hosted GitLab cannot be derived from the slug) and
  `notify_interval` (0 = off) for a periodic digest.
- `docs/cutover.md`: the Telegram section stops being about decisions only.

## Non-goals

- No new transport (no webhook, no mail), no interactive bot, no per-seat messages.
- No replacement for the board: the message is a pointer to it, and carries the project record rather than a
  second copy of the fleet's history.
- The message is not a log. It carries state, not history: a later tick edits nothing, it reports the new state.
