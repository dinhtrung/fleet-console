# Design — the New project dialog fits the viewport

## 1. The primitive locks the page, so the dialog must own its scroll

`DialogContent` is fixed and centred with `-translate-y-1/2` (`src/components/ui/dialog.tsx:55-58`) and the Base UI
primitive owns the scroll lock (`src/components/ui/dialog.tsx:3-4`). An oversized popup therefore has no scroll
path at all: the document cannot move and the popup has no `overflow`. The fix belongs in the dialog's own layout,
not in a page-level workaround.

## 2. Bound the popup, pin the ends, scroll the middle

The create/edit dialog gets a three-row grid: header, body, footer. The popup is capped at
`max-h-[calc(100dvh-2rem)]` and the body row is `minmax(0,1fr)` with `min-h-0 overflow-y-auto`; the header and
footer are `auto` rows and never scroll. The popup already uses `grid` and `gap-3`, so this is a className change
on the existing `DialogContent` plus one wrapper element around the form/created panel.

`100dvh` rather than `100vh`: the mobile browser chrome must not put the footer under the address bar. At
390×844 the same rule applies; a full-screen modal is not needed to make the form usable and would lose the
"this is a dialog over the board" context the design system keeps.

## 3. Long guidance is disclosed

The live preflight renders collapsed by default as one line: its existing state badge (`would be ready` /
`would not be ready`), the failing check ids and the count, and a `Show details` control. Expanding renders
exactly today's rows — badge, label, verbatim detail, fix sentence, command, runtime row. Collapsing changes no
data and no request: the query already runs on the settled draft
(`src/components/ProjectPreflight.tsx:265-277`), and only the rendering is gated.

The summary must name the failing ids rather than only a count: a collapsed panel that says "2 problems" hides
which two, and the operator's next click would be a guess. When every check is ok the line says so and offers the
same expansion, because the setup commands are still useful.

## 4. The footer does not gate and does not move

`project-preflight` already decided that the live panel informs and never blocks creation
(`src/screens/ProjectsScreen.tsx:871-874`). The disclosure preserves that: `Create project` is enabled in both
states. Expanding the panel must not move the footer out of the viewport — the body absorbs the extra height and
scrolls, which is the point of decision 2.

## 5. Evidence is a screenshot at two sizes, because there is no browser harness

The repo has no Playwright or other browser-test dependency (`package.json` scripts) and this change does not add
one. The acceptance is therefore visual and reproducible by hand: open the dialog at 1280×720 and at 390×844,
capture closed and expanded, and read the dialog's own rectangle and the footer's position from the page. The
language gates (`typecheck`, `lint`, `build`) prove nothing about layout, so the screenshots are the evidence, not
a supplement to it.

## Risks

- **A collapsed panel hides a failing check.** Mitigated by the summary naming the failing ids and the detail
  staying one click away; creation was already allowed while checks fail, so no gate is lost.
- **Bounding the shared primitive changes every dialog.** Mitigated by applying the height/scroll through this
  dialog's `className` (and a body wrapper), leaving `DialogContent`'s defaults untouched; the delete confirmation
  keeps its current size.
- **A nested scroll area traps the wheel on the form.** The body is the only scroll container; the dialog's own
  `overflow` stays visible, so there is exactly one scrollable region.

## Decisions ledger

| Decision | Requirement that carries it |
|---|---|
| D1 — the dialog, not the page, owns the scroll | The New project dialog fits the viewport |
| D2 — header/footer pinned, body scrolls, `100dvh` bound | The New project dialog fits the viewport |
| D3 — preflight collapsed to a summary naming failing ids | Long preflight guidance is disclosed, not dumped |
| D4 — creation stays enabled in both states | Long preflight guidance is disclosed, not dumped |
| D5 — screenshots at 1280×720 and 390×844 are the evidence | The New project dialog fits the viewport |
