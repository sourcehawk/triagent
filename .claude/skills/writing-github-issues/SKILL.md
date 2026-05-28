---
name: writing-github-issues
description:
  Issue description shape for triagent. Use any time you're about to
  call `gh issue create` or `gh issue edit` against sourcehawk/triagent.
---

# writing-github-issues

## When to invoke

Whenever you're about to file a tracking issue, bug report, or feature request against `sourcehawk/triagent` — either
because no issue exists yet, or one exists but is missing context that's now in hand.

Three branches:

- **No issue yet**: create one (§Step 2A).
- **Issue exists but is missing context**: update it (§Step 2B).
- **Issue exists and is sufficient**: no GitHub-side action (§Step 2C).

## Core principle: user-in-the-loop for every GitHub mutation

Don't create or modify a GitHub issue without an explicit confirmation **for the specific body about to land**. This
applies even if the user said "file an issue" earlier in the conversation; generic intent earlier is not standing
consent for the specific body now.

By default, assign the user to any issue created or touched (`--assignee @me`, which `gh` resolves to the authenticated
user). Surface the assignment in the same confirmation prompt; if they decline, note it and move on.

Every confirmation shows the user:

- The exact target (`sourcehawk/triagent`, or `sourcehawk/triagent#<num>` for updates).
- The full proposed body.
- Whether you intend to assign them (default: yes).

Wait for an explicit "yes" before any `gh issue create` / `gh issue edit` call. Treat absence of objection as a no.

GitHub doesn't render HTML comments, so leaving the template guidance in place is harmless — don't burn a step
removing it.

## Steps

### Step 1: Identify which branch

Does an issue for this already exist?

- If the user supplied a ref, use it.
- Otherwise probe: `gh issue list --repo sourcehawk/triagent --search "<keywords>" --limit 5`. Show candidates to the
  user; let them point at the right one or confirm there's none.

### Step 2A: No issue yet (create)

1. **Draft the issue body** from `templates/github-issue.md` into a staging file (e.g. `/tmp/issue.md`). The issue is
   read by people outside the immediate work, so each section earns its keep:
   - **Title**: a human-readable sentence a no-context reader can parse.
   - **Description**: a few sentences oriented at a no-context reader, opening with the elevator pitch (the what)
     and then stating the problem or operational reason it matters (the why). No solution; the fix belongs in the
     PR description.
   - **Acceptance criteria**: bulleted, verifiable conditions a reviewer can answer "yes / no" against at done-time.
     Each bullet names a concrete, observable change.
   - **Out of scope** (optional): include only when there is a non-goal worth fencing off for future readers; omit the
     whole section otherwise.

2. **Confirm with the user, with the body inline:**

   > About to create an issue in `sourcehawk/triagent` with the body below, and assign you (`@me`). Confirm?
   >
   > ```
   > [paste /tmp/issue.md]
   > ```

   Wait for an explicit yes. If they push back on specific wording, edit `/tmp/issue.md`, re-present.

3. **Create the issue:**
   ```
   gh issue create --repo sourcehawk/triagent --body-file /tmp/issue.md --assignee @me
   ```
   Drop `--assignee @me` if they declined assignment in step 2.

4. **Capture the URL** and surface it to the user.

### Step 2B: Issue exists but is missing context (update)

1. **Read the existing issue:**
   ```
   gh issue view sourcehawk/triagent#<num> --json title,body,assignees
   ```
2. **Identify the gaps.** Compare the existing body against the template sections (description, acceptance
   criteria; out-of-scope when relevant). State each gap in one sentence.
3. **Draft the updated body** in `/tmp/issue.md`. Preserve content from the existing issue that the user wants to keep;
   merge in what's missing. Use `templates/github-issue.md` for sections that need them.
4. **Confirm with the user, surfacing both the gap list and the proposed body:**

   > The issue at `sourcehawk/triagent#<num>` is missing: <one-line gap list>. About to update its body to the version
   > below, and assign you (`@me`) if you're not already. Confirm?
   >
   > ```
   > [paste /tmp/issue.md]
   > ```

   Wait for an explicit yes. On push-back, edit and re-present.

5. **Update the issue:**
   ```
   gh issue edit sourcehawk/triagent#<num> --body-file /tmp/issue.md --add-assignee @me
   ```
   Drop `--add-assignee @me` if declined.

### Step 2C: Issue exists and is sufficient (no body change)

1. **Surface the link to the user**.
2. **Ask about assignment** if the user isn't listed in `assignees`:

   > You're not currently assigned to `sourcehawk/triagent#<num>`. Want me to add you (`@me`)?

   On yes: `gh issue edit sourcehawk/triagent#<num> --add-assignee @me`. On no: leave it.

3. **No body changes.**

## Anti-patterns

- **Inventing a feature-id-style slug as the title.** Issue titles are human-readable sentences. Slugs belong on
  branches and PR titles, not in the issue heading a reviewer reads first.
- **Putting design into the issue.** The issue is "description, acceptance criteria." Design belongs in the PR
  description that lands the work; the issue body should not grow approach paragraphs, diagrams, or code over time —
  it should grow links.
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

## Red flags: STOP and re-confirm

These thoughts mean you're about to mutate GitHub without a fresh confirm:

| Thought                                                      | Reality                                                                                      |
| ------------------------------------------------------------ | -------------------------------------------------------------------------------------------- |
| "The user already said they want this issue"                 | Generic intent earlier ≠ consent for the specific body now. Re-confirm with the body inline. |
| "I'm just updating, the diff is small"                       | Public action on a public surface. Show the diff first.                                      |
| "They said yes a turn ago, this is the same thing"           | Bodies change between turns. Confirm what you're about to send.                              |
| "They didn't object to the assignment line, so they want it" | Absence of objection ≠ consent. Ask, then act.                                               |
| "I'll just append and they can edit later if needed"         | They shouldn't have to clean up after the agent. Confirm first.                              |

All of these mean: paste the proposed body and the assignment intent into chat, wait for an explicit yes, then act.
