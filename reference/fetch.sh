#!/bin/sh
# Fetches SpeexDSP at the pinned commit into reference/speexdsp/ and verifies
# it. Safe to run repeatedly; it needs the network only when the commit is
# not already present.
set -eu

URL=https://github.com/xiph/speexdsp.git
TAG=SpeexDSP-1.2.1
COMMIT=1b28a0f61bc31162979e1f26f3981fc3637095c8

here=$(cd "$(dirname "$0")" && pwd)
dest="$here/speexdsp"

if [ ! -d "$dest/.git" ]; then
	rm -rf "$dest"
	git init -q "$dest"
	git -C "$dest" remote add origin "$URL"
fi

if [ -n "$(git -C "$dest" status --porcelain)" ]; then
	echo "fetch.sh: $dest has local changes; remove the directory to refetch" >&2
	exit 1
fi
if ! git -C "$dest" rev-parse -q --verify "refs/tags/$TAG^{commit}" >/dev/null; then
	git -C "$dest" fetch -q --depth 1 origin "refs/tags/$TAG:refs/tags/$TAG"
fi
tagged=$(git -C "$dest" rev-parse -q --verify "refs/tags/$TAG^{commit}" || true)
if [ "$tagged" != "$COMMIT" ]; then
	echo "fetch.sh: tag $TAG is ${tagged:-missing}, expected $COMMIT" >&2
	exit 1
fi
git -C "$dest" -c advice.detachedHead=false checkout -q "$COMMIT"
head=$(git -C "$dest" rev-parse HEAD)
if [ "$head" != "$COMMIT" ]; then
	echo "fetch.sh: HEAD is $head, expected $COMMIT" >&2
	exit 1
fi
echo "speexdsp $TAG at $COMMIT in $dest"
