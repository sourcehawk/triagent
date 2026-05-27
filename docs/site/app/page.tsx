import { promises as fs } from "node:fs";
import { join } from "node:path";
import { DocsView } from "@/components/DocsView";

// The docs root renders the "overview" section directly rather than
// redirecting — a static export served from a sub-path (GitHub Pages)
// doesn't get cleanly served by next/redirect, and a meta-refresh
// double-loads. Read the markdown at build time so the page ships as
// pre-rendered HTML, no flash-of-loading.
export default async function DocsHome() {
  const md = await fs.readFile(
    join(process.cwd(), "..", "content", "overview.md"),
    "utf8",
  );
  return <DocsView active="overview" markdown={md} />;
}
