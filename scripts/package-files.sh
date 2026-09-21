#!/bin/sh
# Writes the files the release packages ship besides the binary into build/:
#   dotsync.1.gz    the dotsync(1) man page (archives, .deb, Homebrew formula)
#   changelog.gz    a Debian changelog entry for this version, from CHANGELOG.md (.deb)
# GoReleaser runs it before building.
#
#   scripts/package-files.sh <version> <commit unix timestamp>
set -eu
version=$1
timestamp=$2
maintainer="Puneet Goyal <pungoyal@gmail.com>" # keep in step with .goreleaser.yaml
mkdir -p build

# GNU date, then BSD date (macOS).
rfc2822=$(date -u -R -d "@$timestamp" 2>/dev/null || date -u -R -r "$timestamp")
iso=$(date -u +%Y-%m-%d -d "@$timestamp" 2>/dev/null || date -u +%Y-%m-%d -r "$timestamp")

# gzip -n leaves the name and time out of the header, so the output is reproducible.
go run ./internal/cmd/genman -version "$version" -date "$iso" > build/dotsync.1
gzip -9 -n -f build/dotsync.1

# This version's section of CHANGELOG.md as Debian changelog lines: headings become
# "[ Features ]", entries become "* …" without their issue and commit links.
notes=$(awk -v v="$version" '
	/^## / { if (found) exit; found = index($0, "## [" v "]") == 1; next }
	found && /^### / { sub(/^### /, ""); print "[ " $0 " ]"; next }
	found && /^\* / { print }
' CHANGELOG.md | sed -e 's/ (\[[^]]*\]([^)]*))//g' -e 's/\*\*\([^*]*\)\*\*/\1/g' -e 's/&lt;/</g; s/&gt;/>/g; s/&amp;/\&/g')
if [ -z "$notes" ]; then
	notes="* Development build."
fi
{
	echo "dotsync ($version) stable; urgency=medium"
	echo
	printf '%s\n' "$notes" | fold -s -w 76 | awk '
		/^\[ / { if (NR > 1) print ""; print "  " $0; next }
		/^\* / { print "  " $0; next }
		{ print "    " $0 }'
	echo
	echo "  * Release notes:"
	echo "    https://github.com/pungoyal/dotsync/releases/tag/v$version"
	echo
	echo " -- $maintainer  $rfc2822"
} | gzip -9 -n > build/changelog.gz
