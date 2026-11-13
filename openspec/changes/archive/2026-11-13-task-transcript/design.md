# Design — the pane, on demand

## D1. Read on open, never on a timer

The drawer already fetches the item when it opens. The transcript is a second query, keyed by the dispatch seat,
enabled only when the operator expands the panel (`staleTime: Infinity`, `refetchOnWindowFocus: false`). A polling
tail would spawn `rig capture` every second for every open drawer, on a box where the process budget is already the
scarce resource — the same reason the tick's own forge reads are bounded.

## D2. The seat comes from the dispatch row, not from a guess

`TaskDrawerScreen` already receives `dispatch`; `dispatch.seat` is `<seat>@<rig>`, exactly what `rig capture` takes.
When there is no dispatch row the panel is absent with `No dispatch — this task was never handed to a seat`, which
is the same sentence the panel above it uses. Never derive a session from the branch name or from the project's
`rigs` list: both can name a seat that is not running this item.

## D3. `lines` is capped where it already is

The route clamps `lines` to 1..200 and defaults to 20. The screen asks 40 by default and 200 on "more", and does not
add a second clamp of its own — two clamps is how a route's contract drifts from the screen's expectation.

## D4. Preformatted, monospace, and nothing else

The pane is terminal output: agent chatter, colour codes, box drawing. It is rendered inside `<pre>` with
`whitespace-pre-wrap` and `overflow-auto`, no markdown, no link detection, no syntax colouring. Colour escape
sequences are stripped server-side? No — they are not, and stripping would be a new promise; the pane's own headers
are readable in plain text and the operator asked for the bytes.

## D5. Empty, refused and unavailable are three states

- `tail` empty → `the pane is empty` (a just-launched seat).
- a 502 from the route → `the seat could not be read: <err>` with a retry, because a dead capture is not an empty
  pane.
- no dispatch → no panel.

Those are the same three states the audit surface needs, and for the same reason: an empty result and a failed read
must never look alike.
