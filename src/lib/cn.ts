import { clsx, type ClassValue } from "clsx";
import { extendTailwindMerge } from "tailwind-merge";

/**
 * Class names, with Tailwind conflicts resolved last-wins.
 *
 * The palette and the type scale are custom tokens, and tailwind-merge cannot tell `text-on-plum`
 * (a colour) from `text-body` (a font size) by name: without this configuration it puts both in the
 * same conflict group and silently drops the earlier one — which painted a primary button's label in
 * the page ink on a plum ground (1.52:1) instead of the intended on-plum colour. Declaring both
 * scales keeps `text-*` colours and `text-*` sizes independent, as they are in Tailwind itself.
 */
const twMerge = extendTailwindMerge({
  extend: {
    theme: {
      color: [
        "ground",
        "panel",
        "rail",
        "rail-ink",
        "hover",
        "selected",
        "ink",
        "ink-muted",
        "rule",
        "rule-strong",
        "attention",
        "on-attention",
        "plum",
        "on-plum",
        "focus",
      ],
      text: ["label", "body", "lead", "title"],
    },
  },
});

export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}
