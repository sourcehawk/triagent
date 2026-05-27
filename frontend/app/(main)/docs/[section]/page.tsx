import { DocsSectionPageClient } from "./client";

// generateStaticParams is required by output: "export". We return a
// single placeholder path so Next.js emits the page shell; the actual
// section id is resolved client-side via usePathname(). The Go server's
// SPA fallback handler serves the nearest-parent index.html for any
// unknown /docs/<section>/ path.
export function generateStaticParams() {
  return [{ section: "_" }];
}

export default function DocsSectionPage() {
  return <DocsSectionPageClient />;
}
