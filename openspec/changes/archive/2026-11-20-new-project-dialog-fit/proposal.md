# Proposal — the New project dialog fits the viewport

## Why

The operator cannot use the New project pop-up on a laptop: it is taller than the screen, its header is clipped at
the top and its Create/Cancel footer sits below the fold, and the page behind it is scroll-locked, so there is no
way to reach either end.

The popup has no height bound and no scroll container of its own:

- `DialogContent` is `position: fixed`, vertically centred with `-translate-y-1/2`, a `grid`, `w-full
  max-w-[calc(100%-2rem)]` and **no** `max-height` or `overflow` (`src/components/ui/dialog.tsx:55-58`). The
  primitive owns the modal scroll lock (`src/components/ui/dialog.tsx:3-4`), so the page cannot be scrolled to
  compensate.
- The create dialog puts the whole form — 14 controls, with a hint under most of them
  (`src/screens/ProjectsScreen.tsx:713-843`) — and then the live preflight panel inside that same popup
  (`src/screens/ProjectsScreen.tsx:874`).
- The panel is not a single line: it renders four check rows, each with a badge, a label, a verbatim detail, a
  fix sentence and a copyable command (`src/components/ProjectPreflight.tsx:89-115`), plus a runtime row with a
  login hint and its documentation line (`src/components/ProjectPreflight.tsx:144-150`). A failing `git ls-remote`
  detail alone wraps over several lines.

The sum is a dialog several hundred pixels taller than a 720px viewport before any check has answered, and the
guidance text — the description, the per-field hints and the preflight answers — is what pushes the controls out
of reach. This is the operator's report of 2026-11-16: "the guidelines on web is too long, making the pop-up
unable to use".

## What this changes

- **The popup is bounded to the viewport** and owns its scrolling: the title and the Create/Cancel footer stay
  visible, and the form body scrolls between them. The same bound applies at 1280×720 and 390×844.
- **Long preflight guidance is disclosed, not dumped**: the live panel renders as one summary line naming its
  state and the failing check ids, and expands to the four rows on demand. The form therefore fits above the fold
  without deleting the diagnostic text.
- **Creation stays enabled whatever the preflight says.** The `project-preflight` rule that the panel informs and
  does not gate is unchanged.
- The edit dialog uses the same form component and gets the same bound; it never renders the live panel.

## Scope

`src/screens/ProjectsScreen.tsx` (dialog layout and the description copy), `src/components/ProjectPreflight.tsx`
(the disclosure), and `src/components/ui/dialog.tsx` only where the bound belongs to the primitive rather than to
this dialog. No API change, no config key, no schema change, no preflight semantics.

## Non-goals

- Not redesigning the form, reordering fields, or removing any.
- Not hiding a hint behind hover only, and not truncating diagnostics: every sentence the panel renders today must
  still be readable after one expansion.
- Not changing what the preflight checks, when it runs, or whether it gates creation.
- Not adding a browser-test dependency to the repo; evidence is screenshots plus the existing gates.

## Impact

Web only. The shared dialog primitive's defaults stay as they are, so `ConfirmDialog` and the delete confirmation
keep their current size.
