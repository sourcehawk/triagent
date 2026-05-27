import { SessionDoc } from "@/components/sessions/SessionDoc";

// generateStaticParams is required by output: "export". We return a single
// placeholder path so Next.js emits the page shell; the actual slug is
// resolved client-side via usePathname() + api.getUpstreamSession(). The
// Go server's SPA fallback handler serves the nearest-parent index.html for
// any unknown /sessions/<slug>/ path.
export function generateStaticParams() {
  return [{ slug: "_" }];
}

export default function SessionPage() {
  return <SessionDoc />;
}
