# Shared upstream repo for playbooks, wikis, and sessions

## Problem

Today an operator must run three separate GitHub repositories to host
their `triagent` content: one for playbooks, one for the wiki, one for
investigation sessions. Each is configured by `OWNER/REPO` slug in the
profile:

```yaml
defaults:
  playbooks_repo: org/triagent-playbooks
  wiki_repo:      org/triagent-wiki
  sessions_repo:  org/triagent-sessions
```

The launcher clones each into its own local dir and writes artifacts at
the repo root: playbooks under `<type>/<id>.yaml`, wiki under
`entries/<slug>.md` + `entities/<type>/<name>.md`, sessions under
`<slug>/session.md`. A team that wants one place for all three has no
clean option — they would either run three repos or fork the code.

## Goal

Let operators point all three `*_repo` defaults at the **same**
`OWNER/REPO`, with each artifact type living under a known top-level
subdirectory so the three feature trees don't collide:

```
shared-upstream-repo/
├── playbooks/
│   ├── investigation/<id>.yaml
│   └── general/<id>.yaml
├── wikis/
│   ├── entries/<slug>.md
│   └── entities/<type>/<name>.md
└── sessions/
    └── <date>-<ns>-<id>/session.md
```

Three separate repos remain a supported layout — the change is
additive, default subpaths are turned on out of the box, and `path: ""`
falls back to the legacy flat layout.

## Non-goals

- **Clone deduplication.** If all three `*_repo` slugs match, the
  launcher still produces three independent local clones of the same
  repo (one per artifact). This wastes a little disk + a few seconds of
  startup time but keeps each subsystem's working-tree state
  independent (separate branches per push-PR flow). Optimising into a
  single shared clone is a follow-up — the user asked for the layout,
  not the optimisation.
- **Migration of existing flat repos.** Operators with a pre-existing
  `org/triagent-wiki` style flat layout set the corresponding
  `*_path: ""` in their profile and keep going. We do not ship a
  bulk-move script.

## Profile schema

`Defaults` (in `internal/profile/profile.go`) gains three string fields:

```go
type Defaults struct {
    PlaybooksRepo string `yaml:"playbooks_repo"`
    PlaybooksPath string `yaml:"playbooks_path"` // NEW; "" = repo root
    WikiRepo      string `yaml:"wiki_repo"`
    WikiPath      string `yaml:"wiki_path"`      // NEW
    SessionsRepo  string `yaml:"sessions_repo"`
    SessionsPath  string `yaml:"sessions_path"`  // NEW
    // … rest unchanged
}
```

The default profile (`internal/profile/profiles/default/profile.yaml`)
sets the three subpaths so the convention applies out of the box even
when the operator only fills in the repo slug:

```yaml
defaults:
  playbooks_repo: ""
  playbooks_path: playbooks
  wiki_repo: ""
  wiki_path: wikis
  sessions_repo: ""
  sessions_path: sessions
```

Profiles that inherit `base: default` get the subpath defaults for
free. A profile can override with `playbooks_path: ""` to restore the
flat layout.

## Path layering

`profile.Paths` is unchanged. Its existing fields
(`UpstreamPlaybooksDir`, `WikiDir`, `UpstreamSessionsDir`) continue to
mean *clone root* — the path `git clone` writes into.

The launcher derives a work-dir per artifact at startup:

```go
playbooksWorkDir := filepath.Join(paths.UpstreamPlaybooksDir, prof.Defaults.PlaybooksPath)
wikiWorkDir      := filepath.Join(paths.WikiDir,              prof.Defaults.WikiPath)
sessionsWorkDir  := filepath.Join(paths.UpstreamSessionsDir,  prof.Defaults.SessionsPath)
```

When `*_path` is empty, work-dir == clone-root, preserving today's
behaviour.

## `server.Options` split

The existing fields stay; their **meaning** shifts to the work-dir.
Three new fields carry the clone root for the few places that need it:

| Existing field        | Now means         | New companion         |
| --------------------- | ----------------- | --------------------- |
| `PluginPlaybooksDir`  | playbooks work-dir | `PluginPlaybooksRoot` |
| `WikiPath`            | wiki work-dir      | `WikiRoot`            |
| `SessionsPath`        | sessions work-dir  | `SessionsRoot`        |

The vast majority of consumers (file reads, file writes, listings, the
strategies/wiki MCP env-var plumbing) work transparently on the work-dir
and need no code change.

## Push-PR refactor

The three push-PR functions today take a single `repoPath` argument
that they use both as the git cwd and as the file-write root.

After the change, this collapses on flat layouts but breaks on
subpath layouts: `git checkout -B branch origin/<base>` may leave
the cwd absent on the new branch if the subdir doesn't exist upstream
yet, and writes to `filepath.Join(repoPath, relPath)` would land at
the repo root instead of inside the subpath.

The fix is to give each PR function two paths:

- **`cloneRoot`** — the `cmd.Dir` for every git invocation. Survives
  branch switches regardless of whether the subpath exists upstream.
- **`workDir`** — `filepath.Join(cloneRoot, subpath)`. Used for file
  writes. The existing `os.MkdirAll(filepath.Dir(target), 0o755)`
  calls in each PR function already mkdir parents, so a missing
  subpath dir on a freshly switched branch self-heals.

Affected functions:

- `internal/server/repo_pr.go::pushPlaybookPR`
- `internal/server/wiki_pr.go::{pushWikiToVault, pushWikiPR, pushWikiDeletePR}`
- `internal/server/sessions_pr.go` (the sessions push flow — same
  shape).

The relative path the function returns in its result (used by the UI
to display "what got committed") is `filepath.Join(subpath, relPath)`
so the operator sees the actual in-repo path, not the work-dir-local
form.

## Preflight

After `EnsurePlaybooksClone` / `EnsureWikiClone` / `EnsureSessionsClone`
succeeds, the launcher `os.MkdirAll`s the configured subpath inside the
clone. First-write paths in the launcher already mkdir parents, but
ensuring the dir exists at preflight time keeps the diagnostic value
of the work-dir field consistent (we can `os.Stat` it and report
"vault present" instead of "vault missing — will materialise on first
write").

`EnsurePlaybooksCloneOpts` etc. stay as they are — they continue to
take the clone-root `Dir`. The subpath mkdir is a follow-up step in
`cmd/triagent/start.go`, not new logic inside preflight.

## Frontend

`PushWikiPRModal` previews the committed paths
(`entries/<slug>.md`, `entities/<type>/<name>.md`). After the change
those become `<wiki_path>/entries/<slug>.md` etc.

The backend's `pushWikiToVaultResult.Path` already returns the
vault-relative path; we change it to return the clone-root-relative
path (i.e. include the subpath). The modal renders it verbatim.

## Test impact

- `internal/profile/profile_test.go` — new fields default-parse,
  base-merge.
- `internal/preflight/` — add a small test that the subpath dir is
  mkdir'd after clone, when configured.
- `internal/server/repo_pr_test.go`, `wiki_pr_test.go`-ish (or
  whichever tests stand up fixtures), `sessions_pr_test.go`,
  `handlers_versions_test.go`, `handlers_types_test.go` — fixtures
  that initialise a fake clone must `mkdir` the configured subpath
  and write fixture YAML there (instead of at the clone root).

## Anti-patterns rejected

- **Encoding the subpath in the repo slug string** (e.g.
  `org/repo@playbooks`). Keeps the slug clean, makes the schema
  ambiguous for tests, and bakes a single-string convention into
  every consumer.
- **Renaming `PluginPlaybooksDir`/`WikiPath`/`SessionsPath` to
  `*WorkDir`.** Mass rename touches every consumer for no semantic
  win; the existing names already read as "the path to operate on".
- **A single per-artifact `Vault` struct in `server.Options`.** Two
  string fields are smaller than introducing a new abstraction every
  consumer would have to import.

## Open questions

None. Defaults are chosen, schema is additive, fallback to flat layout
is supported.
