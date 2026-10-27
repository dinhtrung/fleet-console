/** Formatting helpers. Everything the interface prints goes through here, so a value looks
 *  the same on every surface. */

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

/** Fixed "now" when the board is frozen for captures, so screenshots are reproducible. */
let nowOverride: number | null = null;

export function setNow(epochMs: number | null): void {
  nowOverride = epochMs;
}

export function now(): number {
  return nowOverride ?? Date.now();
}

export function relativeTime(iso: string): string {
  const then = new Date(iso).getTime();
  const delta = now() - then;
  if (Number.isNaN(then)) return "—";
  if (delta < MINUTE) return "just now";
  if (delta < HOUR) return `${Math.floor(delta / MINUTE)}m ago`;
  if (delta < DAY) return `${Math.floor(delta / HOUR)}h ago`;
  if (delta < 30 * DAY) return `${Math.floor(delta / DAY)}d ago`;
  return absoluteDate(iso);
}

export function absoluteDate(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "—";
  return d.toLocaleDateString("en-GB", { day: "2-digit", month: "short", year: "numeric" });
}

export function dateTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "—";
  return `${d.toLocaleDateString("en-GB", { day: "2-digit", month: "short", year: "numeric" })}, ${d.toLocaleTimeString(
    "en-GB",
    { hour: "2-digit", minute: "2-digit" },
  )}`;
}

export function isoDate(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "—";
  return d.toISOString().slice(0, 10);
}

export function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return "??";
  if (parts.length === 1) return parts[0]!.slice(0, 2).toUpperCase();
  return `${parts[0]![0]!}${parts[parts.length - 1]![0]!}`.toUpperCase();
}

export function truncate(value: string, max: number): string {
  if (value.length <= max) return value;
  return `${value.slice(0, Math.max(0, max - 1))}…`;
}

export function plural(count: number, one: string, many = `${one}s`): string {
  return `${count} ${count === 1 ? one : many}`;
}

export function number(value: number): string {
  return value.toLocaleString("en-GB");
}

export function signed(value: number): string {
  if (value === 0) return "0";
  return `${value > 0 ? "+" : "−"}${Math.abs(value).toLocaleString("en-GB")}`;
}

export function percent(value: number): string {
  return `${Math.round(value * 100)}%`;
}

export function slugify(value: string): string {
  return value
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 48);
}

export function isSlug(value: string): boolean {
  return /^[a-z0-9]+(-[a-z0-9]+)*$/.test(value) && value.length >= 3 && value.length <= 48;
}

/** Age of an ISO timestamp in whole days, used by the idle-expiry and recency logic. */
export function daysAgo(iso: string): number {
  return Math.floor((now() - new Date(iso).getTime()) / DAY);
}

export function duration(ms: number): string {
  const totalMinutes = Math.max(0, Math.floor(ms / MINUTE));
  const h = Math.floor(totalMinutes / 60);
  const m = totalMinutes % 60;
  if (h === 0) return `${m}m`;
  return `${h}h ${m}m`;
}

/** Field-level diffs are rendered as before → after; render an empty side as an explicit em dash. */
export function orDash(value: string | null | undefined): string {
  return value === null || value === undefined || value === "" ? "—" : value;
}
