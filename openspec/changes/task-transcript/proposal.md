# Proposal — read the seat before believing it

## Why

The app already has the read: `GET /api/seats/:session/tail?lines=N` shells out to `rig capture` and returns the
pane's last lines (`internal/web/fleet.go:660-679`, capped at 200). The task drawer already knows the seat: it
renders `dispatch.seat` beside the branch and worktree (`src/screens/TaskDrawerScreen.tsx:135-149`).

Nothing joins the two. So the moment a reviewer needs evidence — is this seat actually working, or is it parked at
`needs-input` while claiming to be busy? — the operator leaves the board for ssh and `tmux capture-pane`. The
board's own seats screen shows lifecycle columns, which the skill's own note warns are not evidence for omp seats
(`rig ps` says `needs-input x12` while the transcript is moving).

A transcript panel in the drawer is the cheapest possible proof: one request, one `<pre>`, on demand.

## What changes

- The drawer's dispatch panel gains a **Transcript** section: the last 40 lines of the dispatch seat's pane, loaded
  when the operator opens it (not with the item, not on a timer), with a 200-line "more" that reuses the existing
  cap.
- No new server route: the existing route is used as it is. The only server change is none — if a permission is
  needed for `/api/seats/:session/tail`, it already has one.

## Losses, named

- **The pane is not the session.** `rig capture` shows the terminal, so a tool result that scrolled away is gone;
  the session `.jsonl` would be richer and is a file read the app has no business doing on another process's box
  path. Named here so nobody expects a full transcript.
- **No polling.** A live-tailing panel would burn a `tmux capture` per second per open drawer; the operator's
  refresh is the trigger.
- **The text is untrusted.** It is rendered as preformatted text, never markdown: a seat that prints `# heading` or
  an HTML snippet is showing what it printed, not formatting the board.

## Capabilities

### New Capabilities

- `seat-transcript`: the drawer's on-demand read of the dispatch seat's pane, its rendering rule and its refusal
  when there is no seat.

## Impact

- `src/lib/fleet.ts` — `seatTail(session, lines)`.
- `src/screens/TaskDrawerScreen.tsx` — the transcript panel and its states.
- No Go change, no route change, no schema change.
