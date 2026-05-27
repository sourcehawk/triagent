package sessions

// draftPromptTemplate has 4 substitution slots:
//   1. %q outPath  — repeated for the body's "produce at" instruction
//   2. %q outPath  — repeated again in the imperative final instruction
//   3. %s metadataPath
//   4. %s eventsPath
//
// The sub-agent runs with WorkingDir=<proposalsDir>, AllowedTools="Read,Glob,
// Grep,Write,Edit". It reads the metadata + events files via the Read tool,
// then writes the rendered markdown via the Write tool to the absolute path.
const draftPromptTemplate = `You are drafting a markdown post-mortem for an investigation session.

# Your job

1. Read the metadata file at %[3]s using the Read tool. It contains the session's id, namespace, context name, slack/incident URLs, and the operator's initial notes.
2. Read the events file at %[4]s using the Read tool. It is a JSON-lines transcript of the investigation: each line is one event (assistant text turns, tool calls, tool results). For very long files, read in chunks if needed (offset + limit) — you do not need every line of raw stdout, just enough to understand what was investigated, what was tried, and what was concluded.
3. Write a single markdown file with the Write tool to the absolute path %[1]q. Do not write any other files. Do not write to stdout — the launcher reads what you wrote to disk.

# File structure

The file must start with this YAML frontmatter (the launcher OVERWRITES the empty fields after you finish, so leave them empty exactly as shown — do NOT fill them in):

---
schema_version: 1
id: ""
date: ""
title: <one-line summary of the incident or investigation, plain text, no quotes>
author:
  name: ""
  email: ""
namespace: ""
context_name: ""
sources:
  bundle: session.triagent.json
---

Followed by these five required body sections, in order, with the EXACT ## headings shown (case-sensitive, no trailing punctuation):

## Summary
A 2–4 sentence summary of what was investigated and what was learned.

## Timeline
A bullet list of the major events in chronological order. Quote tool calls and operator notes as needed; redact any obvious credentials.

## What was tried
A bullet list of approaches the operator + agent tried, in order, with one sentence per item describing the outcome.

## Findings
The technical conclusions reached (root cause if known, contributing factors, ruled-out hypotheses).

## Outcome
What state the cluster ended in, what was done to remediate, what (if anything) is still open.

# Constraints

- Do not invent facts not present in the transcript.
- Do not include the raw bundle JSON inline — it ships alongside as session.triagent.json.
- Only write to %[2]q. No other files. No stdout-only output.
- After the Write succeeds, reply with one short sentence confirming the path you wrote to. The launcher reads the file from disk; your reply text is for telemetry only.
`
