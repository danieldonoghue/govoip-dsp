#!/bin/sh
# Builds and runs reference drivers with the toolchain it runs on. Called by
# reference/fixtures.sh, directly on the host or inside the container.
set -eu

: "${HARNESS_MODE:?}" "${HARNESS_CHECK:?}"
CC=${CC:-clang}
CFLAGS="-O2 -fwrapv -ffp-contract=off"
# No -fwrapv here: with it clang does not report signed overflow or shift
# undefined behaviour, which is what this build is for (docs/testing.md).
UBSAN_CFLAGS="-O2 -ffp-contract=off -fsanitize=undefined"
SPX=reference/speexdsp/libspeexdsp
INCLUDES="-DHAVE_CONFIG_H -Ireference -Ireference/common -Ireference/include -Ireference/speexdsp/include -I$SPX"
build=reference/build/$HARNESS_MODE

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1
	else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

# Prints the manifest from its "cases" key on: everything but the build block.
cases_of() {
	sed -n '/"cases": \[/,$p' "$1"
}

export HARNESS_MODE HARNESS_IMAGE
HARNESS_CC_VERSION=$($CC --version | head -n 1)
HARNESS_OS=$(uname -s)
HARNESS_ARCH=$(uname -m)
HARNESS_CONFIG_SHA256=$(sha256 reference/config.h)
export HARNESS_CC_VERSION HARNESS_OS HARNESS_ARCH HARNESS_CONFIG_SHA256

status=0
for c in "$@"; do
	driver=reference/drivers/$c.c
	srcs=""
	# shellcheck disable=SC2013
	for s in $(sed -n 's/^ \* harness-sources: *//p' "$driver"); do
		srcs="$srcs $SPX/$s"
	done
	mkdir -p "$build/bin" "$build/out/$c" "$build/ubsan/$c"

	HARNESS_CFLAGS=$CFLAGS
	export HARNESS_CFLAGS
	# shellcheck disable=SC2086
	$CC $CFLAGS $INCLUDES "$driver" reference/common/harness.c $srcs -lm -o "$build/bin/$c"
	if [ "$HARNESS_CHECK" = 1 ]; then
		out=$build/out/$c
	else
		out=testdata/speexdsp/$c
		mkdir -p "$out"
		rm -f "$out"/*.raw "$out/manifest.json"
	fi
	echo "harness: $c ($CC, $HARNESS_MODE) -> $out"
	"$build/bin/$c" "$out" "reference/build/in/$c"

	HARNESS_CFLAGS=$UBSAN_CFLAGS
	# shellcheck disable=SC2086
	$CC $UBSAN_CFLAGS $INCLUDES "$driver" reference/common/harness.c $srcs -lm -o "$build/bin/$c-ubsan"
	log=$build/ubsan/$c.log
	"$build/bin/$c-ubsan" "$build/ubsan/$c" "reference/build/in/$c" 2>"$log"
	reports=$(grep -c 'runtime error' "$log" || true)
	cases_of "$out/manifest.json" >"$build/out/$c.cases"
	cases_of "$build/ubsan/$c/manifest.json" >"$build/ubsan/$c.cases"
	if cmp -s "$build/out/$c.cases" "$build/ubsan/$c.cases"; then
		echo "harness: $c: UBSan build gives identical outputs; $reports report(s) in $log"
	else
		echo "harness: $c: UBSan build outputs DIFFER from the plain build" >&2
		status=1
	fi

	if [ "$HARNESS_CHECK" = 1 ]; then
		cases_of "testdata/speexdsp/$c/manifest.json" >"$build/out/$c.committed.cases"
		if cmp -s "$build/out/$c.cases" "$build/out/$c.committed.cases"; then
			echo "harness: $c: $HARNESS_MODE outputs match the committed fixtures"
		else
			echo "harness: $c: $HARNESS_MODE outputs DIFFER from the committed fixtures" >&2
			status=1
		fi
	fi
done
exit $status
