import { describe, it, expect } from "vitest";
import { extractSummarizeResult } from "./summarize";

describe("extractSummarizeResult", () => {
  it("splits markdown and evidence_markdown into separate bodies", () => {
    const wire = JSON.stringify({
      markdown: "## Symptom\n\nfoo",
      evidence_markdown: "## Evidence\n\n- bar",
    });
    const out = extractSummarizeResult(wire);
    expect(out).not.toBeNull();
    expect(out?.markdown).toBe("## Symptom\n\nfoo");
    expect(out?.evidence).toBe("## Evidence\n\n- bar");
  });

  it("returns null evidence when evidence_markdown is missing (legacy session)", () => {
    const wire = JSON.stringify({ markdown: "## Symptom\n\nfoo" });
    const out = extractSummarizeResult(wire);
    expect(out).not.toBeNull();
    expect(out?.markdown).toBe("## Symptom\n\nfoo");
    expect(out?.evidence).toBeNull();
  });

  it("returns null evidence when evidence_markdown is an empty string", () => {
    const wire = JSON.stringify({ markdown: "verdict", evidence_markdown: "" });
    const out = extractSummarizeResult(wire);
    expect(out?.evidence).toBeNull();
  });

  it("returns null evidence when evidence_markdown is whitespace only", () => {
    const wire = JSON.stringify({
      markdown: "verdict",
      evidence_markdown: "   \n\n  ",
    });
    const out = extractSummarizeResult(wire);
    expect(out?.evidence).toBeNull();
  });

  it("returns null when markdown is missing entirely", () => {
    const wire = JSON.stringify({ evidence_markdown: "## Evidence" });
    expect(extractSummarizeResult(wire)).toBeNull();
  });

  it("returns null when the wire payload is not valid JSON", () => {
    expect(extractSummarizeResult("not json")).toBeNull();
  });
});
