# Palette provenance — "Yoko Hanako 1109"

The palette is a **pinned choice**, not an invention: the brief required it to come from a real
COLOURlovers palette. It is recorded here with its source and the exact colour set, so the choice
stays auditable even if the source site disappears.

## Source

| Field | Value |
|---|---|
| Site | COLOURlovers |
| Palette | **Yoko Hanako 1109** |
| Palette id | 1004609 |
| Author | `_Mac_DyE_` |
| Original URL | https://www.colourlovers.com/palette/1004609/Yoko_Hanako_1109 |
| Retrieved from | Internet Archive snapshot `20190724012746` — https://web.archive.org/web/20190724012746id_/https://www.colourlovers.com/palette/1004609/Yoko_Hanako_1109 |

Why the archive: `colourlovers.com` answers every automated request (headless Chromium with a real
UA, and the extraction backend) with a Cloudflare bot challenge, and its JSON API was retired —
`GET /api/palettes/top` returns `{"error":"gone","message":"The COLOURlovers API closed when the site
was rebuilt in 2026."}`. The archived palette page carries the palette's own five swatches in
document order and its title, which is what is recorded above.

## The palette, verbatim

| Swatch | Role in the system |
|---|---|
| `#181818` | ink (light theme) / board ground (dark theme) |
| `#F0F0F0` | board ground (light theme) / ink (dark theme) |
| `#D5DEF0` | pale tint: selected row band, invited state, dark-theme accent text |
| `#903078` | attention: suspended state fill, focus ring, destructive confirm |
| `#483048` | plum: secondary text, rail and panel surfaces in dark theme, strongest chip fill |

## What the palette could not do, and the translation used

Light theme passes contrast everywhere it is used (lowest pair 6.40:1). In the dark theme
`#903078` is only **2.44:1** against `#181818` and 1.61:1 against `#483048`, so it can never be text
there. The roles are therefore preserved while the carrier changes:

- `#903078` is a **fill only** in both themes (white ink on it: 7.29:1).
- Where the light theme uses `#903078` as *text or a ring*, the dark theme uses `#D5DEF0`
  (13.14:1 on `#181818`).
- `#FFFFFF` and the rule/overlay opacities are derived neutrals, not palette members; no sixth hue
  is introduced.

## State vocabulary (the signature move)

Status is a **magnet**: a fixed-width chip in a fixed column slot, carrying a short code, a fill
weight and a hue — never colour alone.

| State | Code | Fill | Ink | Contrast |
|---|---|---|---|---|
| Active | `ACT` | plum `#483048` solid | `#F0F0F0` | 10.28:1 |
| Invited | `INV` | lavender `#D5DEF0` | plum `#483048` | 8.67:1 |
| Suspended | `SUS` | magenta `#903078` solid | `#FFFFFF` | 7.29:1 |
| Deactivated | `DEA` | none (ghost, 1px plum rule) | plum on ground | 10.28:1 |

All ratios computed with WCAG 2.1 relative luminance. The audit of every shipped text/ground pair
lives in `tasks.md` (build change) and `DESIGN.md`.

## Second seed palette — the third appearance ("Dusk")

The board's appearance is a registry, and an appearance may be seeded by its own pinned palette. The
third appearance is seeded by a second one, recorded here on the same terms as the first: a real
COLOURlovers palette, pinned, with its source and the exact colour set.

| Field | Value |
|---|---|
| Site | COLOURlovers |
| Palette | **Stormy Dusk** |
| Palette id | 100429 |
| Author | `junyr` |
| Original URL | https://www.colourlovers.com/palette/100429/Stormy_Dusk |
| Retrieved from | Internet Archive snapshot `20130622165810` — https://web.archive.org/web/20130622165810id_/http://www.colourlovers.com/palette/100429/Stormy_Dusk |

Same retrieval route as the first palette, for the same reason: `colourlovers.com` answers automated
requests with a Cloudflare challenge, so the record comes from the archived page, which carries the
palette's five swatches in document order and its title.

### The palette, verbatim, and the roles it took

| Swatch | Role in the third appearance |
|---|---|
| `#ABC5C9` | board ground (mist) and the rail's label ink |
| `#0E7583` | teal — the selected-row band, laid over the mist so it reads at 4.69:1 |
| `#4A4B4B` | slate — secondary text on the ground (4.82:1) and on a panel (7.33:1) |
| `#083A52` | deep — the rail, the one region owning a saturated ground, and the focus ring |
| `#1A3E42` | charcoal — primary text and the rules |

Two decisions worth stating rather than hiding:

- **The semantic hues do not move.** `attention` stays `#903078` and `plum` stays `#483048` in every
  appearance, taken from the first palette. A state that changes colour when the lighting changes is a
  state the operator cannot learn; the appearance changes the board, not the vocabulary.
- **Focus changes carrier, as it already did between light and dark.** `#903078` measures below the
  3:1 ring threshold on the mist ground, so focus is carried by the deep swatch (10.12:1 on a panel),
  exactly as the dark appearance hands focus to the tint rather than to the attention colour.

## Third seed palette — the fourth appearance ("Ember")

| Field | Value |
|---|---|
| Site | COLOURlovers |
| Palette | **Clay** |
| Palette id | 1001576 |
| Author | `eponine` |
| Original URL | https://www.colourlovers.com/palette/1001576/Clay |
| Retrieved from | Internet Archive snapshot `20100326071244` — https://web.archive.org/web/20100326071244id_/http://www.colourlovers.com/palette/1001576/Clay |

### The palette, verbatim, and the roles it took

| Swatch | Role in the fourth appearance |
|---|---|
| `#BF7436` | tan — mixed toward paper for the ground (`#ECD5C3`) and for the panels (`#F7EEE7`) |
| `#8C5223` | brown — mixed with the dark swatch for secondary text (`4.92:1` on the ground) |
| `#703E14` | dark brown — primary text, the rules, **and** the rail, which is the one region owning a saturated ground |
| `#AD5207` | burnt orange — the selected-row band and the focus ring on the panels |
| `#F77D19` | **not used.** At any structural size this chroma competes with the attention fill, which has to remain the one thing that shouts; recorded here so the exclusion is a decision rather than an omission |

The warm appearance exists for the case the other three do not cover: a warm light. Light is a cool
daylight, Dusk a dim cool room, Dark the dark — none of them is a room lit by something warm, and the
rail spends its single saturated region on the warm brown rather than on ink, plum or teal.

## Fourth seed palette — the fifth appearance ("Sage")

| Field | Value |
|---|---|
| Site | COLOURlovers |
| Palette | **Limelicious** |
| Palette id | 1077213 |
| Author | `gracefulNothing` |
| Original URL | https://www.colourlovers.com/palette/1077213/Limelicious |
| Retrieved from | Internet Archive snapshot `20100122015359` — https://web.archive.org/web/20100122015359id_/http://www.colourlovers.com/palette/1077213/Limelicious |

Same retrieval route as the other three, for the same reason: the live site answers automated requests
with a Cloudflare challenge and its JSON API was retired, so the record comes from the archived page,
which carries the palette's five swatches in document order and its title.

### The palette, verbatim, and the roles it took

| Swatch | Role in the fifth appearance |
|---|---|
| `#ADB08B` | sage — the ground (30% toward paper, `#E6E7DC`), the panel (12%, `#F5F6F1`) and the focus ring on the rail |
| `#535735` | olive — the focus ring on the board, and 70/30 with the violet for secondary text (`#49423C`) |
| `#729478` | leaf — recorded; the appearance needs no further structural colour, so this one is vocabulary rather than token |
| `#31114D` | violet — the ink, the rail (**the one region owning a saturated ground**) and, 24% into the ground, the selected-row band (`#BBB4BA`) |
| `#EB4123` | **not used.** At any structural size this red-orange competes with the attention fill, which has to remain the one thing that shouts; recorded here so the exclusion is a decision rather than an omission |

Two decisions worth stating rather than hiding:

- **The palette's one high-chroma member is spent on the rail.** The violet carries the ink as well,
  because the palette's other dark member is an olive whose own contrast against a sage ground is too
  low to be body text; the rail is then the region that owns the saturated ground, as the committed-rail
  rule asks.
- **The ring changes carrier, and neither carrier is the ink.** The violet on a panel would be a second
  reading of the ink, and on the rail it would be the rail's own colour — 1.00:1, i.e. no ring at all.
  The olive (`#535735`, 6.05:1 on the ground and 6.95:1 on a panel) carries the board, and the sage
  (`#ADB08B`, 7.10:1 on the violet) carries the rail.

### Candidate palettes screened for the fifth appearance

The seed was chosen by screening archived palettes with the composition recipe above: **50 palettes**
fetched from the Internet Archive, **20** clearing the contrast bar, **9** of those also quiet enough in
tone (core saturation ≤ 0.50 against Ember's 0.695). The shortlist below was refused, and the refusals
are recorded so the next appearance does not re-screen them:

| Palette | Id | Refused because |
|---|---|---|
| "Blue Cake" by MargaretRose | 1077208 | the quietest of the sample, but its ground is a near-neutral cool white and its rail a desaturated slate — a further reading of colours Light and Dusk already show |
| "Standard Camo" by Xaviara | 1188389 | a new khaki ground, but its ink and rail are a brown in the same family as Ember's `#703E14` |
| "slap my face twice" by tvr | 1007149 | the widest contrast margin of the sample (7.82:1), but its ink and secondary colour are plum-blacks sitting beside the semantic plum, and its near-black rail leaves no saturated region to name |
| "transfusion" by fuzzy ort | 1077234 | ink `#7C3249`, a plum-red — the family of the semantic plum |
| "Warmer Tones" by wackzingo | 1007174 | secondary colour `#735C84`, an orchid in the plum family |
| "The Happy Widow" by Ablep | 100713 | its pale swatch is a high-chroma mint doing structural work (ground tint and rail ring) |

The refusals are scoped to that sample: three of them are refusals under the semantic-vocabulary rule
this change states, and the rule is what the next screening has to apply.

## Measured contrast, every appearance

Computed from the tokens as the browser resolves them (custom properties are not resolved by
`getComputedStyle`, so each value is painted onto a probe element and read back), with alpha
composited before the ratio is taken.

| Pair | Minimum | Light | Dark | Dusk | Ember | Sage |
|---|---|---|---|---|---|---|
| body text on the ground | 4.5:1 | 15.58 | 15.58 | 6.38 | 6.24 | **12.77** |
| body text on a panel | 4.5:1 | 17.76 | 14.38 | 9.71 | 7.70 | **14.67** |
| muted text on the ground | 4.5:1 | 10.28 | 13.14 | 4.82 | 4.92 | **7.91** |
| muted text on a panel | 4.5:1 | 11.72 | 12.12 | 7.33 | 6.07 | **9.09** |
| body text on a selected row | 4.5:1 | 13.14 | 8.10 | 4.69 | 4.68 | **7.85** |
| rail label on the rail | 4.5:1 | 15.58 | 10.28 | 6.65 | 6.24 | **12.77** |
| text on a plum fill | 4.5:1 | 10.28 | 10.28 | 10.28 | 10.28 | **10.28** |
| text on an attention fill | 4.5:1 | 7.29 | 7.29 | 7.29 | 7.29 | **7.29** |
| focus ring on a panel | 3:1 | 7.29 | 12.12 | 10.12 | 4.61 | **6.95** |
| focus ring on the ground | 3:1 | 6.40 | 13.14 | 6.65 | 3.74 | **6.05** |
| focus ring **on the rail** | 3:1 | 13.14 | 8.67 | 6.65 | 6.24 | **7.10** |
| hairline rule on a panel *(informational)* | — | 1.40 | 1.71 | 1.52 | 1.48 | **1.65** |
| strong rule on a panel *(informational)* | — | 2.17 | 2.86 | 2.51 | 2.32 | **3.04** |

Every appearance passes every minimum; the light column reproduces the 6.40:1 the first palette's
record already claimed, which is what makes these numbers comparable rather than new. Rules are listed
as information, not as a pass/fail: a row is identified by its content and its slot, and the hairline
is a separator rather than the only thing distinguishing one row from the next.
