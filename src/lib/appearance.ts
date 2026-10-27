/** Appearance: a registry of named appearances, resolved in one place.
 *
 *  An appearance is a set of the design system's semantic role tokens (defined in `src/index.css`),
 *  not a code path: adding one means adding a token block and an entry here, and nothing in a
 *  component, screen or shell file changes. The inline script in `index.html` resolves the same
 *  choice before the first paint — it cannot import this module, so it mirrors the id list and says
 *  so; keep the two in step.
 */

export type AppearanceId = "light" | "dark" | "dusk" | "ember" | "sage";

/** What the operator can choose: an appearance, or the machine's preference. */
export type AppearanceChoice = "system" | AppearanceId;

export type AppearanceDefinition = {
  id: AppearanceId;
  label: string;
  /** What the document declares to the browser, so native widgets match the appearance. */
  colorScheme: "light" | "dark";
  /** The single region that owns a saturated ground in this appearance (the committed-rail rule). */
  saturatedRegion: string;
  /** The scene the appearance is for, in the operator's terms — the switcher shows it as the hint. */
  useScene: string;
};

export const APPEARANCES: AppearanceDefinition[] = [
  {
    id: "light",
    label: "Light",
    colorScheme: "light",
    saturatedRegion: "the navigation rail, in ink",
    useScene: "daylight",
  },
  {
    id: "dark",
    label: "Dark",
    colorScheme: "dark",
    saturatedRegion: "the navigation rail, in plum",
    useScene: "night",
  },
  {
    id: "dusk",
    label: "Dusk",
    colorScheme: "light",
    saturatedRegion: "the navigation rail, in deep teal",
    useScene: "dim light",
  },
  {
    id: "ember",
    label: "Ember",
    colorScheme: "light",
    saturatedRegion: "the navigation rail, in clay brown",
    useScene: "warm light",
  },
  {
    id: "sage",
    label: "Sage",
    colorScheme: "light",
    saturatedRegion: "the navigation rail, in violet",
    useScene: "tinted paper",
  },
];

export const APPEARANCE_IDS: AppearanceId[] = APPEARANCES.map((appearance) => appearance.id);

export const APPEARANCE_KEY = "fleet-console.appearance";

export function isAppearanceId(value: string | null): value is AppearanceId {
  return value !== null && (APPEARANCE_IDS as string[]).includes(value);
}

/** The stored choice, or the machine's preference. A stale or unknown id falls back to the machine. */
export function readAppearance(): AppearanceChoice {
  try {
    const raw = localStorage.getItem(APPEARANCE_KEY);
    return isAppearanceId(raw) ? raw : "system";
  } catch {
    return "system";
  }
}

export function resolvedAppearance(choice: AppearanceChoice): AppearanceId {
  if (choice !== "system") return choice;
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

/** The label an operator sees for a choice, including the machine-following one. */
export function appearanceLabel(choice: AppearanceChoice): string {
  if (choice === "system") return "System";
  return APPEARANCES.find((appearance) => appearance.id === choice)?.label ?? choice;
}

export function applyAppearance(choice: AppearanceChoice): void {
  const resolved = resolvedAppearance(choice);
  document.documentElement.setAttribute("data-theme", resolved);
  const meta = document.querySelector('meta[name="color-scheme"]');
  if (meta) {
    const declared = choice === "system" ? "light dark" : (APPEARANCES.find((a) => a.id === choice)?.colorScheme ?? "light");
    meta.setAttribute("content", declared);
  }
}

export function writeAppearance(choice: AppearanceChoice): void {
  try {
    localStorage.setItem(APPEARANCE_KEY, choice);
  } catch {
    // storage unavailable: the choice still applies for this session
  }
  applyAppearance(choice);
}

/** When the operator has chosen "system", follow the OS as it changes during the session. */
export function watchSystemAppearance(onChange: () => void): () => void {
  const media = window.matchMedia("(prefers-color-scheme: dark)");
  const listener = () => {
    if (readAppearance() === "system") {
      applyAppearance("system");
      onChange();
    }
  };
  media.addEventListener("change", listener);
  return () => media.removeEventListener("change", listener);
}
