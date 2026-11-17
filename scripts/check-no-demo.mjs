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

/** Demo copy. `demo` is an ordinary word, so it is word-bounded; the rest are distinctive enough. */
const TOKENS = [
  /synthetic/i,
  /\bdemo\b/i,
  /admin-panel/i,
  /not a real service/i,
  /\bauthored\b/i,
];

/** Every static import/re-export, dynamic `import()` and `require()` whose path reaches the mock
 *  module. The scan runs over the whole file, because the path may sit on the line after the
 *  keyword. */
function mockImports(source) {
  const pattern =
    /(?:from|import|require)\s*\(?\s*["'][^"']*\/mock(?:\/|["'])/g;
  return [...source.matchAll(pattern)];
}

/** A NUL byte in the leading block marks a binary (image, font, archive): never copy, never scanned. */
function isBinary(buffer) {
  return buffer.subarray(0, 8000).includes(0);
}

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
    out.push(full);
  }
  return out;
}

const files = new Set(EXTRA_FILES);
for (const root of ROOTS) {
  for (const file of await walk(root, [])) files.add(file);
}

const offenders = [];
let scanned = 0;
for (const file of [...files].sort()) {
  const buffer = await readFile(file);
  if (isBinary(buffer)) continue;
  scanned += 1;
  const source = buffer.toString("utf8");
  source.split("\n").forEach((line, index) => {
    if (TOKENS.some((token) => token.test(line))) {
      offenders.push({ file, line: index + 1, text: line.trim() });
    }
  });
  for (const match of mockImports(source)) {
    const line = source.slice(0, match.index).split("\n").length;
    offenders.push({
      file,
      line,
      text: match[0].replace(/\s+/g, " ").trim(),
    });
  }
}

offenders.sort((a, b) => a.file.localeCompare(b.file) || a.line - b.line);

if (offenders.length === 0) {
  console.log(`check:demo — ${scanned} files scanned, no demo scaffolding.`);
  process.exit(0);
}

console.error("check:demo — demo scaffolding found:");
for (const offence of offenders) {
  console.error(`  ${offence.file}:${offence.line}  ${offence.text}`);
}
console.error(
  `\n${offenders.length} offence(s). Remove the demo copy, the mock import or the asset.`,
);
process.exit(1);
