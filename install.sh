#!/bin/sh
# dotsync installer: downloads a release binary, verifies it, installs it.
#
#   curl -fsSL https://raw.githubusercontent.com/pungoyal/dotsync/main/install.sh | sh
#
# Environment:
#   DOTSYNC_VERSION              release tag to install (default: latest), e.g. v0.2.0
#   DOTSYNC_BIN_DIR              install directory (default: ~/.local/bin)
#   DOTSYNC_REQUIRE_ATTESTATION  if 1, fail unless the GitHub build provenance verifies
#                                (needs the GitHub CLI, `gh`)
set -eu

REPO="pungoyal/dotsync"
VERSION="${DOTSYNC_VERSION:-latest}"
BIN_DIR="${DOTSYNC_BIN_DIR:-$HOME/.local/bin}"
REQUIRE_ATTESTATION="${DOTSYNC_REQUIRE_ATTESTATION:-0}"

say() { printf '%s\n' "$*" >&2; }
die() { say "error: $*"; exit 1; }

fetch() { # fetch URL [OUTPUT]
	if command -v curl >/dev/null 2>&1; then
		if [ -n "${2:-}" ]; then curl -fsSL --retry 3 -o "$2" "$1"; else curl -fsSL --retry 3 "$1"; fi
	elif command -v wget >/dev/null 2>&1; then
		if [ -n "${2:-}" ]; then wget -q -O "$2" "$1"; else wget -q -O - "$1"; fi
	else
		die "need curl or wget"
	fi
}

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d ' ' -f 1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d ' ' -f 1
	else
		die "need sha256sum or shasum to verify the download"
	fi
}

case "$(uname -s)" in
	Darwin) os=darwin ;;
	Linux) os=linux ;;
	*) die "unsupported OS: $(uname -s) (dotsync supports macOS and Linux)" ;;
esac
case "$(uname -m)" in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) die "unsupported CPU: $(uname -m)" ;;
esac

if [ "$VERSION" = latest ]; then
	VERSION=$(fetch "https://api.github.com/repos/$REPO/releases/latest" |
		sed -n 's/^[[:space:]]*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)
	[ -n "$VERSION" ] || die "could not determine the latest release of $REPO"
fi
case "$VERSION" in v*) ;; *) VERSION="v$VERSION" ;; esac

archive="dotsync_${VERSION#v}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$VERSION"
tmp=$(mktemp -d 2>/dev/null || mktemp -d -t dotsync)
trap 'rm -rf "$tmp"' EXIT INT TERM

say "downloading dotsync $VERSION ($os/$arch)"
fetch "$base/$archive" "$tmp/$archive" || die "download failed: $base/$archive"
fetch "$base/checksums.txt" "$tmp/checksums.txt" || die "download failed: $base/checksums.txt"

expected=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")
[ -n "$expected" ] || die "$archive is not listed in checksums.txt"
actual=$(sha256 "$tmp/$archive")
[ "$expected" = "$actual" ] || die "checksum mismatch for $archive (expected $expected, got $actual)"
say "checksum verified"

if command -v gh >/dev/null 2>&1 && gh attestation verify "$tmp/$archive" --repo "$REPO" >/dev/null 2>&1; then
	say "build provenance verified (built by $REPO's release workflow)"
elif [ "$REQUIRE_ATTESTATION" = 1 ]; then
	die "could not verify build provenance (is the GitHub CLI installed and logged in?)"
else
	say "note: build provenance not checked (install the GitHub CLI to verify it automatically)"
fi

tar -xzf "$tmp/$archive" -C "$tmp" dotsync
mkdir -p "$BIN_DIR"
install -m 0755 "$tmp/dotsync" "$BIN_DIR/dotsync" 2>/dev/null || {
	cp "$tmp/dotsync" "$BIN_DIR/dotsync.tmp.$$"
	chmod 0755 "$BIN_DIR/dotsync.tmp.$$"
	mv -f "$BIN_DIR/dotsync.tmp.$$" "$BIN_DIR/dotsync"
}
say "installed $BIN_DIR/dotsync"

case ":$PATH:" in
	*":$BIN_DIR:"*) ;;
	*) say "note: $BIN_DIR is not on your PATH; add it to your shell configuration" ;;
esac
say ""
say "next: dotsync init <url-of-your-private-dotfiles-repo>"
