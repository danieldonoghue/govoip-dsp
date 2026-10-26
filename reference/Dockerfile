# Canonical reference harness environment: Debian 13 (trixie) arm64 with
# clang 19, both pinned. See docs/decisions/wp0-reference-build.md.
FROM debian:trixie-slim@sha256:a66b19aa552f595b4ab276c6b4f9b24fa4546632c5ecd88cccdced4a6866323a

RUN apt-get update \
	&& apt-get install -y --no-install-recommends \
		clang-19=1:19.1.7-3+b1 \
		libclang-rt-19-dev=1:19.1.7-3+b1 \
		libc6-dev \
	&& rm -rf /var/lib/apt/lists/*

ENV CC=clang-19
