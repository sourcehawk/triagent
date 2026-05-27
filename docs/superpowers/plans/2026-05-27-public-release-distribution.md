# Public Release & Distribution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship `triagent` + `triagent-mcp` as prebuilt binaries installable via a one-line hosted script on macOS / Linux / Windows, plus a Homebrew tap, with the release pipeline fully automated on git tag push.

**Architecture:** GoReleaser cross-compiles both binaries for 5 platform/arch combos on tag push, packages them as `.tar.gz` / `.zip` with checksums, publishes a GitHub Release, and pushes a Homebrew formula to a sibling `sourcehawk/homebrew-tap` repo. Two shell install scripts (`install.sh` / `install.ps1`) live in `docs/site/public/` and ship via the existing GitHub Pages docs deploy at `sourcehawk.github.io/triagent/install.{sh,ps1}`.

**Tech Stack:** GoReleaser v2, GitHub Actions, POSIX sh, PowerShell 5.1+. Uses existing repo conventions (`actions/checkout@v6`, `actions/setup-go@v6`, `actions/setup-node@v4` with `node-version-file: '.tool-versions'`, etc.).

**Reference spec:** `docs/superpowers/specs/2026-05-27-public-release-distribution-design.md`

---

## File map

| Path | Action | Responsibility |
| --- | --- | --- |
| `cmd/triagent/main.go` | Modify (1 line) | Change `const version = "0.1.0"` → `var version = "dev"` so ldflags can inject it. |
| `cmd/triagent-mcp/main.go` | Modify (1 line) | Same — version var injectable via ldflags. |
| `.goreleaser.yaml` | Create | Build matrix, archives, checksums, GitHub Release config, Homebrew formula. |
| `.github/workflows/release.yml` | Create | Tag-triggered release workflow: frontend build → GoReleaser. |
| `docs/site/public/install.sh` | Create | POSIX install script (macOS + Linux). |
| `docs/site/public/install.ps1` | Create | PowerShell install script (Windows). |
| `README.md` | Modify | Replace lines 94–105 (the "Install" subsection) with the one-liners. |

**External (one-time, by hand, in Task 7 — not in any commit):**
- New empty GitHub repo `sourcehawk/homebrew-tap`.
- Repo secret `HOMEBREW_TAP_GITHUB_TOKEN` on `sourcehawk/triagent` — fine-grained PAT with `contents:write` scoped to the tap repo only.

---

## Task 1: Make version injectable via ldflags

Convert the hardcoded `const version` in both binaries to a `var` so GoReleaser's `-X main.version=…` ldflag can override it at release time. Defaults to `"dev"` for local builds.

**Files:**
- Modify: `cmd/triagent/main.go:10`
- Modify: `cmd/triagent-mcp/main.go:10`

- [ ] **Step 1: Change `const` → `var` in `cmd/triagent/main.go`**

In `cmd/triagent/main.go`, replace line 10:

```go
const version = "0.1.0"
```

with:

```go
// version is overridden via -ldflags "-X main.version=..." in release builds.
// Local/dev builds report "dev".
var version = "dev"
```

- [ ] **Step 2: Same change in `cmd/triagent-mcp/main.go`**

In `cmd/triagent-mcp/main.go`, replace line 10:

```go
const version = "0.1.0"
```

with:

```go
// version is overridden via -ldflags "-X main.version=..." in release builds.
// Local/dev builds report "dev".
var version = "dev"
```

- [ ] **Step 3: Verify default behavior**

Run:
```sh
go build -o /tmp/triagent ./cmd/triagent && /tmp/triagent --version
```
Expected: `triagent version dev`

```sh
go build -o /tmp/triagent-mcp ./cmd/triagent-mcp && /tmp/triagent-mcp --version
```
Expected: `triagent-mcp version dev`

- [ ] **Step 4: Verify ldflags injection works**

Run:
```sh
go build -ldflags "-X main.version=1.2.3" -o /tmp/triagent ./cmd/triagent && /tmp/triagent --version
```
Expected: `triagent version 1.2.3`

```sh
go build -ldflags "-X main.version=1.2.3" -o /tmp/triagent-mcp ./cmd/triagent-mcp && /tmp/triagent-mcp --version
```
Expected: `triagent-mcp version 1.2.3`

- [ ] **Step 5: Run the existing Go test suite**

Run: `go test -race -count=1 ./cmd/...`
Expected: PASS (no version-related tests exist; this is a sanity check).

- [ ] **Step 6: Commit**

```sh
git add cmd/triagent/main.go cmd/triagent-mcp/main.go
git commit -m "build(version): make version injectable via -ldflags

Convert const version to var so GoReleaser can stamp the real tag
into release builds via -X main.version=. Local builds report 'dev'."
```

---

## Task 2: Create `.goreleaser.yaml`

Defines the GoReleaser v2 build matrix, archives, checksums, Homebrew formula, and GitHub Release configuration. This file is the single source of truth for what gets built and published per tag.

**Files:**
- Create: `.goreleaser.yaml`

- [ ] **Step 1: Create `.goreleaser.yaml`**

Write the following file:

```yaml
# GoReleaser v2 config. Triggered by .github/workflows/release.yml on tag
# push. See docs/superpowers/specs/2026-05-27-public-release-distribution-design.md
# for the design rationale.
version: 2

project_name: triagent

before:
  hooks:
    - go mod tidy

builds:
  - id: triagent
    main: ./cmd/triagent
    binary: triagent
    env:
      - CGO_ENABLED=0
    goos: [linux, darwin, windows]
    goarch: [amd64, arm64]
    ignore:
      # No Windows on ARM in the first release (see spec § Platform matrix).
      - goos: windows
        goarch: arm64
    ldflags:
      - -s -w -X main.version={{ .Version }}

  - id: triagent-mcp
    main: ./cmd/triagent-mcp
    binary: triagent-mcp
    env:
      - CGO_ENABLED=0
    goos: [linux, darwin, windows]
    goarch: [amd64, arm64]
    ignore:
      - goos: windows
        goarch: arm64
    ldflags:
      - -s -w -X main.version={{ .Version }}

archives:
  - id: default
    ids: [triagent, triagent-mcp]
    name_template: "triagent_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
    # Default to tar.gz everywhere except Windows, which gets zip.
    formats: [tar.gz]
    format_overrides:
      - goos: windows
        formats: [zip]
    files:
      - LICENSE
      - README.md

checksum:
  name_template: "checksums.txt"
  algorithm: sha256

snapshot:
  # Used by `goreleaser build --snapshot` for local validation.
  version_template: "{{ incpatch .Version }}-next"

changelog:
  sort: asc
  use: github
  filters:
    exclude:
      - "^docs:"
      - "^test:"
      - "^chore:"
      - "^style:"

brews:
  - name: triagent
    repository:
      owner: sourcehawk
      name: homebrew-tap
      # Fine-grained PAT with contents:write on the tap repo. The default
      # GITHUB_TOKEN cannot push to a different repo.
      token: "{{ .Env.HOMEBREW_TAP_GITHUB_TOKEN }}"
    homepage: https://github.com/sourcehawk/triagent
    description: Agentic incident investigation driven from your browser.
    license: Apache-2.0
    install: |
      bin.install "triagent"
      bin.install "triagent-mcp"
    test: |
      system "#{bin}/triagent", "--version"

release:
  github:
    owner: sourcehawk
    name: triagent
  # Tags like v0.0.0-rc1 / v0.1.0-beta are auto-marked as pre-releases and
  # skip updating the "latest" pointer + Homebrew formula.
  prerelease: auto
  draft: false
```

- [ ] **Step 2: Install GoReleaser locally**

Install via your OS package manager or:
```sh
go install github.com/goreleaser/goreleaser/v2@latest
```
Verify: `goreleaser --version`

- [ ] **Step 3: Validate the config schema**

Run: `goreleaser check`
Expected: `1 configuration file(s) validated` (no errors).

If `check` fails with schema errors (GoReleaser v2 occasionally renames fields), fix the specific field per the error message and re-run until it passes.

- [ ] **Step 4: Local snapshot build (single target, fast)**

Pre-populate the frontend so `//go:embed all:dist` doesn't fail:
```sh
make frontend
```

Then snapshot-build for the host platform only:
```sh
goreleaser build --snapshot --clean --single-target
```
Expected: builds succeed; `dist/` contains `triagent_…/triagent` and `triagent-mcp_…/triagent-mcp`.

- [ ] **Step 5: Verify both binaries run and report a version**

```sh
./dist/triagent_<os>_<arch>*/triagent --version
./dist/triagent-mcp_<os>_<arch>*/triagent-mcp --version
```
Expected: both print something like `triagent version 0.0.1-next-<sha>` (the snapshot template). The exact string proves ldflags injection works through GoReleaser.

- [ ] **Step 6: Clean up snapshot artifacts**

```sh
rm -rf dist/
```

- [ ] **Step 7: Commit**

```sh
git add .goreleaser.yaml
git commit -m "build(release): add GoReleaser config

Cross-compile both binaries for linux/darwin {amd64,arm64} + windows/amd64,
bundle as per-platform archives with LICENSE + README, emit SHA-256
checksums, publish a GitHub Release, and push a Homebrew formula to the
sourcehawk/homebrew-tap repo. Pre-release tags (-rc, -beta) skip the
'latest' update and tap formula bump."
```

---

## Task 3: Create the release workflow

GitHub Actions workflow that triggers on `v*.*.*` tag push, builds the frontend, runs GoReleaser, and publishes everything. Mirrors the style of the existing `.github/workflows/ci.yml` and `docs.yml`.

**Files:**
- Create: `.github/workflows/release.yml`

- [ ] **Step 1: Create `.github/workflows/release.yml`**

```yaml
name: Release

# Triggered by pushing a semver tag like v0.1.0 (or v0.0.0-rc1 for a
# pre-release dry-run). See docs/superpowers/specs/2026-05-27-public-release-distribution-design.md
# for the rollout sequence.
on:
  push:
    tags:
      - 'v*.*.*'

permissions:
  contents: write   # required to publish the GitHub Release

jobs:
  goreleaser:
    name: Release
    runs-on: ubuntu-latest
    timeout-minutes: 30
    steps:
      - uses: actions/checkout@v6
        with:
          # GoReleaser needs the full commit history to generate the
          # changelog from messages since the previous tag.
          fetch-depth: 0

      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
          cache-dependency-path: go.sum

      - name: Setup Node.js
        uses: actions/setup-node@v4
        with:
          node-version-file: '.tool-versions'
          cache: 'npm'
          cache-dependency-path: frontend/package-lock.json

      - name: Build frontend
        # Populates internal/web/dist/ so //go:embed picks it up at
        # Go-compile time. Mirrors what `make frontend` does locally.
        run: make frontend

      - name: Run GoReleaser
        uses: goreleaser/goreleaser-action@v6
        with:
          distribution: goreleaser
          version: '~> v2'
          args: release --clean
        env:
          # Used by the github release publisher.
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          # Required for pushing the Homebrew formula to sourcehawk/homebrew-tap.
          # Created by hand once (see plan Task 7).
          HOMEBREW_TAP_GITHUB_TOKEN: ${{ secrets.HOMEBREW_TAP_GITHUB_TOKEN }}
```

- [ ] **Step 2: Lint the workflow YAML**

If `actionlint` is available locally:
```sh
actionlint .github/workflows/release.yml
```
Expected: no errors. If `actionlint` is not installed, parse-check with:
```sh
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/release.yml'))" && echo OK
```
Expected: `OK`.

- [ ] **Step 3: Commit**

```sh
git add .github/workflows/release.yml
git commit -m "ci(release): add tag-triggered release workflow

On v*.*.* tag push: build the frontend, run GoReleaser to cross-compile
both binaries, publish a GitHub Release with archives + checksums, and
push the Homebrew formula to sourcehawk/homebrew-tap."
```

---

## Task 4: Write `install.sh` (POSIX, macOS + Linux)

Hosted on the docs Pages site at `https://sourcehawk.github.io/triagent/install.sh`. Detects OS/arch, downloads + verifies the matching archive, installs both binaries.

**Files:**
- Create: `docs/site/public/install.sh`

- [ ] **Step 1: Create `docs/site/public/install.sh`**

```sh
#!/bin/sh
# triagent install script (macOS / Linux)
# Usage: curl -fsSL https://sourcehawk.github.io/triagent/install.sh | sh
#
# Env vars:
#   TRIAGENT_VERSION       (default: latest)        e.g. v0.1.0
#   TRIAGENT_INSTALL_DIR   (default: ~/.local/bin)
#   TRIAGENT_BASE_URL      (default: https://github.com/sourcehawk/triagent/releases/download)
#                          override the asset download root for local testing

set -eu

REPO="sourcehawk/triagent"
INSTALL_DIR="${TRIAGENT_INSTALL_DIR:-$HOME/.local/bin}"
BASE_URL="${TRIAGENT_BASE_URL:-https://github.com/${REPO}/releases/download}"

die() { echo "triagent: $*" >&2; exit 1; }
info() { echo "triagent: $*"; }

# --- detect OS / arch -------------------------------------------------------
os="$(uname -s)"
arch="$(uname -m)"
case "$os" in
  Linux)  os="linux" ;;
  Darwin) os="darwin" ;;
  *) die "no prebuilt binary for $os $arch — see https://github.com/${REPO}/releases for manual download" ;;
esac
case "$arch" in
  x86_64|amd64)   arch="amd64" ;;
  aarch64|arm64)  arch="arm64" ;;
  *) die "no prebuilt binary for $os $arch — see https://github.com/${REPO}/releases for manual download" ;;
esac

# --- pick a downloader ------------------------------------------------------
if command -v curl >/dev/null 2>&1; then
  download() { curl -fsSL "$1" -o "$2"; }
  fetch()    { curl -fsSL "$1"; }
elif command -v wget >/dev/null 2>&1; then
  download() { wget -qO "$2" "$1"; }
  fetch()    { wget -qO- "$1"; }
else
  die "neither curl nor wget found — install one of them and re-run"
fi

# --- resolve version --------------------------------------------------------
version="${TRIAGENT_VERSION:-}"
if [ -z "$version" ]; then
  info "resolving latest release..."
  api_response=$(fetch "https://api.github.com/repos/${REPO}/releases/latest") || \
    die "failed to query GitHub API (rate-limited?) — set TRIAGENT_VERSION=vX.Y.Z to skip"
  version=$(echo "$api_response" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)
  [ -n "$version" ] || die "could not parse tag_name from GitHub API response"
fi

info "installing $version for $os/$arch to $INSTALL_DIR"

# --- download archive + checksums ------------------------------------------
trim_v=$(echo "$version" | sed 's/^v//')
archive="triagent_${trim_v}_${os}_${arch}.tar.gz"
tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT

info "downloading $archive..."
download "${BASE_URL}/${version}/${archive}"        "${tmpdir}/${archive}"     || die "archive download failed"
download "${BASE_URL}/${version}/checksums.txt"    "${tmpdir}/checksums.txt"   || die "checksum download failed"

# --- verify checksum --------------------------------------------------------
info "verifying checksum..."
expected=$(grep " ${archive}\$" "${tmpdir}/checksums.txt" | awk '{print $1}')
[ -n "$expected" ] || die "no checksum entry for $archive"

if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "${tmpdir}/${archive}" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 "${tmpdir}/${archive}" | awk '{print $1}')
else
  die "no sha256sum or shasum found — cannot verify download"
fi

[ "$expected" = "$actual" ] || die "checksum mismatch (expected $expected, got $actual) — refusing to install"

# --- extract ---------------------------------------------------------------
info "extracting..."
tar -xzf "${tmpdir}/${archive}" -C "${tmpdir}" || die "extraction failed"

# --- install ---------------------------------------------------------------
mkdir -p "$INSTALL_DIR" 2>/dev/null || \
  die "cannot create $INSTALL_DIR — set TRIAGENT_INSTALL_DIR to a writable path or re-run with sudo"
[ -w "$INSTALL_DIR" ] || \
  die "cannot write to $INSTALL_DIR — set TRIAGENT_INSTALL_DIR to a writable path or re-run with sudo"

for bin in triagent triagent-mcp; do
  mv "${tmpdir}/${bin}" "${INSTALL_DIR}/${bin}"
  chmod +x "${INSTALL_DIR}/${bin}"
  # macOS Gatekeeper: clear quarantine xattr so the unsigned binary launches
  # without the "cannot be opened" prompt. Harmless on Linux (xattr absent).
  if [ "$os" = "darwin" ] && command -v xattr >/dev/null 2>&1; then
    xattr -d com.apple.quarantine "${INSTALL_DIR}/${bin}" 2>/dev/null || true
  fi
done

# --- PATH hint -------------------------------------------------------------
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    echo ""
    echo "triagent: $INSTALL_DIR is not on your PATH. Add it with:"
    case "$(basename "${SHELL:-bash}")" in
      zsh)  echo "  echo 'export PATH=\"$INSTALL_DIR:\$PATH\"' >> ~/.zshrc" ;;
      fish) echo "  fish_add_path $INSTALL_DIR" ;;
      *)    echo "  echo 'export PATH=\"$INSTALL_DIR:\$PATH\"' >> ~/.bashrc" ;;
    esac
    echo "Then restart your shell."
    ;;
esac

echo ""
info "installed. Run: triagent start"
```

- [ ] **Step 2: Lint with shellcheck (if available)**

```sh
shellcheck docs/site/public/install.sh
```
Expected: no errors. If shellcheck is unavailable, skip — manual smoke tests below catch real bugs.

- [ ] **Step 3: Local end-to-end test with fake assets**

The script honors `TRIAGENT_BASE_URL`, so we can serve a fake release locally without publishing anything.

```sh
# Build a snapshot to get a real archive + checksums.
make frontend
goreleaser release --snapshot --clean --skip=publish,homebrew

# Serve the dist/ directory at a path that mirrors the GitHub layout.
# GoReleaser puts archives directly in dist/ and the checksums file too.
mkdir -p /tmp/triagent-fake/v0.0.1-test
cp dist/triagent_*.tar.gz dist/triagent_*.zip dist/checksums.txt /tmp/triagent-fake/v0.0.1-test/ 2>/dev/null
( cd /tmp/triagent-fake && python3 -m http.server 9000 ) &
SERVER_PID=$!

# Install with the fake server as the source.
TRIAGENT_BASE_URL=http://localhost:9000 \
TRIAGENT_VERSION=v0.0.1-test \
TRIAGENT_INSTALL_DIR=/tmp/triagent-bin \
sh docs/site/public/install.sh

# Verify the binaries landed and run.
/tmp/triagent-bin/triagent --version
/tmp/triagent-bin/triagent-mcp --version

# Clean up.
kill $SERVER_PID
rm -rf /tmp/triagent-fake /tmp/triagent-bin dist/
```
Expected: both `--version` calls succeed and print `triagent version 0.0.2-next-<sha>` (or similar snapshot version).

- [ ] **Step 4: Docker smoke test on a clean Ubuntu**

(This confirms the script works on an OS without any build tooling installed.)

```sh
docker run --rm -v "$PWD":/w -w /w ubuntu:24.04 bash -c '
  apt-get update -qq && apt-get install -y -qq curl ca-certificates
  sh docs/site/public/install.sh
'
```
Expected: script runs to completion; either installs the latest published release (if one exists) or fails cleanly with the "rate-limited / no release" message. Either is acceptable here — we're verifying it doesn't crash on a fresh OS.

- [ ] **Step 5: Commit**

```sh
git add docs/site/public/install.sh
git commit -m "feat(install): add POSIX install script

Detects OS/arch (linux|darwin × amd64|arm64), downloads the matching
release archive + checksums, verifies SHA-256, installs both binaries
to ~/.local/bin (overridable via TRIAGENT_INSTALL_DIR), clears macOS
quarantine xattr, and prints a PATH hint if needed. Honors
TRIAGENT_VERSION and TRIAGENT_BASE_URL for pinning and local testing.

Served via the existing docs Pages deploy at
sourcehawk.github.io/triagent/install.sh."
```

---

## Task 5: Write `install.ps1` (Windows / PowerShell)

Same shape as `install.sh`. Hosted at `sourcehawk.github.io/triagent/install.ps1`.

**Files:**
- Create: `docs/site/public/install.ps1`

- [ ] **Step 1: Create `docs/site/public/install.ps1`**

```powershell
# triagent install script (Windows / PowerShell)
# Usage: irm https://sourcehawk.github.io/triagent/install.ps1 | iex
#
# Env vars:
#   $env:TRIAGENT_VERSION       (default: latest)
#   $env:TRIAGENT_INSTALL_DIR   (default: $env:LOCALAPPDATA\Programs\triagent)
#   $env:TRIAGENT_BASE_URL      override download root for testing

$ErrorActionPreference = 'Stop'
# Defensive TLS pin for older PowerShell hosts (PS 5.1 on stock Windows).
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$repo       = 'sourcehawk/triagent'
$installDir = if ($env:TRIAGENT_INSTALL_DIR) { $env:TRIAGENT_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\triagent' }
$baseUrl    = if ($env:TRIAGENT_BASE_URL)    { $env:TRIAGENT_BASE_URL }    else { "https://github.com/$repo/releases/download" }

function Die([string]$msg)  { Write-Host "triagent: $msg" -ForegroundColor Red; exit 1 }
function Info([string]$msg) { Write-Host "triagent: $msg" }

# --- detect arch (Windows: amd64 only in v1) --------------------------------
if (-not [Environment]::Is64BitOperatingSystem) {
  Die 'only 64-bit Windows is supported — see https://github.com/sourcehawk/triagent/releases'
}
$arch = 'amd64'

# --- resolve version --------------------------------------------------------
$version = $env:TRIAGENT_VERSION
if (-not $version) {
  Info 'resolving latest release...'
  try {
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest" -UseBasicParsing
    $version = $release.tag_name
  } catch {
    Die "failed to query GitHub API (rate-limited?) — set `$env:TRIAGENT_VERSION=vX.Y.Z to skip"
  }
}

Info "installing $version for windows/$arch to $installDir"

# --- download archive + checksums ------------------------------------------
$trimV   = $version -replace '^v', ''
$archive = "triagent_${trimV}_windows_${arch}.zip"
$tmpdir  = New-Item -ItemType Directory -Path (Join-Path $env:TEMP "triagent-install-$([guid]::NewGuid())")
try {
  Info "downloading $archive..."
  Invoke-WebRequest -Uri "$baseUrl/$version/$archive"      -OutFile (Join-Path $tmpdir $archive)        -UseBasicParsing
  Invoke-WebRequest -Uri "$baseUrl/$version/checksums.txt" -OutFile (Join-Path $tmpdir 'checksums.txt') -UseBasicParsing

  # --- verify checksum ----------------------------------------------------
  Info 'verifying checksum...'
  $entry = (Get-Content (Join-Path $tmpdir 'checksums.txt')) | Where-Object { $_ -match " $([regex]::Escape($archive))$" }
  if (-not $entry) { Die "no checksum entry for $archive" }
  $expected = ($entry -split '\s+')[0].ToLower()
  $actual   = (Get-FileHash -Algorithm SHA256 (Join-Path $tmpdir $archive)).Hash.ToLower()
  if ($expected -ne $actual) { Die "checksum mismatch (expected $expected, got $actual)" }

  # --- extract ------------------------------------------------------------
  Info 'extracting...'
  Expand-Archive -Path (Join-Path $tmpdir $archive) -DestinationPath $tmpdir -Force

  # --- install ------------------------------------------------------------
  New-Item -ItemType Directory -Path $installDir -Force | Out-Null
  Copy-Item -Path (Join-Path $tmpdir 'triagent.exe')     -Destination (Join-Path $installDir 'triagent.exe')     -Force
  Copy-Item -Path (Join-Path $tmpdir 'triagent-mcp.exe') -Destination (Join-Path $installDir 'triagent-mcp.exe') -Force
}
finally {
  Remove-Item -Recurse -Force $tmpdir -ErrorAction SilentlyContinue
}

# --- PATH update -----------------------------------------------------------
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if ($userPath -notlike "*$installDir*") {
  $newPath = if ($userPath) { "$userPath;$installDir" } else { $installDir }
  [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
  Info "added $installDir to user PATH. Open a new terminal for the change to take effect."
}

Write-Host ''
Info 'installed. Run: triagent start'
```

- [ ] **Step 2: Syntax-parse the script**

If PowerShell is available on the dev box:
```sh
pwsh -NoProfile -Command "[void][System.Management.Automation.Language.Parser]::ParseFile('docs/site/public/install.ps1', [ref]\$null, [ref]\$null); 'OK'"
```
Expected: `OK`.

If `pwsh` is not installed locally, skip — full Windows verification happens in Task 7 (rollout) on a real Windows host or via a `windows-latest` GitHub Actions runner.

- [ ] **Step 3: Commit**

```sh
git add docs/site/public/install.ps1
git commit -m "feat(install): add PowerShell install script

Same shape as install.sh: resolves latest tag (or honors
\$env:TRIAGENT_VERSION), downloads the windows/amd64 zip + checksums,
verifies SHA-256 via Get-FileHash, extracts to
%LOCALAPPDATA%\\Programs\\triagent, and appends that dir to the user PATH
if not already present. Manual smoke on a Windows host happens during
the rc rollout in plan Task 7."
```

---

## Task 6: Update README install section

Replace the current "Install" subsection with the one-liners for each OS plus the manual + build-from-source fallbacks.

**Files:**
- Modify: `README.md:94-107`

- [ ] **Step 1: Replace the `### Install` block (lines 94 through 107 inclusive)**

Find this block:

```md
### Install

Releases ship as two standalone binaries: `triagent` (the launcher) and `triagent-mcp` (the MCP multiplexer). Keep
them in the same directory and put `triagent` on `$PATH` — the launcher locates `triagent-mcp` adjacent to itself,
or anywhere on `$PATH`.

Building from source:

```sh
make build    # requires Node 20+ and Go (see .tool-versions)
```

The Next.js frontend is embedded in the launcher binary, so the runtime ships as a single executable per binary.
```

Replace it with:

````md
### Install

**macOS / Linux:**

```sh
curl -fsSL https://sourcehawk.github.io/triagent/install.sh | sh
```

**Windows (PowerShell):**

```powershell
irm https://sourcehawk.github.io/triagent/install.ps1 | iex
```

**Homebrew (macOS / Linux):**

```sh
brew install sourcehawk/tap/triagent
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
````

- [ ] **Step 2: Verify the markdown renders**

Open `README.md` in your editor's markdown preview and confirm code blocks render correctly (the outer fence around the install block uses 4 backticks because it contains 3-backtick code blocks).

- [ ] **Step 3: Commit**

```sh
git add README.md
git commit -m "docs(readme): rewrite Install section with one-liners

Replace 'build from source' default with prebuilt-binary install
commands for macOS/Linux/Windows + Homebrew + manual download.
Build-from-source is retained as a fallback for contributors."
```

---

## Task 7: Rollout — first release

Manual steps: create the tap repo, add the secret, push the rc tag, validate end-to-end, then push the real tag. This task is not committed — it's the operational sequence.

**Files:** none (manual operations)

- [ ] **Step 1: Create the Homebrew tap repo**

On github.com: create a new public empty repo `sourcehawk/homebrew-tap` (no README, no .gitignore). Homebrew requires the `homebrew-` prefix; the tap user-facing name will be `sourcehawk/tap`.

- [ ] **Step 2: Create the fine-grained PAT for tap pushes**

In GitHub user settings → Developer settings → Personal access tokens → Fine-grained tokens → Generate new token:
- Resource owner: `sourcehawk`
- Repository access: `sourcehawk/homebrew-tap` only
- Permissions → Repository → `Contents: Read and write`
- Set a 1-year expiry (renew annually)

Copy the token.

- [ ] **Step 3: Add the secret to `sourcehawk/triagent`**

Settings → Secrets and variables → Actions → New repository secret:
- Name: `HOMEBREW_TAP_GITHUB_TOKEN`
- Value: the PAT from step 2

- [ ] **Step 4: Push the rc tag**

From the branch with Tasks 1–6 merged to `main`:
```sh
git checkout main
git pull
git tag v0.0.0-rc1
git push origin v0.0.0-rc1
```

Expected: `.github/workflows/release.yml` triggers within ~30s.

- [ ] **Step 5: Watch the workflow**

```sh
gh run watch
```
Or in the browser: `https://github.com/sourcehawk/triagent/actions`. Expected: workflow goes green in 5–8 min.

- [ ] **Step 6: Verify Release artifacts**

```sh
gh release view v0.0.0-rc1
```
Expected: 5 archives (linux amd64/arm64, darwin amd64/arm64, windows amd64) + `checksums.txt`, marked as **Pre-release**.

- [ ] **Step 7: Verify Homebrew tap update**

Visit `https://github.com/sourcehawk/homebrew-tap`. Expected: a `Formula/triagent.rb` file committed by goreleaser. (For pre-releases, GoReleaser may skip the tap update — that's fine; verify the `v0.1.0` tag updates it in step 12.)

- [ ] **Step 8: Install the rc on Linux via the install script**

```sh
TRIAGENT_VERSION=v0.0.0-rc1 \
TRIAGENT_INSTALL_DIR=/tmp/triagent-rc \
curl -fsSL https://sourcehawk.github.io/triagent/install.sh | sh

/tmp/triagent-rc/triagent --version
/tmp/triagent-rc/triagent-mcp --version
```
Expected: both report `0.0.0-rc1`.

- [ ] **Step 9: Install the rc on macOS via the install script (if you have a Mac)**

Same as step 8 on a Mac. Verify `xattr` removal worked: `triagent start` should launch without Gatekeeper prompt.

- [ ] **Step 10: Install the rc on Windows via the install script (if you have a Windows host)**

```powershell
$env:TRIAGENT_VERSION = 'v0.0.0-rc1'
irm https://sourcehawk.github.io/triagent/install.ps1 | iex
# Open a NEW terminal:
triagent --version
triagent-mcp --version
```
Expected: both report `0.0.0-rc1`.

If you don't have a Windows host, skip this step and rely on the docker-based parity testing — but commit to verifying on a real Windows host before declaring v0.1.0 done.

- [ ] **Step 11: Delete the rc tag if you want a clean release**

(Optional — leaving the rc is harmless, but if you want a tidy releases page:)
```sh
gh release delete v0.0.0-rc1 --yes
git push --delete origin v0.0.0-rc1
git tag -d v0.0.0-rc1
```

- [ ] **Step 12: Push the real release tag**

```sh
git tag v0.1.0
git push origin v0.1.0
```

Watch the workflow as in step 5. Expected: same archives + checksums, marked as the **Latest** release, and the Homebrew tap formula bumped to `v0.1.0`.

- [ ] **Step 13: Verify the Homebrew install path**

(On a Mac or Linux box with Homebrew installed:)
```sh
brew install sourcehawk/tap/triagent
triagent --version
triagent-mcp --version
```
Expected: both report `0.1.0`.

- [ ] **Step 14: Smoke-test `triagent start` end-to-end on at least one installed binary**

```sh
triagent start
```
Expected: launcher boots, opens a browser tab, the SPA loads. No regressions vs `make build` local runs.

- [ ] **Step 15: Announce / mark plan complete**

Update any TODO trackers; the public install paths are now live.

---

## Out of scope (deferred — track separately if needed)

These are explicitly excluded from this plan, per the spec's "Future extensions" section:

- macOS code signing & notarization (Apple Developer cert + CI secrets + entitlements work).
- Scoop manifest, winget submission.
- Linux package repos (`apt`, `dnf`, AUR).
- Docker image (`ghcr.io/sourcehawk/triagent`).
- `triagent self-update` in the launcher itself.

Each is a multi-day chunk and should get its own spec + plan when prioritized.
