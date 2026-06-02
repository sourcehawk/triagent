// Single source of truth for the docs sections. Both the docs view (left-rail
// rendering) and the /docs/[section] route (URL validation) import from here, so
// adding a section in one place can never drift from the other. Each id matches
// a markdown filename under /public/docs/.

export type SectionID =
  | "overview"
  | "investigations"
  | "watches"
  | "mcp"
  | "playbooks"
  | "repos"
  | "wiki"
  | "connections"
  | "cloud-providers"
  | "profiles";

export type Section = { id: SectionID; label: string; subtitle: string };

export const SECTIONS: Section[] = [
  {
    id: "overview",
    label: "Overview",
    subtitle: "What Triagent is and what you can do with it",
  },
  {
    id: "investigations",
    label: "Investigate",
    subtitle: "AI-driven cluster triage",
  },
  {
    id: "watches",
    label: "Watches",
    subtitle: "Persistent eyes-on a source",
  },
  {
    id: "mcp",
    label: "MCP",
    subtitle: "Tool servers the agent uses",
  },
  {
    id: "playbooks",
    label: "Playbooks",
    subtitle: "Structured procedural knowledge",
  },
  {
    id: "repos",
    label: "Repos",
    subtitle: "Linked GitHub projects + architecture summaries",
  },
  {
    id: "wiki",
    label: "Wiki",
    subtitle: "Persistent know-how",
  },
  {
    id: "connections",
    label: "Connections",
    subtitle: "Slack and incident.io integrations",
  },
  {
    id: "cloud-providers",
    label: "Cloud providers",
    subtitle: "Read-only GCP and AWS investigation context",
  },
  {
    id: "profiles",
    label: "Profiles",
    subtitle: "Forking the default to fit your platform",
  },
];

// SECTION_IDS is the set of valid section slugs, derived from SECTIONS so the
// two never diverge. The route uses it to validate the URL section param.
export const SECTION_IDS: SectionID[] = SECTIONS.map((s) => s.id);
