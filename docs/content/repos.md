# Linked repositories

Linked GitHub repos give the agent two surfaces per repo: a cached **architecture summary** for fast orientation when an investigation touches the repo's code, and a **codefix** flow that turns an investigation finding into a draft PR on the same repo. Both ride on the same per-repo triagent-git MCP, the same cached clone, and the same `gh` auth.

## Adding a repo

Triagent reads two GitHub-touching tools off your local environment, both of which need to be authenticated once:

- **SSH** for cloning. Each linked repo is cloned over SSH (`git@github.com:owner/name.git`) into a per-machine
  cache, so make sure your SSH key is on file with GitHub. No extra token to issue — the launcher reuses the same
  key your terminal `git` already uses.
- **The [`gh` CLI](https://cli.github.com/)** for the *Push as PR* flows (codefix, playbooks, wiki). Run
  `gh auth login` once; the editor surfaces a clear error if it isn't authenticated yet.

Linked repos come from two sources:

- **Defaults** ship with the active profile via `profile.linked_repos`. The default profile ships none; a forked
  profile populates this list with the repos every operator on your team should have. Profile-supplied repos appear
  locked (🔒) in the manage-repos modal and can't be removed via the UI — edit the profile to change them.
- **Yours.** Add personal repos on top from the **linked github projects → manage** panel in the sidebar, or via
  the **+ add repository** action on the **/repos** index. These persist across launcher restarts under
  `~/.config/triagent/repos.yaml`, scoped to your machine.

The description you write when adding a repo isn't decoration — it's the lens the sub-agent uses when generating the
architecture summary. See [Your description shapes the summary](#your-description-shapes-the-summary) below.

## The architecture summary

### The problem this solves

When an investigation needs to consult a linked repo ("did `widget-broker` change recently?", "which file owns the
condition string the gateway is logging?"), the agent doesn't know the repo's shape. It rediscovers it every session,
either via expensive sub-agent calls (`analyze_change`, `correlate_with_findings`) or many cheap discovery calls
(`commit_summary`, `search_log`, `diff_summary`) that have to find the right thread first. That cost compounds across
investigations that all touch the same repo for the same reasons.

The architecture summary collapses that work. One sub-agent reads the repo end-to-end, distills its top-level
structure, external dependencies, domain language, key invariants, and common breakage patterns into ~300 lines of
markdown, and writes it to a cache the agent reads on first contact. **One expensive read, amortised across every
investigation that touches the repo.** The tab-multiplication problem the rest of Triagent solves for clusters,
this surface solves for code.

### Your description shapes the summary

When you add a linked repo via the sidebar's **manage** button, you write a short description of what it is and when
to consult it. That description does two things:

1. It's the **fallback orientation** the agent reads when no summary is cached yet. Until the sub-agent finishes its
   first run (or if generation fails), the description is what the agent has to go on.
2. It's the **lens the sub-agent uses** when generating the summary. A description like _"Kubernetes operator that
   reconciles cluster CRs and owns the zeebe-cluster condition strings"_ tells the sub-agent which parts of the repo
   matter, so the reconcile loop and condition-string sites get depth while tangential code stays shallow. A vague
   description ("the repo") gives the sub-agent nothing to focus on; it produces a generic summary that ages
   into background noise.

Spending a minute on the description when you add the repo is the highest-leverage thing you can do for every future
investigation that touches it.

### What's in a summary

Each summary covers, in order:

- **Top-level structure**: entry points, main packages, build layout. The directory map.
- **External dependencies**: services, databases, queues, APIs the repo talks to.
- **Domain language**: canonical names for entities, error types, status values that show up in logs and on the wire.
  Critical for the agent's literal-match against incident reports. Searching for "stuck-reconciliation" should hit
  the same string the code uses.
- **Key invariants**: assumptions the code relies on that aren't enforced at the type level.
- **Common breakage patterns**: areas with frequent bug fixes, recurring revert/fix cycles, derived from a `git log`
  scan.

Soft target ~300 lines, hard cap 500. The sub-agent uses canonical names from the actual code, never paraphrased, so
when an incident report mentions a specific string, the summary surfaces it.

### Summary lifecycle

- **Auto-generated when you add a repo.** The launcher kicks off generation in the background as soon as you add an
  entry through the **manage** modal. It can take up to 15 minutes for large repos. Close the window or navigate
  away, and you'll get a toast when it's done.
- **Manual refresh.** When the repo's shape shifts enough that the summary doesn't match (major refactor, big new
  subsystem, abandoned package), open the repo's page from the sidebar or the **/repos** index and click **refresh**.
  Same async flow as auto-gen.
- **Hand-edit.** Sometimes you know something the sub-agent missed (a non-obvious invariant, a tribal-knowledge
  gotcha). The repo page has an **edit** button that drops you into the markdown for direct edits. Your edit replaces
  what the sub-agent wrote until the next refresh, so save before triggering a refresh, or wait for an in-flight one
  to land.

### Status at a glance

The sidebar's linked-repos panel shows a status glyph next to each repo:

- **✓**: summary cached and ready.
- **⏳**: generation in progress.
- **⨯**: generation failed (click through for details).
- **⚠**: no summary cached yet. Either auto-gen hasn't run, hasn't finished, or was never triggered for a default
  repo. Click through to the repo page and refresh.

The same icons show on the **/repos** index page, with the generated-on date next to the ✓.

### What the agent does with it

Every investigation walks a short orientation step (`git_inspect`) that consults the cached summary first, then falls
back through a layered ordering: the description in the system prompt, then cheap deterministic git tools
(`commit_summary`, `search_log`, `diff_summary`), then expensive sub-agent tools (`analyze_change`,
`correlate_with_findings`) only when cheaper layers aren't enough. The structure forces cheap-then-expensive ordering,
and the audit trail captures what was tried at each layer.

In practice: a healthy summary means the agent answers most repo-orientation questions from the cache and reaches for
sub-agent calls only when the question genuinely requires reading code. A stale or missing summary means the agent
falls through to the expensive tools every time. The status icons are how you notice when that's happening.

## Codefix

The codefix surface closes the loop from investigation finding to a draft PR on a linked repo. After a summary lands, the SessionView's third action button (**request codefix**) drives the agent through assessing warrant, picking the affected repo(s), filing a GitHub issue, and spawning a sub-agent that authors the change in a fresh `git worktree`. The host pushes the branch, opens a draft PR labelled `triagent-proposal`, and renders a `CodefixProposalCard` in the chat with the issue link, the PR link, and a Discard button.

### What it is, what it isn't

It IS:

- A way to turn an investigation finding into a real code/config/docs/alert change proposal in minutes, while the context is still loaded.
- A scaffold that produces the **issue + the draft PR**, both labelled `triagent-proposal` so the team can spot AI-drafted artefacts at a glance.
- General, not limited to fixes. An alert rule that would have caught the symptom earlier, a docs gap, a config tightening all qualify. The button reads "request codefix" because fix is the dominant case.

It ISN'T:

- A merge-it-for-you robot. Review still happens on GitHub. The card has no Approve button; the operator merges directly, or types a chat refinement to drive the sub-agent through review-comment iteration.
- Cross-repo coordination. The agent walks one repo at a time and pauses for operator confirmation between repos so a misframed scope on the first one doesn't compound.

### When the bar is met

The agent's `pr_proposal` meta-playbook walks three criteria (all must hold) before drafting anything:

1. The investigation produced a concrete, bounded change opportunity (a root cause, an alert that would have caught this, a docs gap, etc.). Not "we should improve X someday".
2. The change lives in a linked repo (anything tracked in git).
3. The change is **scoped**: a discrete edit or addition the agent can plausibly land in one sub-agent run. The bar is "not a refactor of the world." If the proposed scope reads like "redesign the partition leader-election algorithm," the playbook terminates.

If the bar isn't met, you'll see a short "no codefix this time, here's why" reply rather than a noisy half-formed PR.

### Triggering

After at least one `summarize` call has landed, the **request codefix** button appears in the action row above the chat textarea (next to **promote to wiki** and **propose playbook**). Click it; the agent walks `pr_proposal`:

1. Warrant assessment in chat (one or two short messages).
2. A `search_issues` tool call (duplicate detection; if a strong match exists, the agent surfaces it and asks how to proceed).
3. A `create_github_issue` tool call.
4. A `draft_pr` tool call (long-running, up to 30 min; status markers narrate progress in italic grey under the in-flight tool card).
5. A `CodefixProposalCard` rendering inline.

If multiple repos are affected, the agent runs the cycle once per repo, **pausing between repos** to ask "proceed to repo B?" so you can spot a misframed scope before more PRs compound.

### Card lifecycle

The card transitions through:

- **Draft PR opened**: initial state. Discard button available.
- **PR open for review**: the operator (or a teammate) marked the PR ready on GitHub. Discard still available.
- **Merged**: landed. No buttons.
- **Closed without merging**: abandoned without merging.
- **PR state unknown**: auth lost or PR deleted. Click through to GitHub to investigate.

State updates flow live via SSE; no page refresh needed. The launcher polls every linked repo with active codefix proposals on the same cadence as the sessions-repo PR poller.

### Editing an open PR

When a reviewer leaves comments on the draft PR (manually or via Copilot's auto-review):

- Type something like _"please address the review comments on the codefix PR"_ in the chat. The agent re-walks `pr_proposal` in **edit mode**: fetches the open PR's diff + review comments via `gh pr view --json` + `gh api`, re-spawns the sub-agent with that context, and force-pushes refinements to the same branch.
- Or click the **edit codefix** button (the same button flips its label once a CodefixProposalCard exists); it sends the canned edit prompt for one-click muscle memory.

The PR number stays stable; the card refreshes automatically on the next poller tick.

### Discarding

If the draft is wrong (misframed issue, scope creep, sub-agent went sideways), click **Discard** on the card:

- The launcher closes the PR via `gh pr close` with an explanatory comment.
- Deletes the remote branch via `gh api -X DELETE refs/heads/<branch>`.
- Writes a discard ledger entry; repeat clicks return 409 and the button stays disabled.

### Tips

- **Don't fight the bar.** If the agent says no, it's usually right. Asking again with the same context will produce the same answer.
- **One narrow, named scope at a time.** When pushing the agent through edit mode, give it concrete refinement: _"narrow this to the BPMN parser, not DMN"_ lands far better than _"improve it"_.
- **Treat the issue as the contract.** The issue body is what the sub-agent reads to understand scope. If the body is fuzzy, the PR will be fuzzy. Edit the issue on GitHub before invoking edit mode if the framing was off.
- **Watch the status markers.** During a long `draft_pr` run, italic grey lines under the in-flight tool card narrate the sub-agent's phases (reading, writing test, running pytest, committing). Quiet stretches longer than a couple of minutes mean the test suite is running; let it complete before interrupting.
- **The card has no Approve button on purpose.** Review on GitHub. The card is a permalink + a discard-or-iterate affordance, not a review surface.

## See also

Profile- and user-supplied repos both get their own architecture summary cache and codefix worktree pool, and both are
reachable from the **/repos** index.

- For the broader picture of how linked repos plug into the agent's tool catalog, see [/docs/mcp](/docs/mcp/).
- For the deployment-specific profile that ships defaults via `linked_repos`, see [/docs/profiles](/docs/profiles/).
- For the SessionView controls that trigger codefix and the post-summary capture flow, see [/docs/investigations](/docs/investigations/).
