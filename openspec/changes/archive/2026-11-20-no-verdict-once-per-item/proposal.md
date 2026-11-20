# Proposal: one unanswered no-verdict question per item, not per head

## Why

tsim #487 / !720, 2026-11-19: a builder iterating on a CI rule pushed 5 heads in 35 minutes. No reviewer row was
live for the item, so each head raised a new `decision.no_verdict` operator question and a notice: 5 pings for one
unanswered question. The owner stopped fleet-console to make it stop.

## What changes

- While a live no-verdict question for the item stands on a human seat (for any head), a moved head raises nothing.
- Once that question is answered or closed, the next head may ask again, as before.
- Stall, malformed-verdict and stale-verdict (reviewer) questions are unchanged.

## Impact

- `internal/decide/plan.go`: `noVerdictStanding` beside `questionStanding` in the no_verdict arm.
- Spec `review-verdict`: the "head moves" scenario.
