#!/usr/bin/env node
// Copies the canonical docs markdown from docs/content/ into
// frontend/public/docs/ so the launcher's DocsView (which fetches
// /docs/<id>.md at runtime) keeps working off a static-export.
//
// Source of truth lives at the repo root so a separate GitHub Pages
// docs site can consume the same files. This script is the launcher's
// build-time mirror — it runs before `next dev` and `next build`.

import { cp, mkdir, readdir, rm } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const src = resolve(here, "../../docs/content");
const dst = resolve(here, "../public/docs");

async function main() {
  await mkdir(dst, { recursive: true });
  // Drop any stale .md files in the destination — covers the case
  // where a section was renamed or removed from the canonical source.
  for (const entry of await readdir(dst, { withFileTypes: true })) {
    if (entry.isFile() && entry.name.endsWith(".md")) {
      await rm(join(dst, entry.name));
    }
  }
  for (const entry of await readdir(src, { withFileTypes: true })) {
    if (!entry.isFile() || !entry.name.endsWith(".md")) continue;
    await cp(join(src, entry.name), join(dst, entry.name));
  }
  console.log(`sync-docs: copied ${src} -> ${dst}`);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
