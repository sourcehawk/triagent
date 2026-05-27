# Public release & distribution

**Status:** Design approved 2026-05-27.

## Context

Triagent currently ships as source only — users have to clone the repo and run `make build`, which requires both Node 20+ and Go 1.26+. That is a non-starter for public distribution: most operators who would benefit from the tool will not have a Go toolchain on the box they want to run it on, and "build from source" is the wrong first impression for a product pitched as "you run `triagent start`."

We want one-command installation on every desktop OS, with no dependency on any single package manager (the author does not use Homebrew, and we cannot assume users do either). The release pipeline must be fully automatic on tag push — manual release work does not scale and is the first thing to go stale.

## Decision

- **Cross-compile prebuilt binaries with [GoReleaser](https://goreleaser.com).** Both `triagent` and `triagent-mcp` are packaged together in each archive (per-platform `.tar.gz` / `.zip`), with checksums, and published to GitHub Releases on each tag push.
- **Primary install path is a hosted shell script** (`install.sh` for POSIX shells, `install.ps1` for PowerShell), served via the existing docs GitHub Pages deployment at `https://sourcehawk.github.io/triagent/install.{sh,ps1}`. The script detects OS/arch, downloads the matching archive, verifies checksum, installs both binaries to a user-writable location, and clears macOS quarantine.
- **Secondary install path is a Homebrew tap** at `sourcehawk/homebrew-tap`, auto-published by GoReleaser. Optional ergonomic path for the subset of users who prefer `brew install`.
- **Releases trigger on git tag push (`v*.*.*`)** via a new `.github/workflows/release.yml`. Pre-release suffixes (`-rc`, `-beta`) are honored as GitHub pre-releases and skip the `latest` pointer + Homebrew formula update — used to validate the full pipeline before the first user-facing tag.

## Platform matrix

| OS      | Architectures           |
| ------- | ----------------------- |
| Linux   | `amd64`, `arm64`        |
| macOS   | `amd64` (Intel), `arm64` (Apple Silicon) |
| Windows | `amd64`                 |

Windows on ARM, FreeBSD, and other niches are out of scope for the first release. The install script prints a clear "no prebuilt binary for `$OS/$ARCH` — see Releases page" message and exits non-zero rather than silently installing nothing.

## Components & file layout

```
.github/workflows/release.yml     NEW — on tag push: frontend build → GoReleaser → tap update
.goreleaser.yaml                  NEW — build matrix, archives, checksums, Release, formula
docs/site/public/install.sh       NEW — POSIX install script (macOS + Linux)
docs/site/public/install.ps1      NEW — PowerShell install script (Windows)
README.md                         EDIT — replace "Install" section with one-liners
```

**External resources (created once, by hand, before the first release):**

1. Empty GitHub repo `sourcehawk/homebrew-tap`.
2. Repo secret `HOMEBREW_TAP_GITHUB_TOKEN` on the `sourcehawk/triagent` repo — fine-grained PAT with `contents:write` on the tap repo only. (The workflow's default `GITHUB_TOKEN` cannot push to a different repo.)

**Why scripts live in `docs/site/public/`:** Next.js's `public/` directory is copied verbatim into the static export and served at the deployment root (the `basePath: /triagent` rewrite does not apply to `public/` assets). The existing `.github/workflows/docs.yml` already deploys the docs site to GitHub Pages on push to `main`; placing the scripts there means they ship with the next docs deploy and need no separate hosting infrastructure.

## Data flow

### Release (author side)

```
git tag v0.1.0 && git push --tags
```

CI does the rest (~5–8 min on `ubuntu-latest`):

1. Check out the tag; set up Node 20 + Go 1.26.
2. `cd frontend && npm ci && npm run build` → static export lands in `frontend/out/`.
3. Copy `frontend/out/.` into `internal/web/dist/` (matches `make frontend`'s sync step) so `//go:embed all:dist` in `internal/web` picks up the bundle.
4. Run `goreleaser release --clean`:
   - Cross-compile both binaries for each platform/arch in the matrix.
   - Bundle `triagent` + `triagent-mcp` + `LICENSE` + `README.md` into `triagent_<version>_<os>_<arch>.{tar.gz,zip}`.
   - Generate `checksums.txt` (SHA-256, one line per archive).
   - Publish GitHub Release with all archives + checksums attached; body auto-generated from commit messages since the previous tag.
   - Render `Formula/triagent.rb` from a template and push it to `sourcehawk/homebrew-tap` using `HOMEBREW_TAP_GITHUB_TOKEN`.

### Install (user side)

```
$ curl -fsSL https://sourcehawk.github.io/triagent/install.sh | sh
```

`install.sh` flow:

1. `uname -s` / `uname -m` → resolve OS/arch → archive name. Normalize `x86_64`/`amd64` and `aarch64`/`arm64`.
2. Resolve target version: honor `TRIAGENT_VERSION` env var if set; otherwise `GET https://api.github.com/repos/sourcehawk/triagent/releases/latest` → read `tag_name`.
3. Download archive + `checksums.txt` from the resolved release.
4. Verify SHA-256 (`sha256sum` on Linux, `shasum -a 256` on macOS — script handles both).
5. Extract `triagent` and `triagent-mcp` to `INSTALL_DIR` (default `~/.local/bin`; overridable via `TRIAGENT_INSTALL_DIR`).
6. On macOS: `xattr -d com.apple.quarantine` on both binaries to bypass Gatekeeper on first launch (binaries are unsigned — see [Non-goals](#non-goals--anti-patterns)).
7. `chmod +x` both.
8. Print "installed to `<dir>`. Run `triagent start` to begin." If `INSTALL_DIR` is not on `$PATH`, print a shell-specific `export PATH=...` hint (detect via `$SHELL`).

`install.ps1` flow (Windows):

1. Detect architecture via `[Environment]::Is64BitOperatingSystem` / `$env:PROCESSOR_ARCHITECTURE`.
2. Resolve version the same way (use `Invoke-RestMethod`).
3. Download `.zip` and `checksums.txt` to `$env:TEMP`; verify SHA-256 with `Get-FileHash`.
4. Extract to `INSTALL_DIR` (default `$env:LOCALAPPDATA\Programs\triagent`; overridable via `$env:TRIAGENT_INSTALL_DIR`).
5. If `INSTALL_DIR` is not in the user PATH, append it via `[Environment]::SetEnvironmentVariable('Path', $newPath, 'User')`; print "open a new terminal."
6. Print "installed to `<dir>`. Run `triagent start` to begin."

### Homebrew install (subset of users)

```
$ brew install sourcehawk/tap/triagent
```

Standard formula install — pulls the platform-matching archive from GitHub Releases, drops both binaries on `$PATH` via the brew prefix. No quarantine workaround required (Homebrew handles it).

## Error handling & edge cases

**`install.sh`:**

- Uses `set -eu` and `set -o pipefail`; `trap` on `EXIT` cleans up the temp dir.
- **Unsupported OS/arch:** print "no prebuilt binary for `$OS/$ARCH` — see https://github.com/sourcehawk/triagent/releases for manual download," exit 1.
- **Missing `curl` and `wget`:** detect both; if neither, print "install curl or wget," exit 1.
- **GitHub API rate limit** (anonymous, 60/hr per source IP): if `/releases/latest` returns 403, print the rate-limit message and suggest setting `TRIAGENT_VERSION=v0.x.y` to skip the API call.
- **Checksum mismatch:** print "checksum mismatch — refusing to install," leave nothing on disk, exit 1.
- **`INSTALL_DIR` not writable:** try `mkdir -p` first; if still not writable, print "cannot write to `<dir>` — set `TRIAGENT_INSTALL_DIR` to a writable path or re-run with sudo," exit 1. Never auto-escalate to sudo.
- **`INSTALL_DIR` not on `$PATH`:** not an error — print the shell-tailored hint.
- **Pre-existing install:** overwrite without prompting; the script is idempotent (re-running upgrades in place).

**`install.ps1`:** same shape. Uses `Invoke-WebRequest` / `Invoke-RestMethod` (built into PS 5.1+). Sets `[Net.ServicePointManager]::SecurityProtocol = 'Tls12'` defensively in case of older PS hosts.

**Release workflow:**

- **Build fails (frontend or Go):** workflow fails fast; the GitHub Release is *not* created. GoReleaser is transactional — `--clean` removes `dist/` and the Release isn't published until all archives + checksums + formula succeed. Fix-forward by deleting the tag (`git tag -d v0.1.0 && git push --delete origin v0.1.0`) and retagging.
- **Homebrew tap push fails** (bad token, repo missing): GoReleaser fails the job, but by then the GitHub Release is already up. Acceptable — the install script still works; the tap update can be re-attempted by re-running the workflow against the same tag.
- **No code signing → no signing failures.** Part of the rationale for skipping signing on day one.

## Testing & rollout

**Install scripts** (manual smoke; no automated test framework — the scripts are ~80 lines each and the cost/benefit does not favor a bash test harness):

- Local development: scripts accept `TRIAGENT_BASE_URL` to override the GitHub download root, so they can be pointed at a local HTTP server serving fake archives + checksums for end-to-end testing without publishing anything.
- Linux: `docker run --rm -v $PWD:/w -w /w ubuntu:24.04 bash -c 'apt-get update && apt-get install -y curl ca-certificates && bash install.sh'` and same for `debian:12`. Verifies the script works without any build tooling on a fresh OS.
- macOS: run on Apple Silicon — verify `xattr` removal lets the binary launch without Gatekeeper prompt.
- Windows: smoke in a Windows 11 VM or via a `windows-latest` GitHub Actions runner — confirm PATH update sticks across a new shell.

**Release workflow** (validate before the first user-facing tag):

1. Push `v0.0.0-rc1`. GoReleaser treats `-rc`/`-beta` suffixes as pre-releases by default — they do not move `latest` on GitHub or update the Homebrew formula.
2. Verify: 5 archives + checksums attached to the Release; `Formula/triagent.rb` lands in `sourcehawk/homebrew-tap`; install script can install the rc tag via `TRIAGENT_VERSION=v0.0.0-rc1`.
3. Only after the rc is green, push `v0.1.0`.

**Verification before claiming the rollout done:**

- After the green `v0.1.0` workflow, run each install one-liner (sh / ps1 / brew) on the matching OS and confirm `triagent --version` works and `triagent start` boots. Verification via real install, not just CI green.

**Rollout sequence:**

1. Land `.goreleaser.yaml`, `.github/workflows/release.yml`, both install scripts, and the README update in one PR. No user impact yet (no tag pushed).
2. Create the empty `sourcehawk/homebrew-tap` repo and add the `HOMEBREW_TAP_GITHUB_TOKEN` secret.
3. Push `v0.0.0-rc1`; validate end-to-end per above.
4. Push `v0.1.0`.

## Non-goals & anti-patterns

- **No macOS code signing or notarization** on day one. Requires an Apple Developer account ($99/yr) and signing cert in CI secrets. Skipping it leaves a one-time Gatekeeper prompt that the install script's `xattr` step neutralizes; cost/benefit does not favor signing until install volume justifies it.
- **No Scoop manifest, no winget submission, no `apt`/`deb` repo, no Docker image** in the initial release. All can be layered on later without disturbing the install-script path. The principle: ship the floor (one-liner per OS), add ergonomic surfaces opportunistically.
- **No `go install` as a supported path.** It would require users to have a Go toolchain — the exact friction we are trying to remove. The build-from-source instructions remain in the contributor docs.
- **No release on push to `main`.** Tag-driven only. Avoids accidental releases on routine merges and keeps versioning deliberate.
- **No hand-rolled CI cross-compilation.** GoReleaser is the standard and saves dozens of lines of brittle shell.

## Future extensions (deliberately deferred)

- Homebrew formula → official Homebrew core (after some adoption; requires meeting Homebrew's notability bar).
- Scoop manifest (Windows users who prefer it).
- winget submission (Windows Package Manager).
- Signed/notarized macOS binaries (eliminates the `xattr` workaround).
- Docker image (`ghcr.io/sourcehawk/triagent`) for users who would rather containerize the launcher.
- Linux package repos (`apt`, `dnf`, AUR) — these compound maintenance, only worth it once user demand surfaces.
- Auto-update support in the launcher itself (`triagent self-update`).
