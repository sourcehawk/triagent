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
