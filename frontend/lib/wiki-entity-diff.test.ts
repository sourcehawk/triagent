import { describe, it, expect } from "vitest";
import {
  diffWikiEntities,
  parseWikiEntities,
  type WikiEntity,
} from "./wiki-entity-diff";

function md(parts: {
  services?: string[];
  errors?: string[];
  symptoms?: string[];
  body?: string;
}): string {
  const fm: string[] = ["---"];
  if (parts.services) fm.push(`services: [${parts.services.join(", ")}]`);
  if (parts.errors) fm.push(`errors: [${parts.errors.join(", ")}]`);
  if (parts.symptoms) fm.push(`symptoms: [${parts.symptoms.join(", ")}]`);
  fm.push("---");
  return fm.join("\n") + "\n" + (parts.body ?? "");
}

function get(map: Map<string, WikiEntity>, name: string): WikiEntity {
  const v = map.get(name);
  if (!v) throw new Error(`expected entity ${name}`);
  return v;
}

describe("parseWikiEntities", () => {
  it("returns empty map for empty markdown", () => {
    expect(parseWikiEntities("").size).toBe(0);
    expect(parseWikiEntities("no frontmatter just text").size).toBe(0);
  });

  it("classifies frontmatter entities by array", () => {
    const m = parseWikiEntities(
      md({
        services: ["tasklist", "zeebe-broker"],
        errors: ["retry-storm"],
        symptoms: ["tasklist-slow"],
      }),
    );
    expect(m.size).toBe(4);
    expect(get(m, "tasklist").type).toBe("service");
    expect(get(m, "zeebe-broker").type).toBe("service");
    expect(get(m, "retry-storm").type).toBe("error");
    expect(get(m, "tasklist-slow").type).toBe("symptom");
    expect(get(m, "tasklist").sources).toEqual(["frontmatter"]);
  });

  it("defaults body-only wikilinks to component", () => {
    const m = parseWikiEntities(
      md({ body: "see [[some-component]] for details" }),
    );
    expect(m.size).toBe(1);
    expect(get(m, "some-component").type).toBe("component");
    expect(get(m, "some-component").sources).toEqual(["body"]);
  });

  it("merges frontmatter type with body source", () => {
    const m = parseWikiEntities(
      md({
        services: ["tasklist"],
        body: "the [[tasklist]] worker pool",
      }),
    );
    expect(get(m, "tasklist").type).toBe("service");
    expect(get(m, "tasklist").sources).toEqual(["frontmatter", "body"]);
  });

  it("ignores non-wikilink bracket text", () => {
    // Uppercase / spaces / punctuation aren't wikilinks.
    const m = parseWikiEntities(
      md({ body: "[[TODO]] and [[bad name]] and [[good-name]]" }),
    );
    expect(m.size).toBe(1);
    expect(m.has("good-name")).toBe(true);
  });

  it("tolerates malformed yaml", () => {
    const broken = "---\nservices: [unclosed\n---\n[[fallback]]\n";
    const m = parseWikiEntities(broken);
    // Frontmatter parse failed — only the body wikilink survives.
    expect(m.size).toBe(1);
    expect(get(m, "fallback").type).toBe("component");
  });

  it("handles document with no frontmatter fence", () => {
    const m = parseWikiEntities("plain body with [[an-entity]]");
    expect(m.size).toBe(1);
    expect(get(m, "an-entity").type).toBe("component");
  });
});

describe("diffWikiEntities", () => {
  it("treats missing base as everything-added", () => {
    const d = diffWikiEntities(md({ services: ["tasklist"] }));
    expect(d.entities.get("tasklist")?.status).toBe("added");
  });

  it("flags identical markdown as all-unchanged", () => {
    const same = md({ services: ["tasklist"], body: "[[tasklist]]" });
    const d = diffWikiEntities(same, same);
    expect(d.entities.get("tasklist")?.status).toBe("unchanged");
  });

  it("flags entity dropped from both frontmatter and body as removed", () => {
    // Matches the scenario in the user's INC-23456 example.
    const base = md({
      errors: ["retry-storm", "job-timeout"],
      symptoms: ["job-completion-delays", "tasklist-slow"],
      body: "saw [[retry-storm]] and [[job-completion-delays]]",
    });
    const proposed = md({
      errors: ["job-timeout"],
      symptoms: ["tasklist-slow"],
      body: "saw retry-storm and job-completion-delays",
    });
    const d = diffWikiEntities(proposed, base);
    expect(d.entities.get("retry-storm")?.status).toBe("removed");
    expect(d.entities.get("job-completion-delays")?.status).toBe("removed");
    expect(d.entities.get("job-timeout")?.status).toBe("unchanged");
    expect(d.entities.get("tasklist-slow")?.status).toBe("unchanged");
  });

  it("flags new entity reference as added", () => {
    const base = md({ services: ["tasklist"] });
    const proposed = md({
      services: ["tasklist"],
      errors: ["job-timeout"],
    });
    const d = diffWikiEntities(proposed, base);
    expect(d.entities.get("job-timeout")?.status).toBe("added");
    expect(d.entities.get("tasklist")?.status).toBe("unchanged");
  });

  it("flags type reclassification as modified with previousType", () => {
    // entity moved from `services` (service) to `errors` (error)
    const base = md({ services: ["foo"] });
    const proposed = md({ errors: ["foo"] });
    const d = diffWikiEntities(proposed, base);
    const e = d.entities.get("foo");
    expect(e?.status).toBe("modified");
    expect(e?.changes).toEqual(["type"]);
    expect(e?.previousType).toBe("service");
    expect(e?.entity.type).toBe("error");
  });

  it("flags source change (frontmatter → body-only) as modified", () => {
    const base = md({ services: ["foo"], body: "[[foo]]" });
    // proposed: still mentioned via wikilink but dropped from frontmatter
    // — schema lets the body link stand alone with the "component"
    // fallback, so the type also flips. Both dimensions change.
    const proposed = md({ body: "[[foo]]" });
    const d = diffWikiEntities(proposed, base);
    const e = d.entities.get("foo");
    expect(e?.status).toBe("modified");
    expect(e?.changes).toContain("sources");
    expect(e?.changes).toContain("type");
  });
});
