---
name: writing-github-issues
description:
  Use when Phase 1 needs a GitHub tracking issue for the feature. Either no issue exists yet, or one exists but is
  missing context that the brainstorming surfaced. Triggers any time the orchestrator is about to call `gh issue create`
  or `gh issue edit` against the primary repo.
---

# writing-github-issues

## When to invoke

Phase 1, from `multi-repo-brief`. The tracking issue lives in the **primary repo** and is the durable cross-repo
discoverability surface; it's what the spec and PRs all link back to.

Three branches:

- **No tracking issue yet**: create one (§2A).
- **Issue exists but is missing context** the brainstorming surfaced: update it (§2B).
- **Issue exists and is sufficient**: record the link in state; no GitHub-side action (§2C).

## Core principle: user-in-the-loop for every GitHub mutation

The orchestrator does **not** create or modify a GitHub issue without an explicit confirmation **for the specific body
about to land**. This applies even if the user said "create a tracking issue" earlier in the conversation; generic
intent earlier ≠ standing consent for the specific body now.

The user is assigned to any issue the orchestrator creates or touches, by default (`--assignee @me`, which `gh` resolves
to the authenticated user). Surface the assignment in the same confirmation prompt; if they decline, note it and move
on.

Every confirmation shows the user:

- The exact target (`<org>/<repo>`, or `<org>/<repo>#<num>` for updates).
- The full proposed body (post-template-strip: what GitHub will actually see).
- Whether you intend to assign them (default: yes).

Wait for an explicit "yes" before any `gh issue create` / `gh issue edit` call. Treat absence of objection as a no.

## Steps

### Step 1: Identify which branch

Does a tracking issue for this feature already exist?

- If the user supplied a ref in the brief, use it.
- Otherwise probe: `gh issue list --repo camunda/<primary-repo> --search "<keywords>" --limit 5`. Show candidates to the
  user; let them point at the right one or confirm there's none.

### Step 2A: No issue yet (create)

1. **Confirm the primary repo with the user.** The tracking issue belongs to that repo's issue tracker for the lifetime
   of the feature.
2. **Draft the issue body** from `templates/github-issue.md` into a staging file (e.g. `/tmp/issue.md`). The issue is
   read by people outside the feature, so each section earns its keep:
   - **Description**: three sentences max, oriented at a no-context reader.
   - **Why**: the user/business problem, no solution.
   - **Acceptance criteria**: bulleted, verifiable conditions a reviewer can answer "yes / no" against at done-time.
     Each bullet names a concrete, observable change.
   - **Out of scope** (optional): include only when there is a non-goal worth fencing off for future readers; omit the
     whole section otherwise.
   - **Repos involved**: every repo carries a one-line role in _this feature_. If you don't know a repo's role, ask the
     user before drafting; the role is part of feature articulation, not implementation detail.
   - **Tracking**: feature ID, primary repo, and placeholders for spec (Phase 2) and PRs (draft refs at Phase 3,
     reordered into merge order and flipped to ready at Phase 5).
3. **Submit and clean:**
   ```
   make submit-issue SRC=/tmp/issue.md
   ```
   Strips guidance comments, validates, writes `/tmp/issue.md.cleaned` only on success.
4. **Confirm with the user, with the body inline:**

   > About to create an issue in `camunda/<primary-repo>` with the body below, and assign you (`@me`). Confirm?
   >
   > ```
   > [paste /tmp/issue.md.cleaned]
   > ```

   Wait for an explicit yes. If they push back on specific wording, edit `/tmp/issue.md`, re-submit, re-present.

5. **Create the issue:**
   ```
   gh issue create --repo camunda/<primary-repo> --body-file /tmp/issue.md.cleaned --assignee @me
   ```
   Drop `--assignee @me` if they declined assignment in step 4.
6. **Capture the URL** and write `state.tracking_issue = "<org>/<repo>#<num>"`.

### Step 2B: Issue exists but is missing context (update)

1. **Read the existing issue:**
   ```
   gh issue view camunda/<primary-repo>#<num> --json title,body,assignees
   ```
2. **Identify the gaps.** Compare the existing body against the template sections (description, why, acceptance
   criteria, repos with per-repo roles, tracking; out-of-scope when relevant). Pre-existing tickets are most often
   missing the per-repo role line and verifiable acceptance criteria; flag those first. State each gap in one sentence.
3. **Draft the updated body** in `/tmp/issue.md`. Preserve content from the existing issue that the user wants to keep;
   merge in what's missing. Use `templates/github-issue.md` for sections that need them.
4. **Submit and clean:** `make submit-issue SRC=/tmp/issue.md`.
5. **Confirm with the user, surfacing both the gap list and the proposed body:**

   > The tracking issue at `<ref>` is missing: <one-line gap list>. About to update its body to the version below, and
   > assign you (`@me`) if you're not already. Confirm?
   >
   > ```
   > [paste /tmp/issue.md.cleaned]
   > ```

   Wait for an explicit yes. On push-back, edit and re-present.

6. **Update the issue:**
   ```
   gh issue edit camunda/<primary-repo>#<num> --body-file /tmp/issue.md.cleaned --add-assignee @me
   ```
   Drop `--add-assignee @me` if declined.

### Step 2C: Issue exists and is sufficient (record)

1. **Write `state.tracking_issue = "<org>/<repo>#<num>"`**.
2. **Ask about assignment** if the user isn't listed in `assignees`:

   > You're not currently assigned to `<ref>`. Want me to add you (`@me`)?

   On yes: `gh issue edit <ref> --add-assignee @me`. On no: leave it.

3. **No body changes.**

### Step 3: Keep the issue current as later phases land

Update the issue body (not just comments) as durable artifacts land: spec link once Phase 2 lands, draft PR refs once
Phase 3 lands, and the PR list reordered into merge order / flipped to ready as Phase 5 lands. The issue is the
canonical pointer surface; comments are not durable enough. Each subsequent body edit goes through the same
confirm-with-body-inline gate.

## Anti-patterns

- **Inventing the title.** Use the feature-id slug as the basis; humans need to match issue → branch → PR by eye.
- **Putting design into the issue.** The issue is "description, why, acceptance criteria." Design lives in the spec;
  the issue body should not grow approach paragraphs, diagrams, or code as Phase 2 progresses; it should grow links.
- **Listing repos without their role.** `camunda/foo`, `camunda/bar` tells a passing reader nothing. Each entry under
  "Repos involved" gets a one-line role in _this feature_ (e.g. "owns the secret resolver chain", "consumes the new SDK
  call"). If you don't know a repo's role, ask the user before drafting.
- **Acceptance criteria written as aspirations.** Each bullet has to be a verifiable condition a reviewer can answer
  "yes / no" against at done-time. "Console renders Elasticsearch as a top-level section" is checkable; "console is
  better organised" is not.
- **One issue per repo.** No: one tracking issue total, in the primary repo. Other repos' PRs link back to it.
- **Forgetting to update the issue when artifacts land.** The issue stays current through Phase 6.
- **Inferring consent from earlier intent.** "The user said 'create an issue' in Phase 0" is not standing consent for
  the specific body you now want to publish. Re-confirm with the actual proposed body, every time.
- **Updating an issue silently because the diff is small.** Even a one-line addition to a public issue is a public
  action the user didn't approve. Show the diff first.
- **Proceeding on absence of objection.** "I'll go ahead unless they stop me". No. Wait for an explicit yes; the cost of
  waiting is low, the cost of an unwanted public mutation is high.
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
| "I'll just append and they can edit later if needed"         | They shouldn't have to clean up after the orchestrator. Confirm first.                       |

All of these mean: paste the proposed body and the assignment intent into chat, wait for an explicit yes, then act.
