# OpenSpec conventions — fleet-console

## What this repository is

An MIT-licensed design and scaffold for an internal control plane that runs agent fleets across many projects and
repos on OpenRig. The service will be one Go binary with SQLite; the board is React + Vite + Tailwind, inherited
from the `admin-panel` scaffold.

## Sources of truth

| Question | Single source | Lifecycle |
|---|---|---|
| What the system must do | the change's deltas, then `openspec/specs/**` after archive | Frozen once archived; changes only through a new change |
| How it is built | the change's `design.md` | Rewritten while the change is open, frozen at archive |
| What the product is | `PRODUCT.md` | Updated when product truth changes |
| What the workflow is | `docs/my-stack.md` | Living document; the loop's contract is §3 |
| What a surface looks like | `DESIGN.md` | Inherited design system; tokens measured from built code |

Bridge rule: a design decision that changes observable behaviour goes back into a change's delta spec. A rule that
exists only in `DESIGN.md` is behaviour nobody approved. A spec never dictates layout, colour or component structure.

## The frozen baseline rule

`openspec/specs/**` is frozen once a change is archived. Never hand-edit it; a later behaviour change is a new change
whose deltas the CLI merges on archive. A change that changes no behaviour declares `skip_specs: true` instead of
inventing a requirement.

## Spec language

English, imperative, one behaviour per requirement, at least one scenario each, and every requirement under 500
characters so `--strict` stays green.
