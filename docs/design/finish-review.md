# Finish review — the shipped build against the direction contract

**Reviewer:** a fresh-context agent with no history from the build, given the direction contract, the
design system, the palette record and the live deployment. Commissioned because the build's own
inspection happened in-thread, which the craft discipline treats as a substitution that must be
disclosed rather than passed off as the shipped reviewer.

**Surface reviewed:** 12 live surfaces (sign-in, Overview, Users, Roles, Organizations, org detail,
Sessions, audit record, audit detail, API keys, Feature flags, Settings) at 1280px and 390px, in all
three appearances.

## Verdict, quoted

> Craft-floor scan over 9 routes found ZERO violations: no emoji, no unicode glyph used as an icon
> (every sort caret, delta caret, row arrow and the field-change `→` is a real lucide/tabler SVG), no
> gradient or gradient text, no backdrop-filter/blur, no glow or zero-offset shadow, no nested panels,
> and no same-size card grid used as page structure. The rail is the ONLY saturated ground in all three
> appearances. Numbers are tabular and mono holds only identifiers — a dump of every Azeroth-mono
> element on `/audit` returned only ids, action codes, IPs and one monogram, no prose.
> `aria-sort` toggles correctly on double-click; there is NO page-level horizontal overflow at 390px on
> any route; contrast is clean in all three appearances. However the build is NOT ready to ship: the
> Dusk appearance paints a completely invisible keyboard focus ring on the entire rail.

The craft floor passing while a functional release criterion failed is the useful part of this
review — the detector cannot see a focus ring that is the colour of its own background.

## Findings and what changed

| # | Severity | Finding | Fix | Measured after |
|---|---|---|---|---|
| 1 | **blocker** | Dusk: the focus ring is invisible on the whole rail — `--focus` and `--board-rail` were both `#083A52`, so the ring painted at **1.00:1** | Split the ring token: `--focus` stays the panel ring, `--focus-rail` carries the rail's own ring, and rail-surfaced regions re-point `--focus` in one base-layer declaration (`aside, [data-rail-surface]`) so a new rail control cannot get it wrong | **6.65:1** (`#ABC5C9` on `#083A52`) |
| 2 | should-fix | Light: the rail ring measured **2.44:1** (`#903078` on the near-black rail) — perceptible but under the 3:1 floor the system commits to | Same split; light's rail ring is now the tint | **13.14:1** (`#D5DEF0` on `#181818`) |
| 3 | should-fix | Overview "Recent activity": the actor column was content-sized, so the action and target columns drifted row to row (27px and 87px of drift) — the contract's FIRST VIEWPORT sentence promises fixed columns | Fixed column slots from the `sm` breakpoint up, wrapping preserved below it | drift **0 / 0 / 0** across six rows (actor x=273, action x=433, target x=609, constant) |
| 4 | minor | Feature flags: row height varied **52–93px** on one page because the mono key soft-wrapped and the environment badges stacked | Key truncates inside a capped slot; environments render on one line, first two in short form with the count for the rest and every full name in the tooltip | heights **51–52px** (uniform) |
| 5 | minor | Feature flags: the table overflowed its container at a 1280px viewport (**1200 vs 988**), pushing the actions column off-screen while every other grid fit | Column widths tightened. The root cause was not the declared widths but `truncate`: `white-space: nowrap` makes a cell's min-content the whole string, so the caps had to go on the cells | table **998 = 998** at 1280 and **1158 = 1158** at 1440 — `needsScroll: false`, actions column fully visible, page overflow 0 |
| 6 | minor | Sessions: the "This session" chip wrapped onto two lines (80×37px), reading as broken | `whitespace-nowrap` on the `Badge` primitive — a chip is one line by definition, so every badge benefits | **87×21px**, `white-space: nowrap` |

Finding 1 is this project's own defect, not an inherited one: the theme-registry change gave the third
appearance a focus colour equal to its rail colour. The palette record had measured a ring on the
ground and on a panel but never on the rail, so the failure sat exactly in the unmeasured case — which
is the argument for the "measured, not asserted" rule, not against it.

## What the review did not cover

Reported by the reviewer, kept here rather than quietly dropped:

- The Overview column drift was measured in the Light appearance only; the row markup is
  appearance-independent, so the geometry is identical — but Dark and Dusk were not re-measured.
- Feature-flag geometry was measured at 1280 and 390 only. **Closed here:** 1440 was measured after
  the fix, and the table fits (`1158 = 1158`).
- The rail ring was measured directly; the ring on every other focusable control (tables, fields,
  menus, toasts) was not measured individually in every appearance.
- Non-text contrast other than focus rings and icon strokes (input borders, hairlines, the ghost
  magnet edge) was not audited; the palette record lists rules as informational, not pass/fail.
- The `--focus-rail` tokens themselves are only exercised by the rail — if a future surface is painted
  with the rail colour, it needs the same treatment, which is why the base-layer rule keys off the
  region rather than off each control.
