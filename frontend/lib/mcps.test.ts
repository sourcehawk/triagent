import { describe, it, expect } from "vitest";
import type { Investigation } from "@/lib/api";
import { activeMCPs, chipClasses } from "@/lib/mcps";

function makeInv(overrides: Partial<Investigation> = {}): Investigation {
  return {
    id: "i1",
    namespace: "",
    mcpConfigPath: "",
    sessionDir: "",
    promEnabled: false,
    createdAt: new Date().toISOString(),
    started: false,
    streaming: false,
    archived: false,
    syncState: { status: "local-only" },
    ...overrides,
  } as Investigation;
}

describe("activeMCPs cloud sources", () => {
  it("emits a cloud chip per wired cloud MCP, keyed by its wire alias", () => {
    const mcps = activeMCPs(
      makeInv({
        cloudMcps: [
          { alias: "triagent-cloud-prod-gcp", provider: "gcp" },
          { alias: "triagent-cloud-prod-aws", provider: "aws" },
        ],
      }),
    );
    const cloud = mcps.filter((m) => m.category === "cloud");
    expect(cloud.map((m) => m.alias)).toEqual([
      "triagent-cloud-prod-gcp",
      "triagent-cloud-prod-aws",
    ]);
    expect(cloud[0].description).toContain("GCP");
    expect(cloud[1].description).toContain("AWS");
  });

  it("emits no cloud chips when no cloud sources are wired", () => {
    const mcps = activeMCPs(makeInv());
    expect(mcps.some((m) => m.category === "cloud")).toBe(false);
  });

  it("gives the cloud category its own chip styling", () => {
    expect(chipClasses("cloud")).not.toBe("");
    expect(chipClasses("cloud")).not.toBe(chipClasses("docs"));
  });
});
