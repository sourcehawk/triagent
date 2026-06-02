import { describe, it, expect } from "vitest";
import { SECTIONS, SECTION_IDS } from "@/lib/docs-sections";

describe("docs sections registry", () => {
  it("derives SECTION_IDS from SECTIONS so the route and rail cannot drift", () => {
    expect(SECTION_IDS).toEqual(SECTIONS.map((s) => s.id));
  });

  it("includes cloud-providers as a routable section", () => {
    // Regression: the route's validation list had drifted from the rail and
    // dropped this id, so clicking it fell back to overview.
    expect(SECTION_IDS).toContain("cloud-providers");
  });
});
