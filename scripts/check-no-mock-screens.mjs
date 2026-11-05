#!/usr/bin/env node
/** Guard: no screen reads the mock module.
 *
 *  Every shipped screen reads `src/lib/fleet.ts`. The mock module survives only for the identity
 *  scaffold (`src/auth/session.tsx`), so a screen importing it would ship fixtures as data — the
 *  exact failure the board-honesty change removes. A grep is enough because the rule is syntactic:
 *  the import path either names `../mock` or it does not (design D4).
 *
 *  Exit 0 when clean, 1 with the offending files when not. */

import { readdir, readFile } from "node:fs/promises";
import path from "node:path";

const SCREENS = "src/screens";
/** Any import or re-export whose path reaches the mock module. */
const MOCK_IMPORT = /(?:from|import)\s*\(?\s*["'][^"']*\/mock(?:\/|["'])/;

async function walk(dir) {
  const out = [];
  let entries;
  try {
    entries = await readdir(dir, { withFileTypes: true });
  } catch {
    return out;
  }
  for (const entry of entries) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      out.push(...(await walk(full)));
      continue;
    }
    if (/\.(ts|tsx)$/.test(entry.name)) out.push(full);
  }
  return out;
}

const files = await walk(SCREENS);
const offenders = [];

for (const file of files) {
  const source = await readFile(file, "utf8");
  const lines = source.split("\n");
  lines.forEach((line, i) => {
    if (MOCK_IMPORT.test(line)) {
      offenders.push({ file, line: i + 1, text: line.trim() });
    }
  });
}

if (offenders.length === 0) {
  console.log(`check:screens — ${files.length} screen files, none import the mock module.`);
  process.exit(0);
}

console.error("check:screens — these screens import the mock module and must read src/lib/fleet.ts:");
for (const o of offenders) {
  console.error(`  ${o.file}:${o.line}  ${o.text}`);
}
console.error(`\n${offenders.length} offending import(s). Delete the screen or point it at the real API.`);
process.exit(1);
