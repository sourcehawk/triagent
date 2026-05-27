// Section metadata is shared between the [section] route and the
// DocsView left rail. Adding a section = drop the matching markdown
// file into docs/content/<id>.md and append an entry here.

export type SectionID =
  | "overview"
  | "investigations"
  | "watches"
  | "mcp"
  | "playbooks"
  | "repos"
  | "wiki"
  | "connections"
  | "profiles";

export type Section = {
  id: SectionID;
  label: string;
  subtitle: string;
};

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
    id: "profiles",
    label: "Profiles",
    subtitle: "Forking the default to fit your platform",
  },
];

export const SECTION_IDS: SectionID[] = SECTIONS.map((s) => s.id);

export function isSectionID(s: string): s is SectionID {
  return (SECTION_IDS as string[]).includes(s);
}
