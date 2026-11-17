#!/usr/bin/env node
/** Guard: the console carries no demo scaffolding.
 *
 *  Every shipped module reads `src/lib/fleet.ts` or a local display preference. A demo token, a mock
 *  import, or a leftover mock reference means the console is advertising itself as a demo instead of
 *  showing the fleet — the exact failure epic #509 removes. The rules are syntactic, so a scan over
 *  the shipped tree is enough (design D6).
 *
 *  Exit 0 with a one-line summary when clean; exit 1 naming every offence. */

import { readdir, readFile } from "node:fs/promises";
import path from "node:path";

/** All of `src/`, the document head, and the public assets the browser downloads. */
const ROOTS = ["src", "public"];
const EXTRA_FILES = ["index.html"];

/** Scan text files only; images and fonts under `public/` are never copy. A file with no extension
 *  (`public/_redirects`) is text by convention and is scanned too. */
const TEXT = /\.(?:html|css|js|jsx|mjs|cjs|ts|tsx|json|svg|txt|md|map|webmanifest)$/i;
const isText = (name) => TEXT.test(name) || path.extname(name) === "";

/** Demo copy. `demo` is an ordinary word, so it is word-bounded; the rest are distinctive enough. */
const TOKENS = [
  /synthetic/i,
  /\bdemo\b/i,
  /admin-panel/i,
  /not a real service/i,
  /\bauthored\b/i,
];

/** Any import or re-export whose path reaches the mock module. */
const MOCK_IMPORT = /(?:from|import)\s*\(?\s*["'][^"']*\/mock(?:\/|["'])/;

async function walk(dir, out) {
  let entries;
  try {
    entries = await readdir(dir, { withFileTypes: true });
  } catch {
    return out;
  }
  for (const entry of entries) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      await walk(full, out);
      continue;
    }
    if (isText(entry.name)) out.push(full);
  }
  return out;
}

const files = new Set(EXTRA_FILES.filter((file) => isText(path.basename(file))));
for (const root of ROOTS) {
  for (const file of await walk(root, [])) files.add(file);
}

const offenders = [];
for (const file of [...files].sort()) {
  const source = await readFile(file, "utf8");
  source.split("\n").forEach((line, index) => {
    const offence =
      MOCK_IMPORT.test(line) || TOKENS.some((token) => token.test(line));
    if (offence) offenders.push({ file, line: index + 1, text: line.trim() });
  });
}

if (offenders.length === 0) {
  console.log(`check:demo — ${files.size} files scanned, no demo scaffolding.`);
  process.exit(0);
}

console.error("check:demo — demo scaffolding found:");
for (const offence of offenders) {
  console.error(`  ${offence.file}:${offence.line}  ${offence.text}`);
}
console.error(`\n${offenders.length} offence(s). Remove the demo copy, the mock import or the asset.`);
process.exit(1);
