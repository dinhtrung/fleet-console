# Tasks — the New project dialog fits the viewport

## 1. The dialog is bounded and scrolls

- [ ] 1.1 Give the create/edit dialog a viewport bound: three grid rows (header, body, footer), popup capped at
  `max-h-[calc(100dvh-2rem)]`, body row `minmax(0,1fr)` with `min-h-0 overflow-y-auto`, in
  `src/screens/ProjectsScreen.tsx`. Leave `src/components/ui/dialog.tsx` defaults alone. Evidence: at 1280×720
  and 390×844 the dialog's `getBoundingClientRect()` is inside the viewport and the footer's Create project button
  is visible without scrolling; screenshot both sizes.
- [ ] 1.2 Prove the body, not the page, scrolls: with the body scrolled to the last field (`paths.worktrees`),
  the header and footer are still visible and `document.scrollingElement.scrollTop` is unchanged. Evidence:
  screenshot at both sizes plus the two scrollTop values before and after.

## 2. Long guidance is disclosed

- [ ] 2.1 Render the live preflight in `src/components/ProjectPreflight.tsx` collapsed by default: the existing
  state badge, the failing check ids and count, and a `Show details` control. Expanding renders today's rows
  unchanged (badge, label, verbatim detail, fix sentence, command, runtime row). Evidence: screenshot of the
  collapsed line for a draft with two failing checks, and of the expanded panel, at both sizes.
- [ ] 2.2 Keep creation enabled in both states: `Create project` is not disabled or hidden by the disclosure, and
  the summary names the failing ids rather than only a count. Evidence: the collapsed screenshot shows the enabled
  footer button and the ids; the expanded screenshot shows the footer still visible.

## 3. Gates

- [ ] 3.1 Run `npm run typecheck`, `npm run lint`, `npm run build`, and `openspec validate new-project-dialog-fit
  --strict`; paste the real output in the PR body. Evidence: the four commands and their exit codes.

## 4. Docs copy

- [ ] 4.1 Shorten only the dialog description to one sentence naming what the service validates; keep every field
  hint, since each names a rule the form does not enforce locally. Evidence: the diff and the 1280×720 screenshot
  showing the description at one line.
