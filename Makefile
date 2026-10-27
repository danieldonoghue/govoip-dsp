# Build entry points for govoip-dsp. See CLAUDE.md and docs/testing.md.

GO ?= go
STATICCHECK ?= $(shell command -v staticcheck 2>/dev/null)

# GOOS/GOARCH pairs that have no CI runner. They are build-only, never tested.
CROSS_TARGETS ?= linux/arm linux/riscv64 linux/ppc64le linux/s390x \
	linux/mips64le freebsd/amd64 darwin/amd64 windows/arm64 wasip1/wasm

.PHONY: help check fmt-check vet test test-purego staticcheck deps-guard race \
	bench fixtures fixtures-host cross speech hooks

.DEFAULT_GOAL := help

# Lists every target that has a "## " description on its rule line.
help: ## List the targets with a short description (the default)
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  %-14s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

check: fmt-check vet test test-purego deps-guard staticcheck ## Run every pre-merge check

hooks: ## Run make check before every git push (once per clone)
	git config core.hooksPath .githooks

fmt-check: ## Fail if any file needs gofmt
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then \
		echo "gofmt needed on:"; echo "$$out"; exit 1; fi

vet: ## go vet with CGO_ENABLED=0
	CGO_ENABLED=0 $(GO) vet ./...

test: ## Run all tests with CGO_ENABLED=0
	CGO_ENABLED=0 $(GO) test -timeout 20m ./...

test-purego: ## Run all tests on the portable path (-tags purego)
	CGO_ENABLED=0 $(GO) test -timeout 20m -tags purego ./...

# Fails if any package (tests included) imports "C" or depends on a module
# outside the allow list.
ALLOWED_MODULES = github.com/danieldonoghue/govoip-dsp golang.org/x/sys
deps-guard: ## Fail on cgo use or an unexpected module dependency
	@cgo="$$(CGO_ENABLED=1 $(GO) list -deps -test -f '{{if and .CgoFiles (not .Standard)}}{{.ImportPath}}{{end}}' ./...)"; \
	if [ -n "$$cgo" ]; then echo "packages using cgo:"; echo "$$cgo"; exit 1; fi; \
	mods="$$($(GO) list -deps -test -f '{{with .Module}}{{.Path}}{{end}}' ./... | sort -u)"; \
	for m in $$mods; do case " $(ALLOWED_MODULES) " in *" $$m "*) ;; \
		*) echo "unexpected module dependency: $$m"; exit 1;; esac; done; \
	echo "deps-guard: no cgo, modules: $$(echo $$mods | tr '\n' ' ')"

staticcheck: ## Run staticcheck if it is installed
	@if [ -n "$(STATICCHECK)" ]; then echo "$(STATICCHECK) ./..."; \
		CGO_ENABLED=0 $(STATICCHECK) ./...; \
	else echo "staticcheck not installed; skipped"; fi

race: ## Run all tests with the race detector
	$(GO) test -timeout 20m -race ./...

bench: ## Run short benchmarks with allocation counts
	CGO_ENABLED=0 $(GO) test -run '^$$' -bench . -benchtime 100ms -benchmem ./...

# Regenerates every committed fixture set. Needs network once, plus Docker or
# a host clang. HARNESS_MODE=host forces the host toolchain.
fixtures: ## Regenerate the SpeexDSP reference fixtures (Docker or clang)
	sh reference/fixtures.sh

# Builds the harness with the host toolchain and compares its output hashes
# with the committed manifests, without rewriting them.
fixtures-host: ## Check the fixtures against a host-clang build
	HARNESS_MODE=host sh reference/fixtures.sh --check

cross: ## Cross-compile the build-only targets
	@set -e; for t in $(CROSS_TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; echo "build-only $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build ./... ; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) test -exec true -run '^$$' -count=1 ./... >/dev/null; \
	done

# Downloads the LibriSpeech sample used by speech tests into .cache/speech/.
speech: ## Download the LibriSpeech sample into .cache/speech/
	sh internal/testutil/fetch-speech.sh
