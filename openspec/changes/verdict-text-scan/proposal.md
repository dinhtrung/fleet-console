# Proposal — read the verdict the reviewer actually wrote

## Why

Three measurements from this box on 2026-11-13, all on project `fleet-console`:

**1. The project has never captured a verdict.** `select project_id, count(*) from verdict` returns 93 rows for
`tsim` and **zero** for `fleet-console`. Every merge in that repo has been a hand merge; the gate has been reporting
"no ACCEPT verdict" for its whole life, and nobody knew because the line looks like a waiting state.

**2. The shape that broke it is one word of prefix.** QA posted, at 13:24:26Z on PR #369:

```
QA ACCEPT 930297d9ac33fd2d3f4ad47f8bc25cc62dbd6274
```

`verdictNote` anchors on the first line and `stripDecoration` only removes decoration and a `label:` prefix. `QA` is
neither, so the note was not verdict-shaped, `CaptureVerdict` returned nothing, and the tick printed
`#368 merge waiting: refusing merge: no ACCEPT verdict for dispatch 364` for forty minutes. A relay note
(`ACCEPT <sha>` + `via: …`) merged it at 13:46:08Z without any other change: the verdict was always there, the reader
was not.

**3. The reader was narrowed on purpose, and that reason still holds.** `internal/act/verdict.go:16-24` records that
an earlier pattern searched the whole body, so prose that mentioned a token became a verdict. `act_test.go:218` pins
it: `Not ready to ACCEPT abc1234 until CI is green.` must stay prose. Any widening has to keep that.

## What changes

`CaptureVerdict` keeps its anchored first-line grammar as pass one, and gains a **text scan** as pass two: the note's
first 1024 characters, line by line, looking for a verdict word that **opens** a line once decoration, a link, icons
and one short label (`QA`, `Verdict`, `Review`, `Result`, `Decision`) are stripped. A sha of 4–6 characters names the
head only when it is a prefix of it. What the scan still cannot attribute — no sha, both polarities, a negation —
becomes a **question to the operator seat** (`operator-agent@kernel` when the project opts in, `human@kernel`
otherwise) instead of silence. A verdict about another commit asks the same seat to accept at the head, reject, or
re-review, rather than waiting for a note that may never come.

## Impact

- `internal/act/verdict.go`, new `verdict_text.go` and `verdict_question.go`; no schema change, no new event kind
  beyond `decision.verdict_question`.
- Both forges: the scan reads note text, and GitHub and GitLab hand it the same `Comment` values.
- The merge gate's four facts are untouched: a captured verdict still has to name the head, checks still have to be
  green, the request still has to be mergeable and to close its item.
- Operator questions move from `human@kernel` to a seat only for projects that set `verdict_relay.mode: operator`;
  an absent block keeps today's behaviour.
