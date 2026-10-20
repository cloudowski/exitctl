#!/bin/sh
# Install exitctl from a GitHub release.
#
#   curl -fsSL https://raw.githubusercontent.com/cloudowski/exitctl/main/install.sh | sh
#
# Environment:
#   VERSION      release tag to install, for example v0.1.0 (default: latest stable release)
#   INSTALL_DIR  target directory (default: /usr/local/bin if writable, else ~/.local/bin)
#
# The archive is checked against the release's checksums.txt. If cosign is installed,
# the signature of checksums.txt is verified too.
set -eu

REPO="cloudowski/exitctl"
BASE_URL="${EXITCTL_BASE_URL:-https://github.com/$REPO/releases}"

say() { printf '%s\n' "$*"; }
die() { printf 'install.sh: %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

have curl || die "curl is required"
have tar || die "tar is required"

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) die "unsupported OS: $(uname -s) (releases exist for linux and darwin)" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) die "unsupported architecture: $(uname -m) (releases exist for amd64 and arm64)" ;;
esac

if [ -n "${VERSION:-}" ]; then
  url="$BASE_URL/download/$VERSION"
else
  url="$BASE_URL/latest/download"
fi

if [ -n "${INSTALL_DIR:-}" ]; then
  dir="$INSTALL_DIR"
elif [ -w /usr/local/bin ]; then
  dir=/usr/local/bin
else
  dir="$HOME/.local/bin"
fi

file="exitctl_${os}_${arch}.tar.gz"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM

say "Downloading $file (${VERSION:-latest})"
curl -fsSL -o "$tmp/$file" "$url/$file" || die "download failed: $url/$file"
curl -fsSL -o "$tmp/checksums.txt" "$url/checksums.txt" || die "download failed: $url/checksums.txt"

if have cosign; then
  curl -fsSL -o "$tmp/checksums.txt.sigstore.json" "$url/checksums.txt.sigstore.json" ||
    die "download failed: $url/checksums.txt.sigstore.json"
  cosign verify-blob "$tmp/checksums.txt" \
    --bundle "$tmp/checksums.txt.sigstore.json" \
    --certificate-identity-regexp "^https://github.com/$REPO/\\.github/workflows/release\\.yml@refs/tags/v" \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com >/dev/null 2>&1 ||
    die "cosign signature verification of checksums.txt failed"
  say "Signature verified (cosign)"
else
  say "cosign not found: skipping signature verification"
fi

want="$(awk -v f="$file" '$2 == f { print $1 }' "$tmp/checksums.txt")"
[ -n "$want" ] || die "$file not listed in checksums.txt"
if have sha256sum; then
  got="$(sha256sum "$tmp/$file" | awk '{ print $1 }')"
elif have shasum; then
  got="$(shasum -a 256 "$tmp/$file" | awk '{ print $1 }')"
else
  die "sha256sum or shasum is required to verify the download"
fi
[ "$got" = "$want" ] || die "checksum mismatch for $file (expected $want, got $got)"
say "Checksum OK"

tar -xzf "$tmp/$file" -C "$tmp" exitctl
mkdir -p "$dir"
if [ -w "$dir" ]; then
  install -m 0755 "$tmp/exitctl" "$dir/exitctl"
else
  say "$dir is not writable, using sudo"
  sudo install -m 0755 "$tmp/exitctl" "$dir/exitctl"
fi

say "Installed $("$dir/exitctl" version | head -n 1) to $dir/exitctl"
case ":$PATH:" in
  *":$dir:"*) ;;
  *) say "Note: $dir is not in your PATH" ;;
esac
