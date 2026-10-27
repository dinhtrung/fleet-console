---
name: admin-panel
description: An operable admin board — ruled surfaces, magnet state tokens, one committed palette.
colors:
  board-ground: "#F0F0F0"
  board-panel: "#FFFFFF"
  board-rail: "#181818"
  board-rail-ink: "#F0F0F0"
  board-selected: "#D5DEF0"
  ink: "#181818"
  ink-muted: "#483048"
  plum: "#483048"
  on-plum: "#F0F0F0"
  attention: "#903078"
  on-attention: "#FFFFFF"
  overlay-scrim: "#1818188C"
  shadow-soft: "#00000073"
  shadow-deep: "#000000B3"
  # The third appearance's seed palette (COLOURlovers 100429 "Stormy Dusk" by junyr). Recorded here
  # because these five swatches are the appearance's whole colour vocabulary — provenance and the
  # role each took are in docs/design/palette.md.
  dusk-mist: "#ABC5C9"
  dusk-teal: "#0E7583"
  dusk-slate: "#4A4B4B"
  dusk-deep: "#083A52"
  dusk-charcoal: "#1A3E42"
  # The fourth appearance's seed palette (COLOURlovers 1001576 "Clay" by eponine). The palette's fifth
  # swatch, #F77D19, is deliberately not used and so is not a token: see docs/design/palette.md.
  clay-orange: "#AD5207"
  clay-tan: "#BF7436"
  clay-brown: "#8C5223"
  clay-dark: "#703E14"
  # The fifth appearance's seed palette (COLOURlovers 1077213 "Limelicious" by gracefulNothing). The
  # palette's fifth swatch, #EB4123, is deliberately not used and so is not a token: see
  # docs/design/palette.md.
  sage-olive: "#535735"
  sage: "#ADB08B"
  sage-leaf: "#729478"
  sage-violet: "#31114D"
typography:
  display:
    fontFamily: "Archivo Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "2.25rem"
    fontWeight: 600
    lineHeight: 1.05
    letterSpacing: "-0.03em"
  title:
    fontFamily: "Archivo Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "1.25rem"
    fontWeight: 600
    lineHeight: 1.2
    letterSpacing: "-0.015em"
  lead:
    fontFamily: "Archivo Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "0.875rem"
    fontWeight: 400
    lineHeight: 1.5
  body:
    fontFamily: "Archivo Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "0.8125rem"
    fontWeight: 400
    lineHeight: 1.5
  label:
    fontFamily: "Archivo Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "0.6875rem"
    fontWeight: 600
    lineHeight: 1
    letterSpacing: "0.07em"
  micro:
    fontFamily: "Azeret Mono Variable, ui-monospace, SFMono-Regular, monospace"
    fontSize: "0.625rem"
    fontWeight: 400
    lineHeight: 1.4
  data:
    fontFamily: "Azeret Mono Variable, ui-monospace, SFMono-Regular, monospace"
    fontSize: "0.6875rem"
    fontWeight: 400
    lineHeight: 1.4
rounded:
  hairline: "1px"
  chip: "2px"
  pill: "9999px"
spacing:
  cell-x: "12px"
  cell-y: "8px"
  control: "36px"
  row: "44px"
  hit-area: "44px"
components:
  button-primary:
    backgroundColor: "{colors.plum}"
    textColor: "{colors.on-plum}"
    rounded: "{rounded.chip}"
    padding: "0 12px"
    height: "36px"
  button-attention:
    backgroundColor: "{colors.attention}"
    textColor: "{colors.on-attention}"
    rounded: "{rounded.chip}"
    padding: "0 12px"
    height: "36px"
  button-outline:
    backgroundColor: "{colors.board-panel}"
    textColor: "{colors.ink}"
    rounded: "{rounded.chip}"
    padding: "0 12px"
    height: "36px"
  field:
    backgroundColor: "{colors.board-panel}"
    textColor: "{colors.ink}"
    rounded: "{rounded.chip}"
    padding: "0 8px"
    height: "36px"
  magnet-active:
    backgroundColor: "{colors.plum}"
    textColor: "{colors.on-plum}"
    rounded: "{rounded.chip}"
    size: "88px x 22px"
  magnet-invited:
    backgroundColor: "{colors.board-selected}"
    textColor: "{colors.ink-muted}"
    rounded: "{rounded.chip}"
    size: "88px x 22px"
  magnet-suspended:
    backgroundColor: "{colors.attention}"
    textColor: "{colors.on-attention}"
    rounded: "{rounded.chip}"
    size: "88px x 22px"
---

# Design System: admin-panel

## Overview

**Creative North Star: "The Handover Board"**

This is a working board that is read at a glance, not a set of dashboards stacked in a scrolling
column. Everything on it descends from one decision: **state is a physical token that sits in a fixed
slot**. A row is a slot-aligned record; its status is a magnet in a dedicated column; the magnet keeps
its position and its weight until someone moves it. That single idea produces the density, the fixed
column widths, the refusal of elevation, and the way a change reads as a thing that happened rather
than a thing that was announced.

The surface language is deliberate opposition to the generated-admin default. There is no grid of
identical rounded KPI cards, no icon-per-navigation-item rail, no coloured pill invented per screen,
no gradient, no glass, no accent glow. The board is **ruled, not shadowed**: separation is a 1px rule,
selection is a tinted band, and the only elevation in the system belongs to overlays. Corners are
square to 2px because a magnet has square corners and a board is printed, not extruded.

Personality without ornament comes from three places: a committed rail (the one surface where colour
takes a whole region), an institutional grotesk set with tabular numerals so columns align down the
page, and restraint in the one place it matters most — colour is only ever spent on a job.

**Key Characteristics:**
- Ruled surfaces; one elevation, overlays only
- Fixed-width columns and fixed-slot state tokens — rows align or they cannot be scanned
- A five-colour palette, each colour with a job; nothing decorative
- Density treated as a feature: identical row height, label-above-value, no whitespace theatre
- Every state (empty, loading, failed, denied, expired) designed as part of the board

## Colors

One committed palette, taken from a real COLOURlovers palette (provenance: `docs/design/palette.md`),
where every colour does work: ground, rail, ink, tint, and attention.

### Primary
- **Magnet Magenta** (`#903078`): attention and only attention — the suspended state's fill, the focus
  ring in light theme, and the destructive confirmation. It is never a background wash and never
  decoration, so when it appears something is genuinely wrong or irreversible.
- **Board Plum** (`#483048`): the strongest fill in the system — the active state's magnet, the primary
  button, secondary text, and the dark theme's rail and panel surfaces. It is the "settled" weight.

### Neutral
- **Board Ground** (`#F0F0F0`): the application ground in light theme and the ink in dark. A printed
  board, not white paper and not grey felt.
- **Panel White** (`#FFFFFF`): panels in light theme — one step above the ground, separated by a rule.
- **Board Ink** (`#181818`): text in light theme; the rail ground and the dark theme's ground.
- **Rule** (`color-mix(in srgb, var(--ink) 16%, transparent)`): every separator. Rules are printed
  lines, never borders that imply a container.
- **Pale Tint** (`#D5DEF0`): the selected row band, the invited state's magnet, and the dark theme's
  accent text — the only soft colour in the system.

### Named Rules
**The Magnet Rule.** State is never colour alone. Every state token carries a three-letter code
(`ACT` / `INV` / `SUS` / `DEA`), a fill weight (ghost → pale → solid) and a hue, and its full word is in
the accessible name. The board must survive greyscale, colour blindness and a screenshot.

**The Committed Rail Rule.** Exactly one region of the interface owns a saturated ground: the
navigation rail (ink in light theme, plum in dark). Everywhere else, colour is a token in a slot.

## Typography

**Display Font:** Archivo Variable (with ui-sans-serif, system-ui, sans-serif)
**Body Font:** Archivo Variable — the same family, separated by weight, size and tracking
**Label/Mono Font:** Azeret Mono Variable — for identifiers, IP addresses, timestamps and event codes

**Character:** An institutional grotesk with tabular numerals: the voice of a printed board and a sign
in a corridor, legible across a room and unmistakably not a system default. The mono is a data voice,
not a costume — it appears only where a value is an identifier or a measurement.

### Hierarchy
- **Display** (600, 2.25rem/36px, line-height 1.05, tracking −0.03em): the sign-in surface's own
  heading, and the only place the type scale goes above the title step. It carries the product's name
  once, on the one screen that has no board to show.
- **Title** (600, 1.25rem/20px, line-height 1.2, tracking −0.015em): the page's own name, once, in the
  page header. Nothing competes with it.
- **Lead** (400, 0.875rem/14px, 1.5): panel headings and empty-state statements.
- **Body** (400, 0.8125rem/13px, 1.5): every cell, label-adjacent value and sentence. Maximum measure
  is held around 60–75ch on prose; tables are dense by design and use fixed column widths instead.
- **Label** (600, 0.6875rem/11px, tracking 0.07em, uppercase): column headers, field labels, section
  names. The board's printed label grammar.
- **Data** (Azeret Mono, 0.6875rem/11px): ids, IPs, slugs, action names, absolute timestamps.
- **Micro** (Azeret Mono, 0.625rem/10px): the second line under an identity — an email address or a
  fingerprint — where the label above already carries the meaning.

### Named Rules
**The Tabular Rule.** Any number the reader compares vertically is set with `font-variant-numeric:
tabular-nums` — tables, counts, deltas and times. A column of numbers that does not align is a defect,
not a style choice.

## Layout

A two-column shell on large screens: a 15rem rail and the board. The rail carries the wordmark, the
scope selector, grouped navigation and the operator's identity block; it is full height and does not
scroll with the content. Below 1024px the rail becomes an overlay drawer opened from the top bar — no
destination is removed, and closing it returns focus to the control that opened it.

The board is a vertical stack of full-width panels separated by rules, never a card grid. Each panel
opens with a ruled head: `label`-set title on the left, count beside it, actions right. Content rows
are one fixed height, with the identity column pinned left and the state token in its own fixed slot.
Cell padding is 12px horizontal, 8px vertical; controls are 36px tall with a 44px hit area.

The page header is its own band: breadcrumb, then title, count and actions on one line, description
under it. Density is high and uniform — the board earns the right to be quiet by being legible.

## Elevation & Depth

**This system is flat.** Depth is conveyed by rules, tinted bands and one elevation reserved
exclusively for overlays (dialogs, menus, toasts). There are no ambient shadows on panels, no hover
lift, no bevel and no gradient. A row is separated from the next by a 1px rule; a selected row is a
tinted band; a state change moves a token rather than animating a surface.

### Shadow Vocabulary
- **Overlay** (`0 10px 24px -12px rgb(0 0 0 / 0.45)` — the `shadow-soft` token): dialogs, menus and
  toasts only — the one place where a surface genuinely floats above the board. In dark theme the same
  role deepens to `0 12px 28px -12px rgb(0 0 0 / 0.7)` (the `shadow-deep` token).
- **Scrim** (`rgb(24 24 24 / 55%)` — the `overlay-scrim` token): the backdrop behind a modal dialog and
  behind the side panel when it takes the full width. It dims the board; it is not a surface.

### Named Rules
**The Ruled-Not-Shadowed Rule.** If two regions need separating, draw a line. Reach for the overlay
shadow only when the region floats above the board and can be dismissed.

## Appearances

Appearance is a **registry**, not a light/dark pair. An appearance is a named set of the semantic role
tokens in `src/index.css`; registering one is a token block plus an entry in `src/lib/appearance.ts`,
and no component, screen or shell file changes. The control lists the registry, and a stored id that is
no longer registered falls back to the machine's preference instead of leaving the board unstyled.

Five ship today:

| Appearance | Seed | Ground | Rail — the one saturated region | Use scene |
|---|---|---|---|---|
| **Light** | "Yoko Hanako 1109", COLOURlovers 1004609 | `#F0F0F0` | ink `#181818` | the workstation in daylight |
| **Dark** | the same palette, roles remapped | `#181818` | plum `#483048` | a dark room, a screen at night |
| **Dusk** | "Stormy Dusk", COLOURlovers 100429 | mist `#ABC5C9` | deep `#083A52` | dim light: less glare than Light, still a printed board rather than a black screen |
| **Ember** | "Clay", COLOURlovers 1001576 | clay `#ECD5C3` | dark brown `#703E14` | a warm light — the one scene the other three do not cover |
| **Sage** | "Limelicious", COLOURlovers 1077213 | sage `#E6E7DC` | violet `#31114D` | a tinted form paper — the first appearance whose ground is neither a neutral, a mist nor a tan |

The control that presents them is a **switcher**, not a row of segments: a row is sized for a fixed
number of appearances and quietly stops working as the registry grows, which is the opposite of what a
registry is for. Each row in the switcher previews its own appearance — the swatch carries that
appearance's `data-theme`, so it is the appearance's real tokens resolved in a subtree rather than a
copy of its colours kept somewhere else, and a preview cannot drift from what choosing it does.

Rules that hold in every appearance:

- **Composed, never inverted.** Each appearance defines its own surfaces, ink and rules from its own
  seed. Dusk is not "Light, but blue", and Dark is not Light flipped.
- **One saturated region.** Exactly one region owns a saturated ground, and it is the rail.
- **Semantics do not move.** Attention and plum keep their hues in every appearance. A state that
  changes colour when the lighting changes is a state the operator cannot learn — the appearance
  changes the board, not the vocabulary of state.
- **Measured, not asserted.** Every text, control and focus pair is computed from the tokens as the
  browser resolves them, and recorded in [`docs/design/palette.md`](docs/design/palette.md).
- **The ring belongs to the surface behind it.** A focus ring is drawn on whatever sits behind the
  control, so the rail — a saturated surface — takes its own ring colour (`--focus-rail`) while panels
  take `--focus`, and rail-surfaced regions re-point the token in one base-layer declaration rather
  than per control. A ring the colour of its own background is not a ring: Dusk shipped exactly that
  until the finish review caught it
  ([`docs/design/finish-review.md`](docs/design/finish-review.md)).

## Icons and marks

Two icon libraries, on purpose, with a rule that keeps them from blending into mush:

- **lucide-react is the primary family.** Nearly everything uses it — chevrons, search, destructive
  actions in menus, page actions. Its 24px grid and 2px stroke are the reference.
- **@tabler/icons-react covers the gaps**, and only where a glyph would otherwise have to be
  hand-authored. It was added to replace hand-written SVG paths (the toast tick, warning and dismiss
  marks; the delta carets; the checkbox tick), not to be a second opinion on the same glyphs. Same
  24px grid, so the two sit together without a visible seam.
- **One family per control.** Never two vendors inside a single control or a single row of glyphs —
  that is the failure mode that makes an icon set look assembled rather than designed.
- An icon is never the only carrier of meaning: every status has its word, every icon-only button has
  an accessible name, and the state magnets carry a three-letter code so state survives greyscale.

**Marks.** The organization marks in `public/mock/` are synthetic rasters generated with fal.ai —
never a real company's logo, and attributed with model, seed and full prompt in
`docs/design/mock-assets.md`. They are drawn at 22–28px from a 128px source, sit inside a 2px chip
radius with a hairline rule like every other object on the board, and degrade to a monogram if the
raster is missing: a mark that fails to load must not leave a hole in a row.

## Shapes

Square to nearly square: the only rounding in the system is a **2px chip radius**, applied to buttons,
fields, magnets, badges and checkboxes alike, and it is what makes a state token read as a physical
magnet rather than a pill. Rules are hairlines (1px) and never rounded. The single exception to the
square vocabulary is the **pill** (9999px), used only by the loading spinner — the one place a full
circle appears in the interface. There is no clipping, no blob, no decorative geometry. Status codes,
initials and counts sit inside rectangles; that is the entire form vocabulary.

## Components

### Buttons
- **Shape:** 2px radius, 36px tall, 12px horizontal padding, `label`/body type at 600 weight.
- **Primary:** plum ground with `on-plum` ink — the single main action of a screen.
- **Attention:** magnet-magenta ground with white ink, used **only** inside a destructive confirmation.
- **Outline:** panel ground with a `rule-strong` edge; the default for secondary actions.
- **Quiet / Text:** no edge; text-weight affordances for row-level actions and sign-out.
- **Hover / Focus:** background shifts by a colour-mix step; focus is a 2px `focus` ring with 2px
  offset. Small controls grow a 44px hit area through a pseudo-element rather than by being drawn large.

### Chips
- **Magnet (signature):** fixed 88×22px token in a fixed column slot, 11px `label` type with a
  three-letter code, one of four fill weights: solid plum (active), pale tint (invited), solid magenta
  (suspended), ghost with a plum rule (deactivated).
- **Badge:** quieter than a magnet — 1px rule and `ink-muted` text, or a tinted band. Roles, plans and
  counts use badges so the state column stays the loudest thing on a row.

### Cards / Containers
- **Corner Style:** 2px.
- **Background:** panel; the ground shows through only as page background.
- **Shadow Strategy:** none (see Elevation) — panels are separated from each other by rules.
- **Border:** 1px `rule`.
- **Internal Padding:** 12px horizontal, 8–12px vertical. Panels are never nested.

### Inputs / Fields
- **Style:** panel ground, 1px `rule-strong` edge, 2px radius, 36px tall, label above in `label` type.
- **Focus:** the shared 2px focus ring, never a glow.
- **Error / Disabled:** error sets the edge to `attention`, prints the message under the field and
  associates it via `aria-describedby`; disabled drops to 45% opacity with a not-allowed cursor.

### Navigation
- **Style:** rail with grouped sections, each group titled in `label` type at 60% opacity. Items are
  36px tall with a **slot marker** — a small filled bar in a fixed left slot — rather than a coloured
  edge. Active items take a lighter rail band and full-strength ink.
- **Mobile:** the same list in an overlay drawer, dismissed by Escape or a backdrop click.

### The Board Controls (signature, secondary)
The rail's tool menu deliberately exposes the seams of a demo: fail the next request, fail every
request, remove simulated latency, reset the board to its seed. A showcase that cannot show its own
failure states is a picture of a product, so the failure switch is part of the interface.

## Do's and Don'ts

### Do:
- **Do** separate with a 1px rule, select with a tinted band, and reserve the overlay shadow for things
  that float and dismiss.
- **Do** put state in a fixed-width magnet carrying a code, a fill weight and a hue, with the word in
  the accessible name.
- **Do** keep numbers tabular and columns fixed so a column of values aligns down the page.
- **Do** design the empty, loading, failed and denied state of every list before calling it finished.
- **Do** spend magenta only on attention and destruction — its rarity is the entire signal.

### Don't:
- **Don't** introduce a sixth hue, an ambient shadow, a gradient, a blur or a glow. The palette is
  closed and the board is flat.
- **Don't** nest panels, and don't express status with a coloured left border on a row.
- **Don't** use a magnet for something that is not a state (counts, plans and roles are badges).
- **Don't** set prose in the mono face, or use a unicode glyph as an icon — icons are drawn.
- **Don't** make light or dark a preference by category: the scene decides, and both themes are first
  class, contrast-audited and shipped together.
