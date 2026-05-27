# Profile setup wizard and in-app settings page

## Problem

The profile (`internal/profile/`) is the runtime-loaded configuration unit
that gives each operator their own auth, repos, prompts, MCPs, and
investigation inputs. Today it is a code-shaped artifact:

- Loaded once at launcher start; never mutated at runtime.
- The fallback is the embedded `default` profile, which intentionally
  ships with empty values for the things a real deployment needs to set
  (`defaults.playbooks_repo`, `defaults.wiki_repo`, `defaults.sessions_repo`,
  `defaults.prometheus.*`, `slack.channel_prefix`).
- To customise anything, the operator must fork the profile on disk and
  point `--profile` / `TRIAGENT_PROFILE` at the fork — there is no
  in-app way to set even the most basic values.

This is a poor first-run experience and a needless friction point for
returning users who just want to tweak one repo URL or toggle
`defaults.auto`. The `linked_repos` and `extra_mcps` blocks already have
dedicated UI surfaces (`/repos`, `/mcp`); the rest of the profile does
not.

## Goal

1. **First-run setup**: when no on-disk operator overlay exists, the
   launcher boots into a guided settings form. Filling it out writes the
   overlay file; subsequent launches load the overlay automatically.
2. **Settings page**: a `/settings` route, reachable from the top nav at
   any time, that edits the same on-disk overlay. Saving validates,
   writes atomically, and hot-reloads the in-memory profile for
   subsequent investigations.

Out of scope for v1 (tracked in [Follow-ups](#follow-ups)): editing
`auth.*`, `paths.*`, `prompt_files`, `kinds_file`, and
`investigation_inputs`. Those remain edit-on-disk + restart.

## Persistence model

The user-edited overlay lives at:

```
${XDG_CONFIG_HOME}/triagent/profile.yaml
```

A single file, not a directory. `profile.LoadPath` already accepts
either a directory or a file path; we point it directly at the file.
Any sibling assets the operator later wants to reference (custom
`architecture.md`, `kinds.json`) sit next to it and are declared
inline via the existing `prompt_files` / `kinds_file` blocks — those
keep relative paths resolved against the directory holding the yaml,
so the layout stays flat and predictable.

The YAML document is a normal `profile.Profile` with `base: default` (or
whatever profile name the operator forks from). The existing `applyBase`
merge in `internal/profile/embed.go` already produces the runtime
profile from base + overlay; nothing new is needed at the merge layer.

### Resolver change

`ResolveProfileRef` (in `cmd/triagent/start.go`) keeps its precedence but
gains one rung at the implicit-default level:

1. `--profile` flag value, if non-empty → use as-is.
2. `TRIAGENT_PROFILE` env, if non-empty → use as-is.
3. **(new)** If `${XDG_CONFIG_HOME}/triagent/profile.yaml` exists → use
   that path (routes through `LoadPath`, which now accepts either a
   directory or a yaml file directly).
4. Else → fall back to the embedded `default` profile.

This means launching with no flag picks up the operator's overlay
automatically once they have one, but the embedded default is still the
true first-boot fallback.

### First-run detection

A single `os.Stat` of `${XDG_CONFIG_HOME}/triagent/profile.yaml`. The
launcher does not need to know about first-run — the frontend asks
`GET /api/settings/state` on boot and renders the wizard if the response
says `firstRun: true`.

## Backend

### New handlers

All cookie-auth on `/api/...`, registered in
`internal/server/handlers.go`:

| Method + path             | Purpose                                              |
| ------------------------- | ---------------------------------------------------- |
| `GET /api/settings/state` | Cheap first-run probe used by the layout on mount.   |
| `GET /api/settings`       | Returns the editable subset of the in-memory profile. |
| `PUT /api/settings`       | Validates + writes overlay + hot-reloads.            |

### `GET /api/settings/state`

```json
{
  "firstRun": true,
  "overlayPath": "/home/aegir/.config/triagent/profile.yaml",
  "sourceKind": "embedded-default"
}
```

`overlayPath` is the path the wizard / settings page will write to.
`sourceKind` reports which input the current in-memory profile came
from, so the UI can warn the operator when their PUT would not change
what triagent loads on next boot (e.g. `--profile` is overriding the
overlay).

`sourceKind` is one of `"flag" | "env" | "user-file" | "embedded-default"`,
mirroring the resolver decision so the UI can warn "your --profile flag
overrides the on-disk overlay" if the operator hits Save while running
under a flag.

### `GET /api/settings`

Returns the editable subset as a flat JSON object plus a `meta` sibling.
The editable subset is precisely the v1 [form fields](#form-shape-v1).

```json
{
  "auth": { "kind": "kubeconfig", "teleport": { "proxy": "", "authConnector": "" } },
  "playbooks": { "entrypoint": "investigation", "closing": "capture_offer" },
  "defaults": {
    "playbooksRepo": "",
    "playbooksPath": "playbooks",
    "wikiRepo": "",
    "wikiPath": "wikis",
    "sessionsRepo": "",
    "sessionsPath": "sessions",
    "prometheus": { "service": "", "namespace": "", "port": 9090 },
    "auto": false,
    "offline": false
  },
  "slack": { "channelPrefix": "" },
  "overrides": {
    "promptFiles": { "architecture.md": "architecture.md" },
    "kindsFile": "kinds.json"
  },
  "meta": {
    "firstRun": false,
    "overlayPath": "/home/aegir/.config/triagent/profile.yaml",
    "sourceKind": "user-file",
    "restartRequiredFor": ["auth", "paths", "promptFiles", "kindsFile"]
  }
}
```

`overrides` is read-only display data (the operator's current
`prompt_files` / `kinds_file` declarations). It is returned so the
settings page can show "you have a custom `architecture.md` declared"
without having to read the YAML separately. PUT bodies do **not**
contain `overrides` — changes to those declarations require editing
the YAML and restarting.

`meta.restartRequiredFor` lists *top-level profile blocks* that are
displayed in the form but cannot be saved through the API in v1 — the
frontend renders these as read-only with a "restart required" hint, and
the PUT handler rejects bodies that try to change them. This is
distinct from blocks not exposed at all (`linked_repos`, `extra_mcps`,
`investigation_inputs`), which are managed on other pages. Returning
the list in `meta` lets us flip a v1 read-only block to editable later
without a frontend rebuild.

### `PUT /api/settings`

Body is the same shape (without `meta`). Server flow:

1. Parse the existing overlay YAML if present (so we preserve fields we
   don't expose, e.g. anything the user hand-edited or future fields).
2. Apply the request body onto that parsed shape, replacing each v1
   block as a whole. (We do not deep-merge inside a block — sending
   `defaults` replaces all of `defaults`.)
3. Build the *merged-with-base* `Profile` by calling
   `profile.Load(overlayDir)` against an in-memory candidate, then call
   `Validate()` on the result.
4. Reject with 409 + `{"error": "restart_required", "fields": [...]}` if
   the candidate's `auth.*`, `paths.*`, `prompt_files`, or `kinds_file`
   differ from the current in-memory profile (we compare these
   specifically rather than trusting client-side gating).
5. Marshal the overlay (operator's overrides plus `base:`) to YAML and
   `atomicWrite` to `${XDG_CONFIG_HOME}/triagent/profile.yaml`.
6. Re-`profile.LoadPath(overlayPath)` from disk to get a clean
   `*Profile` identical to what a fresh launch would see.
7. `Store()` it on the launcher's `atomic.Pointer[profile.Profile]`.
8. Return the new `GET /api/settings` payload.

Validation errors return 400 with the joined `Validate()` error string
verbatim. The frontend parses the `<key>: <msg>` prefixes to attribute
per-field errors where possible.

### Hot-reload mechanism

Today `server.Options.Profile *profile.Profile` is captured by value in
many places (preflight, handlers, prompt builders). V1 changes:

- `server.Options` keeps `Profile *profile.Profile` for the bootstrap
  value, plus a new `ProfileRef *atomic.Pointer[profile.Profile]` that
  the server owns and Stores into on each successful PUT.
- A thin accessor `(a *apiHandlers) profile() *profile.Profile` reads
  the pointer. All current `a.opts.Profile` reads in
  `internal/server/handlers*.go` move to `a.profile()`.
- **Preflight pins a snapshot**: `handlePreflight` reads
  `a.profile()` once at the top of the request and passes that value
  down. In-flight investigations keep their captured profile so a save
  mid-investigation does not retroactively change behavior; new
  investigations pick up the new values.
- **Restart-required fields are not swapped at runtime**. We do not
  re-init the auth `Provider`, re-`mkdir` paths, re-extract system
  playbooks, re-read prompt files, or re-clone upstreams on save. The
  PUT rejects changes that would require any of those.

### Storage path helper

A new helper in `internal/profile/paths.go`:

```go
// UserOverlayPath returns ${XDG_CONFIG_HOME}/triagent/profile.yaml.
// Single source of truth — used by ResolveProfileRef, the PUT handler,
// and the state probe.
func UserOverlayPath() (string, error) { ... }
```

## Frontend

### Route + layout gate

- New route: `frontend/app/(main)/settings/page.tsx`. Lives under the
  `(main)` route group so it inherits `TopNav` + the global
  `<StreamProvider>` and the URL is the source of truth (per CLAUDE.md
  frontend rules).
- A new client component `<SettingsGate>` is rendered inside
  `app/(main)/layout.tsx`. On mount it fetches
  `GET /api/settings/state` once, caches the result in
  `triagent.settings-state` localStorage (cache invalidated on Save), and:
  - If `firstRun: true` and `pathname !== "/settings"`: pushes
    `/settings?setup=1` via `router.replace`.
  - Otherwise: renders children unchanged.
- TopNav gets a right-aligned gear icon linking to `/settings`. When
  `firstRun: true`, the icon pulses (Tailwind `animate-pulse`) so the
  user notices it on first boot if they bypass the redirect.

### Setup vs settings rendering

Same form component (`<SettingsForm>`), two presentation modes selected
by `?setup=1`:

| Aspect       | Settings mode (default)                          | Setup mode (`?setup=1`)                                  |
| ------------ | ------------------------------------------------ | -------------------------------------------------------- |
| Header       | "Settings" h1                                    | Yellow banner: "Welcome — finish setup to investigate."  |
| Navigation   | Horizontal tabs across the top of the form       | Vertical stepper on the left                             |
| CTA          | "Save changes" (sticky bottom-right)             | "Save & start investigating"                             |
| Post-save    | Stay on `/settings`, show "Saved" toast          | `router.push("/")`                                       |
| Cancel       | Discards unsaved changes                         | No cancel — only "Save"                                  |

Other top-nav tabs stay clickable in setup mode but their pages render
a small "Finish setup first → /settings" stub. We do not hard-block
navigation; we just nudge.

### Form shape (v1)

| Section          | Fields                                                                                  | Control                       |
| ---------------- | --------------------------------------------------------------------------------------- | ----------------------------- |
| Cluster auth     | `auth.kind`, `auth.teleport.proxy`, `auth.teleport.authConnector`                       | **Read-only** display + hint  |
| Playbooks repo   | `defaults.playbooksRepo` + `defaults.playbooksPath`                                     | Paired inputs: `OWNER/REPO` and subdir |
| Wiki repo        | `defaults.wikiRepo` + `defaults.wikiPath`                                               | Paired inputs                 |
| Sessions repo    | `defaults.sessionsRepo` + `defaults.sessionsPath`                                       | Paired inputs                 |
| Prometheus       | `defaults.prometheus.service`, `defaults.prometheus.namespace`, `defaults.prometheus.port` | Text inputs (port: number) |
| Slack            | `slack.channelPrefix`                                                                   | Text input                    |
| Playbooks        | `playbooks.entrypoint`, `playbooks.closing`                                             | Text inputs (with id hint)    |
| Defaults         | `defaults.auto`, `defaults.offline`                                                     | Checkboxes                    |
| Advanced (read-only) | `overrides.promptFiles`, `overrides.kindsFile`, `paths.*`                           | Display only + "edit on disk" hint |

The three repo pairs are rendered as a single row each: a wide
`OWNER/REPO` input and a narrower subdir input (placeholder = the
matching default like `playbooks`, `wikis`, `sessions`). An empty
subdir is valid and means "repo root", matching how `defaults.*_path`
already behaves.

Each field has inline help text describing what it controls and a link
to the relevant docs page where useful.

### Validation

- Client-side shape checks: `OWNER/REPO` regex for repos, port range
  (1–65535) for prometheus, slug regex for playbook ids. These produce
  inline errors and disable the Save button.
- Server-side authoritative validation via `Profile.Validate()`. On
  400, the form parses `<key>: <msg>` lines and maps known prefixes to
  fields; unmapped lines surface in a banner.

### Cross-links

The Repos and MCP sections render as small read-only cards on the
settings page with a "Manage on the Repos page →" / "Manage on the MCP
page →" link. No editing in v1 — these surfaces already exist.

### Pure helpers

Two new files under `frontend/lib/` (Vitest-node tested):

- `lib/settings-payload.ts` — pure shape conversion between the form
  state and the API JSON body, plus a `diffPayload(prev, next)` used
  to detect "restart required" fields before submitting.
- `lib/settings-validation-errors.ts` — parser for the joined
  `Validate()` error string → `{field?: string, message: string}[]`.

## Testing

### Backend

- `handlers_settings_test.go` (new): GET `state`, GET `settings` (with
  and without overlay file), PUT happy path, PUT validation error, PUT
  restart-required (auth/paths). Uses `t.TempDir()` for the overlay
  path; pass it through `server.Options` for the test server.
- `profile_atomic_test.go` (new in `internal/server`): asserts that
  after PUT, a follow-up GET returns the new values and that two
  goroutines reading `a.profile()` are race-clean under `-race`.

### Frontend

- `lib/settings-payload.test.ts` — round-trip + diff cases.
- `lib/settings-validation-errors.test.ts` — error parsing fixtures.
- `<SettingsForm>` is verified by manual smoke; no jsdom test (per
  frontend conventions).

### Manual smoke

1. Start the launcher with no `--profile` and no overlay file present.
   The browser opens, the layout redirects to `/settings?setup=1`,
   the banner is visible.
2. Fill in repo URLs and prometheus, click Save & start investigating.
   The overlay file appears on disk. The browser lands on `/`.
3. Open a new tab to `/settings`; values are loaded. Change the
   `defaults.auto` toggle, Save, reload → still applied.
4. Restart the launcher with no flag. Resolver picks the overlay path.
   Values still applied.
5. Edit the overlay file on disk to change `auth.kind` to `teleport`.
   Restart. Settings page now shows the new auth values (read-only).

## Migration

None required. Existing deployments using `--profile <path>` or
`TRIAGENT_PROFILE` keep working unchanged — they bypass the overlay
file. Operators who currently rely on the embedded `default` get the
new behavior on next launch (the wizard offers to write their first
overlay).

## Anti-goals (explicitly not v1)

- Editing `prompt_files` / referenced prompt bodies, `paths.*`, `auth.*`,
  `investigation_inputs`, `kinds_file`, `linked_repos`, or `extra_mcps`
  in this page.
- Re-initialising the auth provider, port-forwards, or MCP children at
  runtime.
- Multiple named overlays per machine.
- Profile export/import in the UI.
- Detecting on-disk overlay edits made outside the UI and offering an
  in-app restart.

## Follow-ups

Tracked here so v1 stays focused; each is its own future spec:

1. **Auth switcher**: in-UI kubeconfig ↔ teleport with provider
   reinit. Requires draining in-flight investigations or rejecting the
   change while any are running.
2. **Paths editor**: edit `paths.*`, drive mkdir + clone-replan + MCP
   relaunch from the server.
3. **Prompts editor**: textarea-per-file under an Advanced tab, with
   diff preview against the base profile and per-file revert. Saves a
   sibling `architecture.md` (or similar) next to the overlay and
   declares it via `prompt_files: { architecture.md: architecture.md }`.
4. **Investigation inputs designer**: drag-and-drop schema editor for
   `investigation_inputs` (currently edit-on-disk only).
5. **kinds.json upload**: file picker that writes a sibling
   `kinds.json` next to the overlay, sets `kinds_file: kinds.json`,
   and reloads the k8s MCP's snapshot.
6. **Profile export/import**: download the resolved profile as a
   single YAML; import a partner's profile via paste-or-upload.
7. **Multiple named profiles**: `${XDG_CONFIG_HOME}/triagent/profiles/<name>/`
   with `--profile <name>` resolution.
8. **External-edit detection**: watch the overlay file and surface a
   "reload?" toast when it changes from outside the UI.
9. **Restart helper**: a Save action that requires restart could offer
   to spawn a graceful restart of the launcher itself.
