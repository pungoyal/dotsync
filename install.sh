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

# fetch URL [OUTPUT] [LABEL]: never hangs silently. Connections time out, a transfer that stalls
# (under 1 KB/s for 20 s) is aborted and retried, and big downloads show progress.
fetch() {
	if command -v curl >/dev/null 2>&1; then
		set -- "$1" "${2:-}" "${3:-}"
		opts="-fL --connect-timeout 15 --speed-limit 1024 --speed-time 20 --retry 3 --retry-delay 2"
		if [ -n "$3" ] && [ -t 2 ]; then opts="$opts --progress-bar"; else opts="$opts -sS"; fi
		# If a download fails or stalls, try once more over IPv4: a broken IPv6 route to GitHub's
		# download host is the most common cause of hangs.
		# shellcheck disable=SC2086 # opts is a list of flags
		if [ -n "$2" ]; then
			curl $opts -o "$2" "$1" || { say "retrying over IPv4…"; curl $opts -4 -o "$2" "$1"; }
		else
			curl $opts "$1" || curl $opts -4 "$1"
		fi
	elif command -v wget >/dev/null 2>&1; then
		if [ -n "${2:-}" ]; then wget -q --timeout=20 --tries=3 -O "$2" "$1"; else wget -q --timeout=20 --tries=3 -O - "$1"; fi
	else
		die "need curl or wget"
	fi
}

download_hint() {
	say "error: download failed: $1"
	say "  if this keeps happening, check your connection to GitHub's download host:"
	say "    curl -v -o /dev/null $1"
	say "  or download the archive from https://github.com/$REPO/releases/tag/$VERSION by hand"
	exit 1
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
fetch "$base/$archive" "$tmp/$archive" progress || download_hint "$base/$archive"
fetch "$base/checksums.txt" "$tmp/checksums.txt" || download_hint "$base/checksums.txt"

expected=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")
[ -n "$expected" ] || die "$archive is not listed in checksums.txt"
actual=$(sha256 "$tmp/$archive")
[ "$expected" = "$actual" ] || die "checksum mismatch for $archive (expected $expected, got $actual)"
say "checksum verified"

if command -v gh >/dev/null 2>&1 && say "verifying build provenance with gh…" &&
	gh attestation verify "$tmp/$archive" --repo "$REPO" >/dev/null 2>&1; then
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
