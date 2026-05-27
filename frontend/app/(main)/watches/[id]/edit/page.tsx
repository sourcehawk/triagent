import { EditWatchClient } from "./client";

// generateStaticParams is required by output: "export". Placeholder path;
// the real id is resolved client-side via useParams().
export function generateStaticParams() {
  return [{ id: "_" }];
}

export default function EditWatchPage() {
  return <EditWatchClient />;
}
