# Tasks — a verdict on a superseded commit is the reviewer's question

## 1. Classify superseded verdicts (#413)

- [ ] 1.1 `internal/act/verdict.go`: a `supersededVerdict` check over the git runner: `written` resolves and is an ancestor of `head`; any error means not superseded.
- [ ] 1.2 `CaptureVerdict` / `unattributedDecision`: return a typed `SupersededVerdictError{NoteID, Written, Head, Decision}` for a superseded note, distinct from `UnattributedRejectError` / `UnattributedAcceptError`.
- [ ] 1.3 `internal/act/verdict_question.go` `questionFromError`: `SupersededVerdictError` asks nothing (`false`); the other types are unchanged.
- [ ] 1.4 `internal/act/tick.go`: the superseded case still writes its skip line once per (note, head) via the ledger, and still stores no verdict.
- [ ] 1.5 Tests: REJECT on an ancestor → no question, no merge; ACCEPT on an ancestor → no question, no merge; sha outside history → question; git error → question; later ACCEPT on the head → merge.

## 2. Close standing questions for a superseded head (#414)

- [ ] 2.1 `internal/act/verdict_question_reconcile.go`: after a verdict for the current head is captured, close the item's standing verdict-question rows whose head differs, with closure `superseded` and a note naming the verdict.
- [ ] 2.2 Leave a question for the current head open; a repeat run closes nothing more.
- [ ] 2.3 Tests for the three cases.

## 3. Docs and gate

- [ ] 3.1 `docs/fleet-console.md`: what a superseded verdict is and why it no longer asks the operator.
- [ ] 3.2 `go build ./... && go vet ./... && go test ./...` and `openspec validate --all --strict` pass; output pasted on each PR.
