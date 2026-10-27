#!/usr/bin/env node
/**
 * Generate the panel's mock organization marks with fal.ai.
 *
 * Run:  node scripts/generate-mock-assets.mjs [--model fal-ai/flux/schnell] [--only aurora-freight]
 *
 * Why this exists as a script rather than a one-off command: the images are *content*, and content
 * that enters the interface has to be reproducible and attributed. This writes the rasters into
 * `public/mock/` and appends the provenance record (model, prompt, date, seed) to
 * `docs/design/mock-assets.md` in the same run, so no raster can ship without its provenance.
 *
 * The key is read from FAL_KEY (environment, or the KEY=value lines of ~/.hermes/.env). It is never
 * written into the repository and never printed.
 *
 * Style constraints this script enforces on every prompt, because a generated mark that ignores the
 * design system is worse than no mark:
 *   - two palette colours only: plum #483048 and pale tint #D5DEF0, on the board ground #F0F0F0
 *   - flat vector geometry: no gradients, no bevels, no drop shadows, no glow (the board is flat)
 *   - no text, letters, numerals or words — generated lettering is illegible and these are marks
 *   - square, centred, generous margin, legible at 24px
 */

import { readFile, writeFile, mkdir, appendFile, rename, rm, stat } from "node:fs/promises";
import { existsSync } from "node:fs";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import path from "node:path";

const execFileP = promisify(execFile);
const ffmpeg = process.env.FFMPEG ?? "ffmpeg";

const ROOT = path.resolve(import.meta.dirname, "..");
const OUT_DIR = path.join(ROOT, "public", "mock");
const PROVENANCE = path.join(ROOT, "docs", "design", "mock-assets.md");

const args = process.argv.slice(2);
const flag = (name, fallback) => {
  const i = args.indexOf(name);
  return i >= 0 && args[i + 1] ? args[i + 1] : fallback;
};
const MODEL = flag("--model", "fal-ai/flux/schnell");
const ONLY = flag("--only", null);
/** Committed size. The model returns 1024², which is ~600KB per mark for something drawn at 24px. */
const SIZE = Number(flag("--size", "128"));

/** The eight seeded organizations, each with a motif that is geometric rather than pictorial. */
const ORGANIZATIONS = [
  { slug: "aurora-freight", name: "Aurora Freight", motif: "a rising arc over a horizontal rule" },
  { slug: "kestrel-health", name: "Kestrel Health", motif: "a rounded cross formed from four rectangles" },
  { slug: "meridian-analytics", name: "Meridian Analytics", motif: "a vertical meridian line crossed by three horizontal bars of different length" },
  { slug: "northline-logistics", name: "Northline Logistics", motif: "an upward chevron stacked over a square" },
  { slug: "halcyon-ports", name: "Halcyon Ports", motif: "two nested right angles suggesting a harbour wall" },
  { slug: "brightwater-co-op", name: "Brightwater Co-op", motif: "three concentric semicircles opening upward" },
  { slug: "verity-public-works", name: "Verity Public Works", motif: "a grid of nine squares with one square filled" },
  { slug: "lantern-union", name: "Lantern Union", motif: "a hexagon with a small filled square at its centre" },
];

const STYLE =
  "flat vector logo mark, geometric, bold simple shapes, two colours only (deep plum #483048 and pale " +
  "lavender #D5DEF0), the mark sitting alone on a plain off-white #F0F0F0 field, generous empty " +
  "margin around the mark, centred. Absolutely no gradient, no soft shading, no vignette, no glow, " +
  "no bevel, no 3d, no texture, no drop shadow, no coloured background fill or coloured square " +
  "behind the mark, no frame or border, no text, no letters, no numerals, no words, no glyph-like " +
  "detail. Hard flat edges only, high contrast, legible at 24 pixels.";

async function readKey() {
  if (process.env.FAL_KEY) return process.env.FAL_KEY.trim();
  for (const file of [path.join(process.env.HOME ?? "", ".hermes", ".env"), path.join(ROOT, ".env")]) {
    if (!existsSync(file)) continue;
    const line = (await readFile(file, "utf8"))
      .split("\n")
      .find((l) => l.startsWith("FAL_KEY="));
    if (line) return line.slice("FAL_KEY=".length).trim().replace(/^["']|["']$/g, "");
  }
  throw new Error("No FAL_KEY found in the environment or ~/.hermes/.env");
}

async function generate(key, org) {
  const prompt = `${STYLE}. Motif: ${org.motif}.`;
  const response = await fetch(`https://fal.run/${MODEL}`, {
    method: "POST",
    headers: { Authorization: `Key ${key}`, "Content-Type": "application/json" },
    body: JSON.stringify({ prompt, image_size: "square_hd", num_images: 1, output_format: "png" }),
  });
  if (!response.ok) {
    throw new Error(`${org.slug}: fal returned ${response.status} ${(await response.text()).slice(0, 200)}`);
  }
  const payload = await response.json();
  const image = payload.images?.[0];
  if (!image?.url) throw new Error(`${org.slug}: no image in the response`);

  const bytes = Buffer.from(await (await fetch(image.url)).arrayBuffer());
  const file = path.join(OUT_DIR, `${org.slug}.png`);
  await writeFile(file, bytes);

  // The model returns 1024². Committing that for a mark drawn at 24px costs ~600KB per file and
  // ~5MB for the set, so the raster is downscaled to SIZE before it enters the repository. The
  // original stays reproducible from the recorded seed and model.
  if (SIZE > 0 && ffmpeg) {
    const tmp = `${file}.full.png`;
    await rename(file, tmp);
    await execFileP(ffmpeg, [
      "-y", "-v", "error",
      "-i", tmp,
      "-vf", `scale=${SIZE}:${SIZE}:flags=lanczos`,
      "-pix_fmt", "rgb24",
      "-compression_level", "100",
      file,
    ]);
    await rm(tmp, { force: true });
  }

  const finalBytes = (await stat(file)).size;
  return { org, prompt, file, seed: payload.seed ?? null, model: MODEL, url: image.url, bytes: finalBytes };
}

const key = await readKey();
await mkdir(OUT_DIR, { recursive: true });

const targets = ORGANIZATIONS.filter((o) => !ONLY || o.slug === ONLY);
if (targets.length === 0) throw new Error(`--only ${ONLY} matched no organization`);

if (!existsSync(PROVENANCE)) {
  await mkdir(path.dirname(PROVENANCE), { recursive: true });
  await appendFile(
    PROVENANCE,
    "# Mock assets\n\nEvery raster in `public/mock/` is synthetic and generated; it is not a real company's\n" +
      "mark and must never be presented as one. Regenerate with `node scripts/generate-mock-assets.mjs`\n" +
      "(`--only <slug>` for one, `--model` to change the model, `FFMPEG=<path>` if ffmpeg is not on PATH).\n" +
      "The model returns 1024²; the committed file is downscaled to 128², which is 2× the largest size the\n" +
      "interface draws — the recorded model and seed reproduce the original.\n\n" +
      "| File | Organization | Model | Seed | Committed | Generated | Prompt |\n|---|---|---|---|---|---|---|\n",
  );
}

/** Rewrite the table row for a file rather than appending a second one when a mark is regenerated. */
async function recordRow(pathCell, row) {
  const table = await readFile(PROVENANCE, "utf8");
  const lines = table.split("\n");
  const index = lines.findIndex((line) => line.startsWith(`| \`${pathCell}\` |`));
  if (index >= 0) {
    lines[index] = row;
    await writeFile(PROVENANCE, lines.join("\n"));
    return "updated";
  }
  await appendFile(PROVENANCE, `${row}\n`);
  return "added";
}

const stamp = new Date().toISOString();
for (const target of targets) {
  const result = await generate(key, target);
  const pathCell = `public/mock/${target.slug}.png`;
  const how = await recordRow(
    pathCell,
    `| \`${pathCell}\` | ${target.name} | \`${result.model}\` | ${result.seed ?? "—"} | ${SIZE}×${SIZE} (from 1024²) | ${stamp} | ${result.prompt.replace(/\|/g, "\\|")} |`,
  );
  console.log(
    `✓ ${target.slug}.png  ${Math.round(result.bytes / 1024)} KB  seed=${result.seed ?? "—"}  (provenance ${how})`,
  );
}
console.log(`\nProvenance written to ${path.relative(ROOT, PROVENANCE)}`);
