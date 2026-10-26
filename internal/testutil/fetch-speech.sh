#!/bin/sh
# Downloads LibriSpeech dev-clean (https://www.openslr.org/12, CC BY 4.0) and
# converts the first SPEECH_COUNT utterances, in sorted path order, to 16 kHz
# mono little-endian int16 raw files in .cache/speech/librispeech/, which is
# git-ignored. Needs curl, tar and either flac or ffmpeg.
set -eu

URL=https://www.openslr.org/resources/12/dev-clean.tar.gz
MD5=42e2234ba48799c1f50f24a7926300a1 # from https://www.openslr.org/resources/12/md5sum.txt
COUNT=${SPEECH_COUNT:-20}

root=$(cd "$(dirname "$0")/../.." && pwd)
cache=$root/.cache/speech
out=$cache/librispeech
tarball=$cache/dev-clean.tar.gz
mkdir -p "$cache"

if [ ! -f "$tarball" ]; then
	curl -fL --retry 3 -o "$tarball.part" "$URL"
	mv "$tarball.part" "$tarball"
fi
if command -v md5sum >/dev/null 2>&1; then
	sum=$(md5sum "$tarball" | cut -d' ' -f1)
else
	sum=$(md5 -q "$tarball")
fi
if [ "$sum" != "$MD5" ]; then
	echo "fetch-speech: $tarball has MD5 $sum, want $MD5" >&2
	exit 1
fi

tar -tzf "$tarball" | grep '\.flac$' | sort | head -n "$COUNT" >"$cache/list.txt"
rm -rf "$cache/src" "$out"
mkdir -p "$cache/src" "$out"
tar -xzf "$tarball" -C "$cache/src" -T "$cache/list.txt"

while read -r f; do
	name=$(basename "$f" .flac)
	if command -v flac >/dev/null 2>&1; then
		flac -d -s -f --force-raw-format --endian=little --sign=signed \
			-o "$out/$name.raw" "$cache/src/$f"
	else
		ffmpeg -nostdin -loglevel error -y -i "$cache/src/$f" -f s16le -ac 1 -ar 16000 "$out/$name.raw"
	fi
done <"$cache/list.txt"

cat >"$out/ATTRIBUTION.txt" <<EOT
LibriSpeech ASR corpus, dev-clean subset, by Vassil Panayotov, Guoguo Chen,
Daniel Povey and Sanjeev Khudanpur, https://www.openslr.org/12, licensed
under CC BY 4.0 (https://creativecommons.org/licenses/by/4.0/). Converted
from FLAC to raw 16-bit little-endian PCM; otherwise unchanged.
EOT
echo "fetch-speech: $(wc -l <"$cache/list.txt" | tr -d ' ') files in $out"
