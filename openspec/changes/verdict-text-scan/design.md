# Design — read the verdict the reviewer actually wrote

## D1. A verdict opens a line; it is not a word somewhere in a paragraph

Two rules were on the table: search the whole note for `ACCEPT|REJECT`, or require the word to open a line after the
wrapping is stripped. The first is what #264 removed, and its test still holds: `Not ready to ACCEPT <sha> until CI
is green` contains the token and says the opposite. Merging that note merges a change its reviewer refused.

So the token must **open** the line. Stripping, in this order, is what turns a wrapped verdict into a line that
opens with it:

1. markdown fences and HTML comments blanked — examples are not verdicts;
2. `[ACCEPT](https://…)` rewritten to its text — otherwise the sha in the URL attributes the verdict;
3. emphasis (`**`, `__`, `~~`) removed;
4. leading non-letters removed (bullets, `#`, `>`, `|`, emoji, check marks);
5. one label from a closed list (`QA`, `Verdict`, `Review`, `Result`, `Decision`, `kết quả`, `đánh giá`) and its
   separator removed.

The list is closed on purpose. "Any short word" would read `Ready to accept` as a verdict, which is the same failure
with a longer prefix.

`Rejected:` a "contains the token" mode behind a config switch. A gate with two readers that disagree is a gate
whose merges depend on which one ran, and the switch would be flipped once, in the direction that merged a refusal.

## D2. A short sha names the head only as a prefix of it

`forge.SameSHA` needs seven characters on both sides, which is right for attribution and wrong for a reviewer who
types `ACCEPT 930297d`. So: seven or more characters keep today's rule; four to six are accepted **only when they
are a prefix of the head under review**, and then the row stores the head, not the abbreviation. Four to six that
are not a prefix name no commit the gate can act on, and the note is skipped rather than turned into a question —
a typo is not an ambiguity the operator should be paged for.

`Rejected:` treating a short sha as "the head" by default. `ffff` would then merge whatever is at the head.

## D3. What the scan cannot attribute becomes a question, once

The three ambiguous shapes — a verdict word with no sha, both polarities in one note, a line that negates the
verdict (`Verdict: not ACCEPT <sha>`) — raise one row whose destination is `operatorDestination(mode, seat)`:
the project's `verdict_relay.seat` when its mode is `operator`, `human@kernel` otherwise. The key is
`note=<id>; head=<head>;`, carried in the event text as well as the dedupe check, so a tick repeating every minute
asks once while a new note or a moved head is a new question.

The brief names the note, the head, the line the scan read, and the three answers: post `ACCEPT <head>`, post
`REJECT <head>`, or send the request back for review. The app never writes the verdict and never merges on the
question. If the row cannot be created for the configured seat, it is retried once against `human@kernel`, so a
seat that was renamed cannot swallow the question.

`Rejected:` a second escalation timer for unanswered questions. The rig already raises undelivered work: on
2026-11-13 13:40Z it queued `Stuck sweep: undelivered-wake on qitem-… (4 min)` to `human@kernel` without any help
from this app. Two mechanisms sweeping the same row is one mechanism too many.

## D4. A verdict about another commit asks the same seat

Today an accept or reject whose sha is not the head ends the scan with a typed refusal and waits for the reviewer to
post again. When the reviewer has stopped posting, that wait is permanent. The tick now also asks the operator seat
to choose: `ACCEPT <head>`, `REJECT <head>`, or re-review. The typed errors stay — they carry the audit event and
the skip line, and `errors.As` is how the tick knows which question to ask.

`Rejected:` auto-forwarding the old verdict to the new head. A verdict is a statement about a commit; moving it is
a new statement, and it is the seat's to make.

## D5. Reading the note is bounded, and the bound is a rule

`verdictWindow` is 1024 characters, cut on a rune boundary. It is large enough for the nine wrapped shapes measured
in #263 plus a paragraph of context, and small enough that a page of prose cannot smuggle a verdict in from below the
fold. The cut is tested (`TestScanVerdictTextReadsOnlyTheWindow`), not incidental.

`Rejected:` reading the whole body. The cost is unbounded note sizes, and the benefit is verdicts that a reviewer
buried — which is exactly the note the gate should ask about rather than act on.
