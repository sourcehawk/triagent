import { promises as fs } from "node:fs";
import { join } from "node:path";
import { notFound } from "next/navigation";
import { DocsView } from "@/components/DocsView";
import { SECTION_IDS, isSectionID } from "@/lib/sections";

// Pre-render one HTML page per section. Required by `output: "export"`
// for any dynamic route; the params returned here become the static
// shell list shipped to GitHub Pages.
export function generateStaticParams() {
  return SECTION_IDS.map((section) => ({ section }));
}

// Force static rendering so the build inlines the markdown into each
// section's HTML. Without this, Next.js sometimes opts dynamic rendering
// when it sees fs reads — fatal for `output: "export"`.
export const dynamic = "force-static";

type Params = { section: string };

export default async function DocsSectionPage({
  params,
}: {
  params: Promise<Params>;
}) {
  const { section } = await params;
  if (!isSectionID(section)) notFound();
  const md = await fs.readFile(
    join(process.cwd(), "..", "content", `${section}.md`),
    "utf8",
  );
  return <DocsView active={section} markdown={md} />;
}
