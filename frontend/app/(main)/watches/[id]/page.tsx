import { WatchDetailClient } from "./client";

// generateStaticParams is required by output: "export". We return a single
// placeholder path so Next.js emits the page shell; the actual id is
// resolved client-side via useParams(). The Go server's SPA fallback
// handler serves the nearest-parent index.html for any unknown
// /watches/<id>/ path.
export function generateStaticParams() {
  return [{ id: "_" }];
}

export default function Page() {
  return <WatchDetailClient />;
}
