---
name: approving-drafts
description: Use when a `propose_wiki_draft` or `playbook_proposal_draft` result with a `proposal_id` appears in the transcript diff.
---

# Approving drafts

After you route `wiki`, `playbook`, `all`, or `both`, the investigation agent stages a draft. The tool result is JSON with a `proposal_id` like `prop-42ec5c16c183`.

- `propose_wiki_draft` result: approve with `kind: "wiki"`.
- `playbook_proposal_draft` result: approve with `kind: "playbook"`.

## Approve or send back

Approve when the draft matches the symptom and resolution narrative that you watched the agent build. Do not second-guess minor wording.

If something is missing or wrong, do not approve. Examples: an incident-id placeholder still in the body, a conclusion that contradicts the evidence. A wiki entry that merges two shapes you asked to split is another. Send the fix with `send_message` and ask for a redraft.

One session can stage several proposals (`all` stages wiki and playbook). Approve each by its own `proposal_id`. One approval does not cover the set.

Never approve a `proposal_id` that you did not see in the transcript. An invented id returns 400.

Codefix proposals have no approve step. They open as draft PRs on GitHub.

## Close the turn

`approve_proposal` is not a terminal action. It records the approval and sends nothing to the investigation agent, so no new turn happens and nothing wakes you again. A turn that ends with only `approve_proposal` calls leaves the session in the `started` phase forever.

After the approvals, end the turn with one of:

- `finish(reason)`: the usual choice. Approving the last draft completes the capture flow.
- `send_message(text)`: only when there is a real follow-up, for example the agent also asked a question that you can answer now.
- `request_takeover(reason)`: when the draft exposed a decision that a human must make.

The usual end-of-capture turn:

```
approve_proposal(kind="wiki",     proposal_id="prop-...")
approve_proposal(kind="playbook", proposal_id="prop-...")
finish(reason="Capture flow complete. Wiki and playbook approved.")
```
