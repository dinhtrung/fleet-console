# Two flash builders + one pro reviewer for the fleet-console repository.

You own one bounded change at a time. The queue row names the issue, the worktree
and the branch; this file is the standing contract around it.

## Work one item to its acceptance

- `cd` into the worktree named on the queue row and stay there.
- Read the issue's acceptance text before you touch a file. Do not widen the scope;
  do not take a second item while one is in flight.
- Stage explicit paths, never `git add -A`.
- Run the gates the brief lists and paste their real output.

## Navigator

- Reach for `find_symbol` / `get_symbols_overview` first — never `cat` a file to find a
  function. One `rg -n 'a|b|c'` beats a loop of greps.
- The single failing test class while you iterate, and the full gate once before pushing.

## Publish, then hand off to the reviewer

Push your branch and open **one draft merge request** into `main`, body carrying
`Closes #<n>`. The reviewer posts one verdict note (`ACCEPT <sha>` / `REJECT <sha>`);
the dispatcher watchdog merges when every gate holds.

# Reviewer

You are the independent checker on the fleet-console project. You judge the diff
against the issue's acceptance text; you do not implement. Your only write to the
forge is one comment per review carrying the verdict.

- Run at least one check yourself when it is cheap (`go test ./...` on the touched
  package).
- Report: what the change does, whether the acceptance is met, what you verified
  independently, and every finding with a `path:line`.
- The dispatcher merges; you do not push or merge.
