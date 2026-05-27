# Triagent

> Agentic incident investigation, driven from your browser.

Triagent is a localhost web app that pairs the [Claude](https://claude.com/claude-code) reasoning agent with read-only
Kubernetes access, an extensible [MCP](https://modelcontextprotocol.io) catalog (Prometheus, Slack, GitHub,
incident.io, your own), a guided playbook walker, and a persistent team wiki. You run `triagent start`, hand it the
symptom, and it drives a focused diagnosis you can paste into a ticket when it's done.

Every tool call stays visible, so you can audit the chain or interrupt at any point. Finished sessions can be shared
so the next operator starts from where you ended, not from the alert.

**📚 [Read the full documentation →](https://sourcehawk.github.io/triagent/)**

![Live investigation: sidebar of past sessions, the agent's diagnosis in the main pane, and the activity panel streaming every tool call on the right.](docs/images/session-screenshot.png)

## What it does

Kubernetes triage isn't a `kubectl` command — it's a multi-tab scramble across half a dozen surfaces. Triagent collapses
that scramble into one conversation against one audit trail:

- **The agent reads the procedure, doesn't memorise it.** Domain knowledge lives in playbooks loaded at runtime, not in
  a system prompt or a fine-tune. Updating what the system can diagnose is a YAML edit.
- **The tools are a typed catalog, not a shell.** Every action the agent can take is a curated MCP tool with a schema'd
  input. The agent can't go off-piste, and the catalog doubles as documentation.
- **Knowledge accumulates as data.** Each investigation can deposit a playbook (procedural) or a wiki entry (factual).
  Tomorrow's recall is a single tool call instead of a Slack archaeology dig.

With watches on the source (Slack channels, GitHub issue queries, more on the way), the launcher pre-classifies new
items and proposes investigations on its own. With auto mode on, routine ones run end-to-end before you've read the
page — and you can take over at any moment.

## What's in the box

Four surfaces, each documented in depth on the [docs site](https://sourcehawk.github.io/triagent/):

- **[Investigations](https://sourcehawk.github.io/triagent/docs/investigations/)** — the live triage view: hand the
  agent a symptom and a context (cluster, Slack thread, incident.io link, notes), watch the walker drive the
  diagnosis, ship the markdown summary.
- **[Playbooks](https://sourcehawk.github.io/triagent/docs/playbooks/)** — the YAML-defined guided walker the agent
  follows. Author them in-browser with an AI co-editor.
- **[Wiki](https://sourcehawk.github.io/triagent/docs/wiki/)** — the team's persistent knowledge base of failure
  patterns and prior art, queryable by the agent.
- **[Watches](https://sourcehawk.github.io/triagent/docs/watches/)** — polling rules that turn Slack messages,
  GitHub issues, or alerts into proposed investigations.

<table>
<tr>
<td width="50%" valign="top">

![Tool catalog](docs/images/tool-catalog.png)

**Typed tool catalog, not a shell.** Every action the agent can take is a schema'd MCP call — the same surface the agent reads and you author against.

</td>
<td width="50%" valign="top">

![Playbook editor](docs/images/playbook-editor.png)

**Playbooks as data.** YAML graphs the walker follows, authored in-browser with an AI co-editor and shipped as PRs to the playbooks repo.

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Wiki editor](docs/images/wiki-editor.png)

**Wiki that compounds.** Every finished investigation can deposit an entry; tomorrow's recall is a single tool call instead of a Slack archaeology dig.

</td>
<td width="50%" valign="top">

![Watches](docs/images/watches-screenshot.png)

**Watches close the loop.** Slack channels and GitHub queries become pre-classified signals — routine ones auto-spawn an investigation before the pager fires.

</td>
</tr>
</table>

## Quick start

### Requirements

- `claude` CLI on `$PATH`, authenticated. See [Claude Code](https://claude.com/claude-code).
- A working kubeconfig with read access to the namespace you want to triage. Triagent talks to the cluster via
  client-go — `kubectl` is not required but most operators have it.
- `tsh` if you use [Teleport](https://goteleport.com)-backed cluster discovery (optional).
- Kubernetes permissions to read pods/logs in the target namespace. Triagent does **not** create RBAC — it refuses
  to start if your existing permissions are insufficient.

### Install

**macOS / Linux:**

```sh
curl -fsSL https://sourcehawk.github.io/triagent/install.sh | sh
```

**Windows (PowerShell):**

```powershell
irm https://sourcehawk.github.io/triagent/install.ps1 | iex
```

**Homebrew (macOS):**

```sh
brew install --cask sourcehawk/tap/triagent
```

**Manual download:** grab the archive for your OS/arch from [the latest
release](https://github.com/sourcehawk/triagent/releases/latest) and put
`triagent` + `triagent-mcp` somewhere on your `$PATH`.

The install script downloads both `triagent` (the launcher) and `triagent-mcp`
(the MCP multiplexer) to `~/.local/bin` (or `%LOCALAPPDATA%\Programs\triagent`
on Windows). The launcher locates `triagent-mcp` adjacent to itself or anywhere
on `$PATH`. The Next.js frontend is embedded in the launcher, so the runtime
ships as a single executable per binary.

**Build from source** (requires Node 20+ and Go — see `.tool-versions`):

```sh
make build
```

### Run

```sh
triagent start
```

This boots a localhost HTTP server, prints its URL with a per-launch token, and opens your browser to it. Press
`Ctrl-C` to stop. It works out of the box on the embedded `default` profile — every team-specific upstream
(playbooks, wiki, sessions) starts in **local-only mode** and is opt-in via the section below.

In the browser:

1. **Pick a cluster** — directly from kubeconfig, or via Teleport.
2. **Log in** if prompted (SSO/2FA prompts go to the launcher terminal).
3. **Enter the namespace** and optional notes, Slack channel, or incident URL.
4. **Preflight runs** — namespace exists, you can list pods. If anything's missing, the launcher tells you why and
   stops.
5. **Investigate** — the agent walks the playbook, calls tools, and writes a summary you can copy or push upstream as
   a PR (once you've wired an upstream repo — see below).

### A few useful commands

```sh
triagent start                       # boot the launcher
triagent start --profile my-profile  # use a custom embedded profile by name
triagent start --profile ./my-prof   # use an on-disk profile (dir or yaml path)
triagent create-profile my-team      # fork the embedded default into ./my-team/ for editing
triagent clean                       # reset launcher caches (sessions, clones, etc.)
triagent clean --dry-run             # show what would be deleted
```

`--profile` accepts either an embedded profile name or a filesystem path; `TRIAGENT_PROFILE` is the env-var
equivalent.

### Connecting upstream repos

The launcher boots on local-only state by default. To enable **sync-from-upstream** (pull other operators'
playbooks / wiki entries / sessions) and **push-as-PR** (ship your edits back), point the profile at three
GitHub repos:

- `defaults.playbooks_repo` — the YAML playbooks the agent walks
- `defaults.wiki_repo` — the team knowledge base
- `defaults.sessions_repo` — committed investigation transcripts

Each is independent; you can wire any subset and the other surfaces stay in local-only mode.

**Option A — fork the default profile (recommended).** `create-profile` drops a copy of the embedded `default`
into the current directory so you can edit `defaults.*_repo` (and any prompt overrides) in place. Empty private
repos work — the launcher just needs somewhere to clone from and push proposals back to:

```sh
triagent create-profile my-team
# edit ./my-team/profile.yaml: set defaults.playbooks_repo / wiki_repo / sessions_repo
triagent start --profile ./my-team
```

**Option B — tiny overlay.** If you don't want a full copy of the default, write a one-file overlay anywhere on
disk that inherits from `default` and only overrides the fields you care about:

```sh
mkdir -p ~/.config/triagent/profile
cat > ~/.config/triagent/profile/profile.yaml <<'YAML'
name: my-team
base: default

defaults:
  playbooks_repo: my-org/triagent-playbooks   # GitHub OWNER/REPO
  wiki_repo:      my-org/triagent-wiki
  sessions_repo:  my-org/triagent-sessions
YAML

triagent start --profile ~/.config/triagent/profile
```

`base: default` means you only have to spell out the fields you're overriding; everything else (paths, prompts,
investigation inputs, the embedded `kinds.json`) is inherited.

**Option C — air-gapped.** If you want to pre-seed the three dirs manually (e.g. cloned via internal mirror) and
have the launcher never reach the network, set `defaults.offline: true` in your profile and pre-create the
clones at `~/.config/triagent/<profile-name>/upstream-playbooks` / `wiki` / `upstream-sessions`.

### Customising the rest

Everything else is optional and inherits from the default. The full schema — repos, paths, prompt overrides,
Prometheus coordinates, linked repos, extra MCPs, investigation form fields, k8s `kinds.json` overrides, auth
modes — is documented inline in the default profile:

- **[`internal/profile/profiles/default/profile.yaml`](internal/profile/profiles/default/profile.yaml)** —
  every field has a comment explaining what it does, its default, and when to set it. Copy the blocks you care
  about into your overlay; leave the rest unset and the merge in `applyBase` fills them from `default`.

The single highest-leverage override is `architecture.md` — the prompt that tells the agent what your platform's
CRDs, namespaces, and dependency wiring look like. See [Profiles](https://sourcehawk.github.io/triagent/profiles/)
on the docs site for the longer narrative on why and how to fork prompts.

## Contributing

PRs welcome. See [DEVELOPER_GUIDE.md](DEVELOPER_GUIDE.md) for the full contributor setup, [CLAUDE.md](CLAUDE.md) for
the durable conventions, and [open issues](https://github.com/sourcehawk/triagent/issues) for ideas worth picking up.

This project is heavily AI generated, I never touched the code myself so good luck. 😆

Quick loop:

```sh
make test    # Go unit tests
make build   # frontend bundle + both binaries

# UI dev loop (no Go rebuild for frontend changes):
go run . start                       # terminal 1
cd frontend && npm run dev           # terminal 2 — proxies /api/* to :8080
```

## License

[Apache 2.0](LICENSE)
