# Connections

Triagent optionally integrates with Slack and incident.io to give the agent access to conversation history and incident
timelines during an investigation. Both are opt-in: the core investigation flow (Kubernetes triage, playbooks, wiki)
works without them.

## How connections work

Tokens are stored locally in `~/.config/triagent/credentials.json` and validated against the upstream service before
saving. They are never sent to any external service other than the upstream API they authenticate.

The **connections** strip at the bottom of the sidebar shows live status for each integration. Click it to open the
manage-connections modal where you can link, replace, or disconnect tokens.

## Slack

When a Slack token is linked, the launcher registers a `triagent-slack` MCP server for each investigation session.
The agent uses it to:

- **Read channel history**: overview, message search, thread summarisation.
- **Resolve channel names**: convert a `#channel-name` to the `C…` channel ID the API needs.

### Getting a token

1. Go to [api.slack.com/apps](https://api.slack.com/apps) and create an app (or ask your Slack admin if one already
   exists for your team's Triagent installation).
2. Under **OAuth & Permissions**, add the following user-token scopes:
   - `channels:history` — read public channel messages
   - `channels:read` — list / resolve public channels
   - `users:read` — resolve user IDs to display names in the rendered history
   - `groups:history` (only if you need private channels) — read private channel messages
   - `groups:read` (only if you need private channels) — list / resolve private channels
3. Click **Install to workspace** and copy the `xoxp-` user OAuth token that appears.
4. Paste it in the Slack card inside the connections modal and click **save**.

The token is validated with `auth.test` before being stored. If validation fails, the token is not saved.

### How the agent uses Slack

The session's pinned Slack channel (when the operator picked one in the new-investigation form) is surfaced in the
system prompt as the suggested default. The agent can also investigate other channels the token can access by passing
a different `channel_id` to channel-aware tool calls.

## incident.io

When an incident.io API key is linked, the launcher registers a `triagent-incidentio` MCP server. The agent uses it
to pull the incident record, timeline, and postmortem for the linked incident.

### Getting an API key

1. Go to **Settings → API keys** in your incident.io dashboard
   ([app.incident.io/settings/api-keys](https://app.incident.io/settings/api-keys)).
2. Create a new key with read access to **incidents** and **post-mortems**.
3. Paste the key in the incident.io card inside the connections modal and click **save**.

The key is validated before being stored.

### How the agent uses incident.io

When the operator pastes an incident URL in the new-investigation form, the agent resolves the numeric incident ID
from the URL and passes it as `incident_id` to every incident.io tool call. The agent can also look up other
incidents by passing a different `incident_id`.

## Cloud (read-only)

Cloud connections (GCP and AWS) appear in the same panel, but read-only. They are configured in the deployment profile under the `cloud:` block, not entered here, so the panel shows a pill per source with no link or replace affordance.

Each pill shows the pinned `assumed_identity` and a validity state. Validity comes from an identity probe run on panel load: GCP checks that the active account equals the impersonated service account, AWS checks that the resolved caller is the pinned assume-role identity. A source that fails the probe shows unavailable with a re-auth hint, and re-authentication is your own cloud login (`gcloud auth login`, `aws sso login`), never a token entered in Triagent.

See [Cloud providers](/docs/cloud-providers) for the service-account and assume-role setup, the `cloud:` profile block, and the read-only command surface.

## Removing a connection

Click **disconnect** in the relevant card inside the connections modal. The token is removed from
`~/.config/triagent/credentials.json` immediately. Future sessions will not register the corresponding MCP server
until a new token is linked.
