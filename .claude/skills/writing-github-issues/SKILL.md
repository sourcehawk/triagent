---
name: writing-github-issues
description:
  Use when about to call `gh issue create` or `gh issue edit` against
  sourcehawk/triagent, or immediately after concluding a brainstorming
  session that needs an issue to hold the work.
---

# writing-github-issues

## When to invoke

Whenever you're about to file or edit an issue on `sourcehawk/triagent`, **or** immediately after a brainstorming
session lands a decision that needs an issue to hold the work.

Three branches:

- **No issue yet**: create one (§Step 2A).
- **Issue exists but is missing context**: update it (§Step 2B).
- **Issue exists and is sufficient**: no GitHub-side action (§Step 2C).

## Core principle: user-in-the-loop for every GitHub mutation

Don't create, modify, or link an issue without an explicit confirmation **for the specific body about to land**. This
applies even if the user said "file an issue" earlier in the conversation; generic intent earlier is not standing
consent for the specific body now.

By default, assign the user to any issue created or touched (`--assignee @me`, which `gh` resolves to the authenticated
user). Surface the assignment in the same confirmation prompt; if they decline, note it and move on.

Every confirmation shows the user:

- The exact target (`sourcehawk/triagent`, or `sourcehawk/triagent#<num>` for updates).
- The full proposed body.
- Which label(s) will be attached.
- Whether you intend to assign them (default: yes).

Wait for an explicit "yes" before any `gh issue create / edit` or sub-issue-linkage call. Treat absence of objection as
a no.

GitHub doesn't render HTML comments, so leaving the template guidance in place is harmless — don't burn a step
removing it.

## Step 1: pick the template

Three templates live under `templates/`. The choice is a single judgment call — make it explicit; don't default to
the smallest shape.

```
Is the work one self-contained change that ships in one PR?
├─ No → It spans several feature-sized chunks ─────────────────→ EPIC
└─ Yes → Is it fixing unintended behaviour (regression, broken contract)?
         ├─ Yes ────────────────────────────────────────────────→ BUG
         └─ No → Is the change user-observable (new flag, UI,
                 API surface)?
                 ├─ Yes ─────────────────────────────────────────→ FEATURE  (label: feature)
                 └─ No  (plumbing, refactor, test work) ─────────→ FEATURE template, label: task
```

For brainstorm output specifically: ask whether the outcome spans multiple feature-sized chunks. If yes, file an
**epic** plus one **feature/task/bug** per chunk; link each child as a sub-issue. If no, file a single **feature**
(or bug) and break it into PR-sized phases inside the Approach section.

**Tiebreaker for feature vs task** (when work touches operator-observable state but reads mostly as plumbing): ask
whether the headline change a no-context reader would describe is user-observable. Yes → `feature`. No → `task`.
Example: a launcher-state migration that changes the on-disk layout is `task` (the headline is "refactor the layout");
the switcher UI that lets the operator pick a profile is `feature` (the headline is "you can now pick a profile").

| Template          | When                                                                                 | Label              |
| ----------------- | ------------------------------------------------------------------------------------ | ------------------ |
| `epic.md`         | Multi-chunk work; parent of feature/task/bug sub-issues                              | `epic`             |
| `feature.md`      | One self-contained capability or refactor                                            | `feature` or `task`|
| `bug.md`          | Unintended behaviour; broken contract; regression                                    | `bug`              |

## Step 2A: No issue yet (create)

1. **Draft the issue body** from the chosen template. Each section earns its keep — read the `<!-- -->` guidance in
   the template for what belongs where. The cross-template common shape:
   - **Title**: human-readable sentence a no-context reader can parse.
   - **Problem**: a few sentences opening with the elevator pitch (the what) and naming the operational reason it
     matters (the why). No solution; that belongs in the PR description (bug) or in Approach / Design overview
     (feature / epic).
   - Template-specific sections: see the relevant template file.

2. **Confirm with the user, with the body inline.** Paste the drafted body into chat under a "About to create a
   `<label>` issue in `sourcehawk/triagent` with the body below, and assign you (`@me`). Confirm?" line. Wait for
   an explicit yes. If they push back on specific wording, redraft and re-present.

3. **Create the issue** by piping the body through a heredoc:
   ```
   gh issue create --repo sourcehawk/triagent --title "<title>" --label <label> --assignee @me --body "$(cat <<'BODY_END'
   <body>
   BODY_END
   )"
   ```
   Drop `--assignee @me` if they declined assignment in step 2. If the body itself contains the line `BODY_END`,
   pick a less collision-prone sentinel (`ISSUE_BODY`, `EOF_ISSUE_42`, etc.) — the heredoc terminator must not
   appear inside the body.

4. **Capture the URL** and surface the number to the user.

5. **If this is an epic**, file in this order:
   a. **Epic first** (steps 1-4 above) so children can reference its number in their `## Context` section.
   b. **Each sub-issue next** using the feature/task/bug template (steps 1-4 above for each).
   c. **Linkage last** — once all children have numbers, link each one to the epic via the native sub-issue API
      (see §Linking sub-issues). Don't try to embed the children in the epic body manually — GitHub renders the
      progress checklist from the linkage, not from a markdown list.

### Linking sub-issues

GitHub's `gh issue create` does not (yet) expose a `--parent` flag. Use the GraphQL `addSubIssue` mutation, fetching
both issue node ids via `gh issue view --json id`.

**Confirm the linkage set before running any mutation.** Sub-issue linking is a GitHub mutation; the user-in-the-loop
rule from §Core principle applies. Once every child issue is filed (so every number is known), present the full
linkage set to the user in one prompt:

> About to link the following sub-issues to epic `sourcehawk/triagent#<epic-num>`:
> - `#<child-1>` (<one-line title>)
> - `#<child-2>` (<one-line title>)
> - ...
>
> Confirm?

Wait for an explicit yes. On push-back, drop or amend specific links before running anything.

Then run the mutation once per sub-issue:

```
PARENT_ID=$(gh issue view <epic-num> --repo sourcehawk/triagent --json id --jq .id)
CHILD_ID=$(gh issue view <child-num> --repo sourcehawk/triagent --json id --jq .id)
gh api graphql -f query="mutation { addSubIssue(input: {issueId: \"$PARENT_ID\", subIssueId: \"$CHILD_ID\"}) { subIssue { number } } }"
```

The epic's body's `## Sub-issues` section auto-renders as a checklist with progress — no manual list maintenance
needed.

## Step 2B: Issue exists but is missing context (update)

1. **Read the existing issue:**
   ```
   gh issue view <num> --repo sourcehawk/triagent --json title,body,labels,assignees
   ```
2. **Identify the gaps.** Compare the existing body against the matching template's section list. State each gap in
   one sentence. Pre-existing tickets are most often missing concrete acceptance criteria, verification steps, or
   labels; flag those first.
3. **Draft the updated body.** Preserve content from the existing issue that the user wants to keep; merge in what's
   missing. Use the template for sections that need them.
4. **Confirm with the user, surfacing both the gap list and the proposed body.** Paste the drafted body into chat
   under a "The issue at `sourcehawk/triagent#<num>` is missing: <one-line gap list>. About to update its body to
   the version below, and assign you (`@me`) if you're not already. Confirm?" line. Wait for an explicit yes. On
   push-back, redraft and re-present.

5. **Update the issue** by piping the body through a heredoc:
   ```
   gh issue edit <num> --repo sourcehawk/triagent --add-label <label> --add-assignee @me --body "$(cat <<'BODY_END'
   <body>
   BODY_END
   )"
   ```
   Drop `--add-assignee @me` if declined. Drop `--add-label` if the label is already attached.

## Step 2C: Issue exists and is sufficient (no body change)

1. **Surface the link to the user**.
2. **Ask about assignment** if the user isn't listed in `assignees`:

   > You're not currently assigned to `sourcehawk/triagent#<num>`. Want me to add you (`@me`)?

   On yes: `gh issue edit <num> --repo sourcehawk/triagent --add-assignee @me`. On no: leave it.

3. **No body changes.**

## Labels

The skill assumes these labels exist on `sourcehawk/triagent`:

| Label     | Used for                                                                  |
| --------- | ------------------------------------------------------------------------- |
| `epic`    | Parent issue grouping feature/task/bug sub-issues                         |
| `feature` | User-observable capability or surface (flag, UI, API, behaviour)          |
| `task`    | Plumbing, refactor, test work with no direct user-visible change          |
| `bug`     | Unintended behaviour; regression; broken contract                         |

`bug` already exists on the repo from GitHub's default set. If `epic`, `feature`, or `task` is missing, surface that
to the user and offer to create them with `gh label create <name> --repo sourcehawk/triagent --description "<one-line>"`
— that's a GitHub mutation, gate it on confirmation like any other.

## Implementation workflow

When working a sub-issue (or a single-feature/bug issue):

- **Commits carry the sub-issue ref** as a suffix on the subject line: `fix(mcp/strategies): handle empty walker dir (#23)`.
  The issue page surfaces the commit history automatically; an epic doesn't need its own ref because GitHub's
  parent→sub-issue linkage already threads them.
- **PR linkage depends on which branch the PR targets** (see `opening-a-pull-request`):
  - **Sub-PR into the feature branch** (multi-PR features) — body uses `Towards #<sub-issue>`. `Fixes` / `Closes`
    don't auto-trigger on non-default branches; `Towards` is explicit about keeping the issue open until the
    orchestrator closes it manually after the self-merge:
    `gh issue close <sub-issue> --repo sourcehawk/triagent --comment "Merged via #<sub-pr> into feature/<slug>"`.
  - **Integration PR into main** (multi-PR features) — body uses `Closes #<epic>` so the epic auto-closes when the
    feature lands.
  - **Single-PR feature → main** — body uses `Fixes #<feature-issue>` or `Closes #<feature-issue>`.
- **Don't bundle work across sub-issues** in one commit. One sub-issue per commit (or per logical commit chain) keeps
  the cross-link signal honest.

## Anti-patterns

- **Forcing multi-chunk work into a single feature/bug issue.** If the brainstorm decision is "build X with three
  feature-sized pieces", an epic + three children beats one bloated issue with a checklist body. Sub-issues give you
  GitHub's progress bar, independent assignees, independent close-on-merge.
- **Inventing a feature-id-style slug as the title.** Issue titles are human-readable sentences. Slugs belong on
  branches and PR titles, not in the issue heading a reviewer reads first.
- **Putting design into a feature or bug issue.** The issue is "problem + how we'll know it's done." Design belongs
  in the PR description that lands the work (or in `docs/superpowers/specs/` for spec-worthy work). The epic's
  `## Design overview` is the exception — it captures the brainstorm output, not the line-level design.
- **Acceptance criteria written as aspirations.** Each bullet has to be a verifiable condition a reviewer can answer
  "yes / no" against at done-time. "MCPs are more reliable" is not checkable; "`get_state` returns the session after
  an MCP restart" is.
- **Inferring consent from earlier intent.** "The user said 'file an issue' two turns ago" is not standing consent
  for the specific body you now want to publish. Re-confirm with the actual proposed body, every time.
- **Updating an issue silently because the diff is small.** Even a one-line addition to a public issue is a public
  action the user didn't approve. Show the diff first.
- **Proceeding on absence of objection.** "I'll go ahead unless they stop me" is not consent. Wait for an explicit
  yes; the cost of waiting is low, the cost of an unwanted public mutation is high.
- **Skipping the assignee question.** Default is to assign the user. If they decline once, note it and move on; don't
  keep asking on later edits.
- **Skipping the label.** Every issue gets exactly one of `epic`, `feature`, `task`, `bug`. The label is how the issue
  list is navigable; an unlabeled issue is invisible to filters.

## Red flags: STOP and re-confirm

These thoughts mean you're about to mutate GitHub without a fresh confirm:

| Thought                                                      | Reality                                                                                      |
| ------------------------------------------------------------ | -------------------------------------------------------------------------------------------- |
| "The user already said they want this issue"                 | Generic intent earlier ≠ consent for the specific body now. Re-confirm with the body inline. |
| "I'm just updating, the diff is small"                       | Public action on a public surface. Show the diff first.                                      |
| "They said yes a turn ago, this is the same thing"           | Bodies change between turns. Confirm what you're about to send.                              |
| "They didn't object to the assignment line, so they want it" | Absence of objection ≠ consent. Ask, then act.                                               |
| "I'll just append and they can edit later if needed"         | They shouldn't have to clean up after the agent. Confirm first.                              |
| "An epic feels heavy; I'll fold the children into one issue" | The brainstorm says it's multi-chunk. Don't compress it just because the template feels new. |

All of these mean: paste the proposed body and the assignment intent into chat, wait for an explicit yes, then act.
