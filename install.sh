#!/bin/sh
# agent-sync installer.
#
#   curl -fsSL https://raw.githubusercontent.com/JustinBeaudry/agent-sync/main/install.sh | sh
#
# Downloads the prebuilt agent-sync binary for your OS/arch from the latest
# GitHub release, verifies its SHA-256 against the release checksums file, and
# installs it onto your PATH.
#
# Environment overrides:
#   AGENT_SYNC_VERSION      install a specific tag (e.g. v0.1.0) instead of latest
#   AGENT_SYNC_INSTALL_DIR  install location (default: /usr/local/bin, falling
#                           back to $HOME/.local/bin when that is not writable).
#                           When set explicitly, the install fails rather than
#                           falling back, so scripted installs get the binary in
#                           the exact location or a clear error.
#
# POSIX sh, no bash-isms. Requires: curl or wget, tar, and sha256sum or shasum.
# Verification is mandatory: the installer refuses to install if the release
# has no checksums.txt or no SHA-256 tool is available.
set -eu

REPO="JustinBeaudry/agent-sync"
BIN="agent-sync"

err() { printf 'install: %s\n' "$1" >&2; exit 1; }
info() { printf '%s\n' "$1" >&2; }

# --- detect os/arch, mapped to goreleaser's naming ---
os=$(uname -s)
case "$os" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) err "unsupported OS '$os'. Windows users: download the .zip from https://github.com/$REPO/releases" ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) err "unsupported architecture '$arch' (need amd64 or arm64)" ;;
esac

# --- pick a download tool ---
if command -v curl >/dev/null 2>&1; then
  dl() { curl -fsSL "$1"; }
  dlo() { curl -fsSL -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
  dl() { wget -qO- "$1"; }
  dlo() { wget -qO "$2" "$1"; }
else
  err "need curl or wget installed"
fi

# --- resolve the release and its asset URL ---
# Match the asset by the stable parts of goreleaser's name_template
# (agent-sync_<version>_<os>_<arch>.tar.gz) rather than reconstructing the
# version string, so the installer is robust to the exact tag formatting.
if [ -n "${AGENT_SYNC_VERSION:-}" ]; then
  api="https://api.github.com/repos/$REPO/releases/tags/$AGENT_SYNC_VERSION"
else
  api="https://api.github.com/repos/$REPO/releases/latest"
fi

release_json=$(dl "$api") || err "could not query the GitHub releases API (rate limited? no release yet?)"

# Split the JSON on quotes so each URL/string lands on its own line, then match
# with plain POSIX grep (avoids the non-POSIX `grep -o` extension).
urls=$(printf '%s' "$release_json" | tr '"' '\n')
asset_url=$(printf '%s' "$urls" \
  | grep "^https://github.com/$REPO/releases/download/.*_${os}_${arch}\.tar\.gz$" \
  | head -n1)
[ -n "$asset_url" ] || err "no $os/$arch build found in the release. Available assets are listed at https://github.com/$REPO/releases"

checksums_url=$(printf '%s' "$urls" \
  | grep "^https://github.com/$REPO/releases/download/.*checksums\.txt$" \
  | head -n1)

tag=$(printf '%s' "$release_json" | sed -n 's/.*"tag_name"[ ]*:[ ]*"\([^"]*\)".*/\1/p' | head -n1)
archive=$(basename "$asset_url")
info "Installing $BIN $tag ($os/$arch)..."

# --- download into a temp dir ---
tmp=$(mktemp -d 2>/dev/null || mktemp -d -t agent-sync)
trap 'rm -rf "$tmp"' EXIT INT TERM
dlo "$asset_url" "$tmp/$archive" || err "download failed: $asset_url"

# --- verify checksum (mandatory: fail closed when it cannot be verified) ---
[ -n "$checksums_url" ] || err "release has no checksums.txt — refusing to install unverified"
dlo "$checksums_url" "$tmp/checksums.txt" || err "could not download checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then
  sum=$(sha256sum "$tmp/$archive" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  sum=$(shasum -a 256 "$tmp/$archive" | awk '{print $1}')
else
  err "no sha256sum or shasum found — cannot verify the download. Install coreutils (sha256sum) or perl (shasum), or download + verify manually from https://github.com/$REPO/releases"
fi
# -F: the checksums line is matched literally, so dots in the archive name are
# not treated as regex wildcards. goreleaser writes "<sha256>  <filename>".
grep -Fq "$sum  $archive" "$tmp/checksums.txt" \
  || err "checksum mismatch for $archive — refusing to install"
info "Checksum verified."

# --- extract and install ---
tar -xzf "$tmp/$archive" -C "$tmp" "$BIN" || err "could not extract $BIN from $archive"
chmod +x "$tmp/$BIN"

dir="${AGENT_SYNC_INSTALL_DIR:-/usr/local/bin}"

if mkdir -p "$dir" 2>/dev/null && mv "$tmp/$BIN" "$dir/$BIN" 2>/dev/null; then
  :
elif [ -n "${AGENT_SYNC_INSTALL_DIR:-}" ]; then
  # An explicit target was requested — do not silently install elsewhere.
  err "could not install to AGENT_SYNC_INSTALL_DIR=$dir (missing or not writable). Create it / fix permissions and retry, or unset it to use the default."
elif command -v sudo >/dev/null 2>&1 && sudo install -d "$dir" 2>/dev/null && sudo install -m 0755 "$tmp/$BIN" "$dir/$BIN"; then
  # `sudo install` copies as root with an explicit mode, so a non-root user is
  # never left owning a binary in a system directory (avoids a local
  # privilege-escalation / binary-hijack vector that `sudo mv` from a
  # user-owned tmpdir would create on a same-filesystem rename).
  :
else
  dir="$HOME/.local/bin"
  if ! { mkdir -p "$dir" && mv "$tmp/$BIN" "$dir/$BIN"; }; then
    err "could not install to $dir"
  fi
  case ":$PATH:" in
    *":$dir:"*) ;;
    *) info "note: $dir is not on your PATH. Add it: export PATH=\"$dir:\$PATH\"" ;;
  esac
fi

info "Installed: $dir/$BIN"
info "Run '$BIN --help' to get started, or see https://github.com/$REPO/blob/main/docs/quickstart.md"
