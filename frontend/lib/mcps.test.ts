import { describe, it, expect } from "vitest";
import type { Investigation, LinkedRepo, ToolEntry } from "@/lib/api";
import { activeMCPs, chipClasses, expandRepoAliases } from "@/lib/mcps";

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

describe("expandRepoAliases", () => {
  const gitTool: ToolEntry = {
    server: "triagent-git",
    name: "latest_tags",
    description: "List tags",
    inputs: [{ name: "limit", type: "integer" }],
  };
  const k8sTool: ToolEntry = { server: "triagent-k8s", name: "get_logs", description: "Logs" };

  it("adds one copy of every git tool per linked repo alias, keeping the logical entry", () => {
    const out = expandRepoAliases(
      [k8sTool, gitTool],
      [
        { owner: "camunda", name: "camunda-operator" },
        { owner: "camunda", name: "saas-argocd-apps", alias: "argocd" },
      ],
    );
    expect(out.map((t) => `${t.server}/${t.name}`)).toEqual([
      "triagent-k8s/get_logs",
      "triagent-git/latest_tags",
      "triagent-git-camunda-operator/latest_tags",
      "triagent-git-argocd/latest_tags",
    ]);
    // The aliased copy carries the same inputs so the editor can offer args.
    expect(out[2].inputs).toEqual(gitTool.inputs);
  });

  it("dedupes repos that resolve to the same alias", () => {
    const out = expandRepoAliases(
      [gitTool],
      [
        { owner: "camunda", name: "zeebe" },
        { owner: "fork", name: "zeebe" },
      ],
    );
    expect(out.map((t) => t.server)).toEqual(["triagent-git", "triagent-git-zeebe"]);
  });

  it("returns the catalog untouched when no repos are linked", () => {
    expect(expandRepoAliases([k8sTool, gitTool], [])).toEqual([k8sTool, gitTool]);
  });
});
