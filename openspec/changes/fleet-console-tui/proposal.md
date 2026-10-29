# Proposal — a terminal surface for the board

## Why

The board today is a web app on `127.0.0.1:8787`. That is the right home for the drawer, the diff of a decision and
anything you want to look at for more than a minute. It is the wrong home for the two things an operator does most:
glancing at whether the fleet is moving, and answering a decision that is blocking a seat — both of which happen while
you are already in ssh and tmux, and neither of which justifies opening a browser tunnel.

Bubble Tea (`github.com/charmbracelet/bubbletea` v1.3.10, MIT) gives a keyboard-driven TUI in the same Go module with no
runtime, so it can ride in the binary that is already installed and pinned by sha in `stack.lock`.

## What changes

A new capability `tui`, one subcommand `fleet-console tui`, and no change to the daemon's behaviour. The TUI reads the
same HTTP API the browser reads, writes only where a human is the missing actor (answering a decision), and never opens
the database or runs a tick.

## Non-goals

- Not a second dispatcher: no act loop, no merge, no queue creation.
- Not a replacement for the web board: the drawer, the audit trail and anything with a diff stays on the web.
- No mouse, no colour themes beyond sage, no plugin surface.
- Bubble Tea v2 is not used; it is not stable yet.

## Impact

- New dependencies (`bubbletea`, `bubbles`, `lipgloss`) in the module; the binary grows and stays CGO-free and static.
- `install.sh` is untouched: the same tarball carries the subcommand.
- A user who never runs `tui` sees no change.
