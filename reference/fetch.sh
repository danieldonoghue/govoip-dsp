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

if ! git -C "$dest" cat-file -e "$COMMIT^{commit}" 2>/dev/null; then
	git -C "$dest" fetch -q --depth 1 origin "refs/tags/$TAG:refs/tags/$TAG"
fi

git -C "$dest" -c advice.detachedHead=false checkout -q --force "$COMMIT"
git -C "$dest" clean -q -fdx

head=$(git -C "$dest" rev-parse HEAD)
tagged=$(git -C "$dest" rev-parse "refs/tags/$TAG^{commit}")
if [ "$head" != "$COMMIT" ] || [ "$tagged" != "$COMMIT" ]; then
	echo "fetch.sh: expected $COMMIT, have HEAD $head and $TAG $tagged" >&2
	exit 1
fi
if [ -n "$(git -C "$dest" status --porcelain)" ]; then
	echo "fetch.sh: $dest has local modifications" >&2
	exit 1
fi
echo "speexdsp $TAG at $COMMIT in $dest"
