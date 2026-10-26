#!/bin/sh
# Regenerates the SpeexDSP reference fixtures (docs/testing.md, "Reference
# harness").
#
#   reference/fixtures.sh [--check] [component...]
#
# Without --check it rewrites testdata/speexdsp/<component>/. With --check it
# writes to reference/build/ and compares the output hashes with the
# committed manifests. HARNESS_MODE=container|host picks the toolchain; the
# default is the container when Docker is usable, else the host clang.
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

check=0
if [ "${1:-}" = "--check" ]; then
	check=1
	shift
fi
components="$*"
if [ -z "$components" ]; then
	components=$(for f in reference/drivers/*.c; do basename "$f" .c; done)
fi

mode=${HARNESS_MODE:-auto}
if [ "$mode" = auto ]; then
	if docker info >/dev/null 2>&1; then mode=container; else mode=host; fi
fi
echo "fixtures: mode $mode, components: $components"

sh reference/fetch.sh

# Drivers that read Go-generated inputs name the package that writes them.
for c in $components; do
	pkg=$(sed -n 's/^ \* harness-inputs: *//p' "reference/drivers/$c.c")
	if [ -n "$pkg" ]; then
		dir="$root/reference/build/in/$c"
		rm -rf "$dir"
		mkdir -p "$dir"
		go test "$pkg" -run '^TestHarnessInputs$' -count=1 -args -harness-inputs="$dir"
	fi
done

IMAGE_BASE=debian:trixie-slim@sha256:a66b19aa552f595b4ab276c6b4f9b24fa4546632c5ecd88cccdced4a6866323a
PLATFORM=linux/arm64
case "$mode" in
container)
	docker build -q --platform "$PLATFORM" -t govoip-dsp-harness reference >/dev/null
	# shellcheck disable=SC2086
	docker run --rm --platform "$PLATFORM" --user "$(id -u):$(id -g)" \
		-v "$root:/src" -w /src \
		-e HARNESS_MODE=container \
		-e HARNESS_IMAGE="$IMAGE_BASE + clang-19=1:19.1.7-3+b1 ($PLATFORM)" \
		-e HARNESS_CHECK="$check" \
		govoip-dsp-harness sh reference/harness.sh $components
	;;
host)
	# shellcheck disable=SC2086
	HARNESS_MODE=host HARNESS_IMAGE="" HARNESS_CHECK="$check" \
		sh reference/harness.sh $components
	;;
*)
	echo "fixtures: unknown HARNESS_MODE $mode" >&2
	exit 1
	;;
esac
