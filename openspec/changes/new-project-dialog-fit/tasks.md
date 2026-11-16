# Tasks — the New project dialog fits the viewport

## 1. The dialog is bounded and scrolls

- [x] 1.1 Give the create/edit dialog a viewport bound: three grid rows (header, body, footer), popup capped at
  `max-h-[calc(100dvh-2rem)]`, body row `minmax(0,1fr)` with `min-h-0 overflow-y-auto`, in
  `src/screens/ProjectsScreen.tsx`. Leave `src/components/ui/dialog.tsx` defaults alone. Evidence: at 1280×720
  and 390×844 the dialog's `getBoundingClientRect()` is inside the viewport and the footer's Create project button
  is visible without scrolling; screenshot both sizes.
  **Done (PR #483):** dialog rect `16,304,976,704` @1280×720 and `16,16,374,828` @390×844, both inside the
  viewport; `docs/screenshots/issue-477/new-project-dialog-{1280x720,390x844}-collapsed.png`.
- [x] 1.2 Prove the body, not the page, scrolls: with the body scrolled to the last field (`paths.worktrees`),
  the header and footer are still visible and `document.scrollingElement.scrollTop` is unchanged. Evidence:
  screenshot at both sizes plus the two scrollTop values before and after.
  **Done (PR #483):** body `scrollTop` 0→367 @1280×720 and 0→841 @390×844;
  `document.scrollingElement.scrollTop` stays 0 throughout; footer visible in both.

## 2. Long guidance is disclosed

- [x] 2.1 Render the live preflight in `src/components/ProjectPreflight.tsx` collapsed by default: the existing
  state badge, the failing check ids and count, and a `Show details` control. Expanding renders today's rows
  unchanged (badge, label, verbatim detail, fix sentence, command, runtime row). Evidence: screenshot of the
  collapsed line for a draft with two failing checks, and of the expanded panel, at both sizes.
  **Done (PR #483):** collapsed line and expanded panel at both sizes in
  `docs/screenshots/issue-477/*-{collapsed,expanded}.png`; expanding restores the four `CheckRow`s and the
  `RuntimeRow`; the query and `enabled` condition are untouched and toggling sends no request.
- [x] 2.2 Keep creation enabled in both states: `Create project` is not disabled or hidden by the disclosure, and
  the summary names the failing ids rather than only a count. Evidence: the collapsed screenshot shows the enabled
  footer button and the ids; the expanded screenshot shows the footer still visible.
  **Done (PR #483):** `Create project` is enabled in every screenshot; the summary reads
  "2 checks need attention: git_url, base_dir".

## 3. Gates

- [x] 3.1 Run `npm run typecheck`, `npm run lint`, `npm run build`, and `openspec validate new-project-dialog-fit
  --strict`; paste the real output in the PR body. Evidence: the four commands and their exit codes.
  **Done (PR #483):** typecheck, lint and build exit 0 (lint warnings pre-existing, none in the changed files);
  `openspec validate new-project-dialog-fit --strict` → "Change 'new-project-dialog-fit' is valid". Output pasted
  in the PR body.

## 4. Docs copy

- [x] 4.1 Shorten only the dialog description to one sentence naming what the service validates; keep every field
  hint, since each names a rule the form does not enforce locally. Evidence: the diff and the 1280×720 screenshot
  showing the description at one line.
  **Done (PR #483):** description is one sentence — "Every field maps to config.yaml, and the service validates the
  key, forge, repo, rigs and runtime — naming the field it refuses." Every field hint is unchanged; visible in the
  1280×720 screenshots.
