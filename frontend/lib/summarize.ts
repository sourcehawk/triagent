// extractSummarizeResult unwraps the strategies summarize tool's
// structured-output JSON. The MCP SDK serialises every handler's typed
// Out into JSON on the wire when the handler doesn't populate Content
// directly; rather than reach into the SDK and duplicate the markdown
// into Content on the server, we just unwrap the known shape here.
//
// New servers return `{markdown, evidence_markdown?}`; historic
// sessions stored before the split return `{markdown}` only — those
// keep rendering as a single amber card with evidence still inline.
// Returns null only when the result isn't valid JSON or carries no
// markdown — caller falls back to the raw tool card so the operator at
// least sees something.
export type SummarizeResult = {
  markdown: string;
  evidence: string | null;
};

export function extractSummarizeResult(result: string): SummarizeResult | null {
  try {
    const parsed = JSON.parse(result) as {
      markdown?: unknown;
      evidence_markdown?: unknown;
    };
    if (typeof parsed.markdown !== "string" || parsed.markdown.length === 0) {
      return null;
    }
    const evidenceRaw =
      typeof parsed.evidence_markdown === "string"
        ? parsed.evidence_markdown.trim()
        : "";
    return {
      markdown: parsed.markdown,
      evidence: evidenceRaw.length > 0 ? evidenceRaw : null,
    };
  } catch {
    return null;
  }
}
