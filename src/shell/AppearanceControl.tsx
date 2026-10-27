/** Appearance switcher: one control that presents every registered appearance, replacing the row of
 *  segments that was sized for a fixed number of them.
 *
 *  Each entry previews its own appearance by carrying that appearance's `data-theme`, so the swatch is
 *  the appearance's real tokens resolved in a subtree rather than a copy of its colours kept elsewhere.
 *  A preview holding its own copy of the colours would be able to drift from what choosing it does;
 *  this one cannot.
 *
 *  Keyboard behaviour (open, move, choose, dismiss, focus return) comes from the `Menu` primitive; this
 *  file supplies the registry and the previews. */

import { useEffect, useState } from "react";
import { ChevronDown } from "lucide-react";
import {
  APPEARANCES,
  applyAppearance,
  appearanceLabel,
  readAppearance,
  resolvedAppearance,
  watchSystemAppearance,
  writeAppearance,
  type AppearanceChoice,
  type AppearanceId,
} from "../lib/appearance";
import { cn } from "../lib/cn";
import { Menu, type MenuItem } from "../components/ui";

/** Half ground, half the region that owns the saturated colour — the two things that differ most
 *  between appearances. Rendered inside the previewed appearance's scope, so the live tokens apply. */
function Swatch({ id }: { id: AppearanceId }) {
  return (
    <span
      data-theme={id}
      aria-hidden="true"
      className="inline-flex h-3.5 w-3.5 shrink-0 overflow-hidden rounded-[2px] border border-rule-strong"
    >
      <span className="h-full w-1/2 bg-ground" />
      <span className="h-full w-1/2 bg-rail" />
    </span>
  );
}

export function AppearanceControl({
  compact = false,
  tone = "rail",
}: {
  compact?: boolean;
  tone?: "rail" | "panel";
}) {
  const [choice, setChoice] = useState<AppearanceChoice>(() =>
    typeof window === "undefined" ? "system" : readAppearance(),
  );

  useEffect(() => {
    applyAppearance(choice);
  }, [choice]);

  useEffect(() => watchSystemAppearance(() => setChoice(readAppearance())), []);

  // "System" is a choice, not an appearance: it previews whatever the machine currently resolves to
  // and names it, so the operator can see what it is following rather than having to infer it.
  const followed = resolvedAppearance("system");
  const activePreview = choice === "system" ? followed : choice;

  const choose = (next: AppearanceChoice) => {
    setChoice(next);
    writeAppearance(next);
  };

  const items: MenuItem[] = [
    {
      id: "system",
      label: "System",
      hint: `follows ${appearanceLabel(followed).toLowerCase()}`,
      swatch: <Swatch id={followed} />,
      checked: choice === "system",
      onSelect: () => choose("system"),
    },
    ...APPEARANCES.map((appearance) => ({
      id: appearance.id,
      label: appearance.label,
      hint: appearance.useScene,
      swatch: <Swatch id={appearance.id} />,
      checked: choice === appearance.id,
      onSelect: () => choose(appearance.id),
    })),
  ];

  // The switcher is drawn on the rail in the shell and on a panel in Settings, so its colours come
  // from the surface it sits on rather than from one hard-coded tone.
  const triggerTone =
    tone === "rail"
      ? "border border-rail-ink/30 text-rail-ink hover:bg-rail-ink/10"
      : "border border-rule-strong text-ink hover:bg-hover";

  return (
    <Menu
      label="Appearance"
      triggerVariant="text"
      // The rail's switcher sits at the bottom of the viewport, so its list opens upward.
      placement={tone === "rail" ? "above" : "below"}
      align={tone === "rail" ? "start" : "end"}
      items={items}
      triggerClassName={cn(
        "flex w-full items-center gap-2 px-2 py-1.5 text-left text-[0.6875rem] font-semibold transition-colors",
        triggerTone,
        compact ? "" : "min-w-[9rem]",
      )}
      trigger={
        <>
          <Swatch id={activePreview} />
          <span className="truncate">{appearanceLabel(choice)}</span>
          <ChevronDown size={12} aria-hidden="true" className="ml-auto shrink-0 opacity-80" />
        </>
      }
    />
  );
}
