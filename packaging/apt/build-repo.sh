#!/bin/sh
# Builds the signed APT repository served at https://pungoyal.github.io/dotsync/apt/ from a
# directory of .deb packages. The docs workflow runs it with the latest release's packages.
#
#   packaging/apt/build-repo.sh <deb-dir> <out-dir>
#
# Environment:
#   APT_SIGNING_KEY   ASCII-armored secret key that signs the repository (required)
#
# Layout (a standard Debian archive, one suite):
#   <out>/dotsync.asc                                 public key, for Signed-By
#   <out>/dotsync.sources                             ready-made deb822 source entry
#   <out>/pool/main/d/dotsync/*.deb
#   <out>/dists/stable/{InRelease,Release,Release.gpg}
#   <out>/dists/stable/main/binary-<arch>/Packages{,.gz}
set -eu

debs=$1
out=$2
: "${APT_SIGNING_KEY:?set APT_SIGNING_KEY to the armored secret key that signs the repository}"
here=$(cd "$(dirname "$0")" && pwd)

for tool in apt-ftparchive gpg; do
	command -v "$tool" >/dev/null 2>&1 || { echo "error: $tool is required" >&2; exit 1; }
done

GNUPGHOME=$(mktemp -d)
export GNUPGHOME
trap 'gpgconf --kill all 2>/dev/null || true; rm -rf "$GNUPGHOME"' EXIT
printf '%s\n' "$APT_SIGNING_KEY" | gpg --batch --quiet --import
fpr=$(gpg --batch --with-colons --list-secret-keys | awk -F: '$1 == "fpr" { print $10; exit }')
[ -n "$fpr" ] || { echo "error: APT_SIGNING_KEY contains no secret key" >&2; exit 1; }

rm -rf "$out"
mkdir -p "$out/pool/main/d/dotsync"
cp "$debs"/*.deb "$out/pool/main/d/dotsync/"

cd "$out"
arches=$(for f in pool/main/d/dotsync/*.deb; do dpkg-deb --field "$f" Architecture; done | sort -u | tr '\n' ' ' | sed 's/ $//')
for arch in $arches; do
	dir=dists/stable/main/binary-$arch
	mkdir -p "$dir"
	apt-ftparchive --arch "$arch" packages pool > "$dir/Packages"
	gzip -9 -n -k "$dir/Packages"
done

apt-ftparchive \
	-o APT::FTPArchive::Release::Origin=dotsync \
	-o APT::FTPArchive::Release::Label=dotsync \
	-o APT::FTPArchive::Release::Suite=stable \
	-o APT::FTPArchive::Release::Codename=stable \
	-o APT::FTPArchive::Release::Components=main \
	-o "APT::FTPArchive::Release::Architectures=$arches" \
	-o APT::FTPArchive::Release::Description="dotsync releases" \
	release dists/stable > dists/stable/Release.tmp
mv dists/stable/Release.tmp dists/stable/Release

gpg --batch --yes --local-user "$fpr" --digest-algo SHA512 --clearsign --output dists/stable/InRelease dists/stable/Release
gpg --batch --yes --local-user "$fpr" --digest-algo SHA512 --armor --detach-sign --output dists/stable/Release.gpg dists/stable/Release

gpg --batch --armor --export "$fpr" > dotsync.asc
cp "$here/dotsync.sources" dotsync.sources
echo "APT repository for $arches signed by $fpr"
