# Design — superseded verdicts

## D1. "Superseded" is decided by the repository, not by string comparison

A note names `written`; the change request's head is `head`. The note is *superseded* when `written` resolves to a commit and `git merge-base --is-ancestor <written> <head>` succeeds, through the existing git runner (`act.ExecGit`) on the project's repository. A short sha that resolves counts. Comparing strings would treat a typo, or a commit from another branch, as superseded and hide a real problem.

Rejected: "any sha that isn't the head is superseded". It would silence a REJECT that names a commit outside the branch, and that's exactly the note the operator should see.

## D2. On doubt, ask

If the git check errors (no such object, repository unavailable, timeout), the note is **not** superseded and the operator question is raised as today. The change removes a false alarm. It never removes a question whose answer we don't know.

## D3. The merge decision is unchanged

A superseded note stores no verdict for the head. The merge for that head stays refused until a verdict names the head, so the existing guarantee holds: a REJECT that names another commit never falls through to an older ACCEPT. Only the operator *question* is suppressed. The skip line is still written once per (note, head) through the existing ledger key, so the journal still shows why the merge waits.

Standing questions (#414): `reconcileVerdictQuestions` already reads the unanswered question rows. When the tick captures a verdict for the current head, it closes each standing question for the same item whose head differs, with closure `superseded` and a note naming the captured verdict. Questions about the current head stay open. Running it again closes nothing more.
