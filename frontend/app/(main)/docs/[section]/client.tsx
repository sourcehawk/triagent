"use client";

import { usePathname, useRouter } from "next/navigation";
import { DocsView } from "@/components/docs/DocsView";
import { SECTION_IDS, type SectionID } from "@/lib/docs-sections";

const VALID_SECTIONS = new Set<SectionID>(SECTION_IDS);

// Reads the section id from the URL pathname. Cannot use useParams()
// here: the static export pre-renders only the "_" placeholder shell,
// so useParams() always returns "_" regardless of the real URL.
// usePathname() re-renders on intra-route navigation (clicking from one
// section to another in DocsView's left rail), which useParams + a
// one-shot useEffect would miss.
function readSectionFromPath(pathname: string | null): string {
  if (!pathname) return "";
  const m = pathname.match(/\/docs\/([^/]+)\/?$/);
  if (!m) return "";
  return decodeURIComponent(m[1]);
}

export function DocsSectionPageClient() {
  const pathname = usePathname();
  const router = useRouter();
  const raw = readSectionFromPath(pathname);

  // Static-export placeholder — render Loading until pathname resolves.
  if (!raw || raw === "_") {
    return (
      <main className="flex-1 overflow-y-auto px-6 py-12 text-sm text-zinc-500">
        Loading…
      </main>
    );
  }

  // Unknown section -> fall back to overview rather than 404; the docs
  // surface is exploratory and a wrong URL shouldn't dead-end.
  const section: SectionID = VALID_SECTIONS.has(raw as SectionID)
    ? (raw as SectionID)
    : "overview";

  return (
    <main className="flex-1 overflow-y-auto">
      <DocsView
        active={section}
        onSectionChange={(next) =>
          router.push(`/docs/${encodeURIComponent(next)}`)
        }
      />
    </main>
  );
}
