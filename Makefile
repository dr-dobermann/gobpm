# Version number
VERSION = $(shell cat .version)

# GO = /mnt/c/Dvl/go/bin/go.exe
GO = go

# DC = docker-compose.exe
DC = docker compose

# Diff-coverage gate (SRD-002): minimum patch coverage on the lines a change
# adds/modifies. Raised 70 -> 80 -> 95 (the standing standard); next phase toward
# 100 as the coverage backlog is paid down. The gate is diff-only, so a higher
# floor binds new/changed lines without touching the untouched-code backlog.
# COVER_BASE is the ref the diff is taken against.
COVER_MIN ?= 95
COVER_BASE ?= origin/master
# Log/observability statements are excluded from the gate's denominator
# (covercheck -exclude-lines, v0.1.2+): a Debug/Info/Warn/Error call is
# observability, not logic, and shouldn't demand a test just to be "covered".
# Matches the two logger-access forms in the codebase: `.logger.LEVEL(` and
# `.Logger().LEVEL(`.
#
# Second/third/fifth regexes: sealed-interface marker methods —
# `func (X) Option() {}` (FIX-020), `func (X) mappedOutcome() {}` (SRD-037
# MappedOutcome), and `func (X) isLoopCharacteristics() {}` (SRD-054, the
# LoopCharacteristics seal). The marker is never invoked — it only makes the type
# satisfy the sealed interface at compile time. An empty, never-called marker body
# is structurally uncoverable.
#
# Fourth regex: empty-body no-op Lock/Unlock — `func (X) Lock() {}` (SRD-042
# scalarLeaf, an immutable path-read leaf). Even when called, an empty function
# body registers no coverage counter (a Go tooling limitation), so it is
# structurally uncoverable — like the markers above. Non-empty Lock/Unlock (the
# real mutex-backed ones) do NOT match the `\{\}` pattern and stay in the gate.
COVER_EXCLUDE ?= \.(logger|Logger\(\))\.(Debug|Info|Warn|Error)\(,func \(.*\) Option\(\) \{\},func \(.*\) mappedOutcome\(\) \{\},func \(.*\) (Lock|Unlock)\(\) \{\},func \(.*\) isLoopCharacteristics\(\) \{\}

# All Go modules in the monorepo (each with its own go.mod).
# Discovered dynamically so adding a new module needs no Makefile edit.
MODULES := $(shell /usr/bin/find . -name go.mod -not -path './.git/*' -exec dirname {} \;)

# CORE vs EXAMPLES split: the examples/* modules (~35 of ~38) dominate a full
# multi-module sweep, yet carry no tests and share the library's dependency
# graph through `replace ../..`. CI runs the core gate as the REQUIRED job and
# the examples sweep as a parallel, non-blocking job; `make ci` locally still
# runs BOTH (the full pre-push gate stays obligatory on dev). The loops below
# iterate $(MODULES), so a sub-make with MODULES="…" scopes any of them.
CORE_MODULES    := $(filter-out ./examples/%,$(MODULES))
EXAMPLE_MODULES := $(filter ./examples/%,$(MODULES))

# ---------------------------------------------------------------------------
# Tooling — versions are the single source of truth, mirrored by the
# "Install tools" step in .github/workflows/check.yml. `make tools` installs
# them locally so a developer's environment matches CI exactly.
#
# require-tool guards every target that shells out to one of these binaries:
# without it a missing tool makes the step a silent no-op (e.g. `vuln`
# "passing" locally because govulncheck was never installed, while CI fails).
# A missing tool must fail loudly, not be skipped.
# ---------------------------------------------------------------------------

MOCKERY_VERSION     := v3.5.0
GOLANGCI_VERSION    := v2.11.4
GOVULNCHECK_VERSION := v1.6.0
COVERCHECK_VERSION  := v0.2.0

define require-tool
@command -v $(1) >/dev/null 2>&1 || { echo "ERROR: '$(1)' not found in PATH. Run 'make tools' (installs CI-pinned versions) or: $(2)"; exit 1; }
endef

tools:
	$(GO) install github.com/vektra/mockery/v3@$(MOCKERY_VERSION)
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh \
		| sh -s -- -b "$$($(GO) env GOPATH)/bin" $(GOLANGCI_VERSION)
	$(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	$(GO) install github.com/dr-dobermann/covercheck/cmd/covercheck@$(COVERCHECK_VERSION)
.PHONY: tools

# ---------------------------------------------------------------------------
# Single-module targets (operate on the core module at repo root)
# ---------------------------------------------------------------------------

# VERSION (= `.version`, line 2) is stamped into the thresher build-info var so
# the startup banner reports the release, not the empty dev sentinel (FIX-024).
build:
	${GO} build -ldflags "-X github.com/dr-dobermann/gobpm/pkg/thresher.version=$(VERSION)" -o ./bin/ "./..."

update_modules:
	@go get -u ./...
	@go mod tidy
.PHONY: update_modules

lint:
	golangci-lint run --timeout=10m cmd/... internal/... pkg/...
.PHONY: lint

lint_fix:
	golangci-lint run --timeout=10m --fix cmd/... internal/... pkg/...
.PHONY: lint_fix

lint_all:
	golangci-lint run --timeout=10m ./...
.PHONY: lint_all

# Mocks are committed under generated/ (FIX-023), so `go test` runs directly —
# no mockery pre-step. Regenerate with `make gen_mock_files` when an interface
# changes. The `grep -v /generated/` keeps the generated packages (no tests,
# always 0%) out of the coverage numbers.
test:
	$(GO) test -v -count=1 -cover $$($(GO) list ./... | grep -v '/generated/')
.PHONY: test

test_coverage:
	$(GO) test -v -count=1 -coverprofile=c.out $$($(GO) list ./... | grep -v '/generated/')
	go tool cover -html=c.out
	rm c.out
.PHONY: test_coverage

tag:
	@git tag -a ${VERSION} -m "version ${VERSION}"
	@echo "Tag ${VERSION} created locally. Push manually: git push origin ${VERSION}"
.PHONY: tag

clear:
	rm -rf ./bin/
.PHONY: clear

# Regenerate the committed mocks (FIX-023) — run when a mocked interface
# changes, then commit generated/. No `go mod tidy`: committed mocks add no
# deps (testify is already required), and tidy-check-all guards go.mod/go.sum
# separately, so tidy stays off the mock path (it was mutating the tree).
gen_mock_files:
	$(call require-tool,mockery,$(GO) install github.com/vektra/mockery/v3@$(MOCKERY_VERSION))
	rm -rf generated/
	mockery
.PHONY: gen_mock_files

# CI drift-guard: regenerate the mocks and fail if the committed tree differs
# from what the current interfaces produce (a changed interface not regenerated
# + committed). Deterministic output + a pinned mockery make git diff a reliable
# signal.
mock-check:
	$(call require-tool,mockery,$(GO) install github.com/vektra/mockery/v3@$(MOCKERY_VERSION))
	rm -rf generated/
	mockery
	@git diff --exit-code -- generated/ || \
		{ echo "ERROR: committed mocks are stale — run 'make gen_mock_files' and commit generated/."; exit 1; }
.PHONY: mock-check

# ---------------------------------------------------------------------------
# Multi-module targets (iterate over every module in the monorepo)
# These are the source of truth used by .github/workflows/check.yml so that
# local `make` runs match what CI runs (no drift between local and GitHub).
# ---------------------------------------------------------------------------

build-all:
	@set -e; for dir in $(MODULES); do \
		echo "::group::build $$dir"; \
		(cd $$dir && $(GO) build -v ./...) || exit 1; \
		echo "::endgroup::"; \
	done
.PHONY: build-all

# TEST_CPUS pins the race test run's CPU budget to what the GitHub runner
# exposes (ubuntu-latest, public repo: 4 vCPUs), so scheduling-sensitive tests
# (stress / deferred-choice races) behave the same locally and on CI instead of
# hiding on a many-core dev box. GOMAXPROCS also drives `go test`'s package
# parallelism (-p), so both knobs sync. Override with `make ci TEST_CPUS=` to
# use the host default, or set another number to experiment.
TEST_CPUS ?= 4

test-all:
	@set -e; for dir in $(MODULES); do \
		echo "::group::test $$dir (TEST_CPUS=$(TEST_CPUS))"; \
		if [ "$$dir" = "." ]; then \
			(cd $$dir && GOMAXPROCS=$(TEST_CPUS) $(GO) test -race -count=1 -coverprofile=coverage.txt $$($(GO) list ./... | grep -v '/generated/')) || exit 1; \
		else \
			(cd $$dir && GOMAXPROCS=$(TEST_CPUS) $(GO) test -race -count=1 ./...) || exit 1; \
		fi; \
		echo "::endgroup::"; \
	done
.PHONY: test-all

lint-all-modules:
	$(call require-tool,golangci-lint,see https://golangci-lint.run/welcome/install/ or run 'make tools')
	@set -e; for dir in $(MODULES); do \
		echo "::group::lint $$dir"; \
		(cd $$dir && golangci-lint run --timeout=10m --config=$(CURDIR)/.golangci.yml) || exit 1; \
		echo "::endgroup::"; \
	done
.PHONY: lint-all-modules

tidy-check-all:
	@set -e; for dir in $(MODULES); do \
		echo "::group::tidy $$dir"; \
		(cd $$dir && $(GO) mod tidy) || exit 1; \
		echo "::endgroup::"; \
	done
	@echo "Checking for go.mod/go.sum drift after 'go mod tidy'..."
	@git diff --exit-code -- '**/go.mod' '**/go.sum' go.mod go.sum || \
		(echo "ERROR: go.mod or go.sum drifted after 'go mod tidy'. Commit the changes."; exit 1)
.PHONY: tidy-check-all

vuln:
	$(call require-tool,govulncheck,$(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION))
	@set -e; for dir in $(MODULES); do \
		echo "::group::govulncheck $$dir"; \
		(cd $$dir && govulncheck ./...) || exit 1; \
		echo "::endgroup::"; \
	done
.PHONY: vuln

# consumer-smoke proves gobpm is cleanly consumable via `go get` (FIX-024): a
# throwaway external module builds against it and must NOT pull test-only deps
# (testify) or the committed mocks (generated/mock*) into its dependency
# closure. Guards "flawless go get" against regressions — a root replace, a mock
# import leaking onto a non-test path, an accidental testify import in library
# code — any of which would surface here.
consumer-smoke:
	@set -e; \
	tmp=$$(mktemp -d); \
	trap 'rm -rf "$$tmp"' EXIT; \
	cd "$$tmp"; \
	$(GO) mod init gobpm.consumer.smoke >/dev/null; \
	$(GO) mod edit -replace github.com/dr-dobermann/gobpm=$(CURDIR); \
	$(GO) mod edit -require github.com/dr-dobermann/gobpm@v0.0.0; \
	printf 'package main\nimport _ "github.com/dr-dobermann/gobpm/pkg/thresher"\nfunc main() {}\n' > main.go; \
	$(GO) mod tidy >/dev/null 2>&1; \
	$(GO) build ./...; \
	leak=$$($(GO) list -deps . | grep -E 'stretchr/testify|/generated/mock' || true); \
	if [ -n "$$leak" ]; then \
		echo "ERROR: test-only deps leaked into a consumer's build closure:"; \
		echo "$$leak"; exit 1; \
	fi; \
	echo "consumer-smoke: an external module builds against gobpm with no testify/mock leak ✓"
.PHONY: consumer-smoke

# Diff-coverage gate: fail when the lines this change adds/modifies are covered
# below COVER_MIN. Consumes the coverage.txt that test-all produces (root
# module) — run `make test-all` first, or use `make ci` which orders them.
# Judges only changed lines, so the untouched-code backlog never blocks it.
cover-check:
	$(call require-tool,covercheck,$(GO) install github.com/dr-dobermann/covercheck/cmd/covercheck@$(COVERCHECK_VERSION))
	covercheck -min $(COVER_MIN) -base $(COVER_BASE) \
		-exclude-lines '$(COVER_EXCLUDE)' \
		-exclude-paths '^generated/' \
		-profiles coverage.txt
.PHONY: cover-check

# Core-scoped aliases — the REQUIRED CI job's steps (one target per workflow
# step, so the per-step log groups and timings survive the split). Each scopes
# the shared loop to the non-example modules via the MODULES override.
tidy-check-core:
	@$(MAKE) tidy-check-all MODULES="$(CORE_MODULES)"
.PHONY: tidy-check-core

lint-core:
	@$(MAKE) lint-all-modules MODULES="$(CORE_MODULES)"
.PHONY: lint-core

build-core:
	@$(MAKE) build-all MODULES="$(CORE_MODULES)"
.PHONY: build-core

test-core:
	@$(MAKE) test-all MODULES="$(CORE_MODULES)"
.PHONY: test-core

vuln-core:
	@$(MAKE) vuln MODULES="$(CORE_MODULES)"
.PHONY: vuln-core

# The examples sweep: tidy + lint + build over every examples/* module. No
# per-example test loop (the examples carry no tests — `go build` already
# compiles them) and no per-example govulncheck (each example consumes the
# library through `replace ../..`, so the core `vuln` scan already covers the
# shared dependency graph; scanning it 35 more times added minutes of CI for
# no new signal). Runs as CI's parallel non-blocking job AND inside the local
# `make ci`.
ci-examples:
	@$(MAKE) tidy-check-all lint-all-modules build-all MODULES="$(EXAMPLE_MODULES)"
.PHONY: ci-examples

# The core gate — everything the REQUIRED CI job runs, in the same order.
ci-core: mock-check tidy-check-core lint-core build-core consumer-smoke test-core cover-check vuln-core
.PHONY: ci-core

# Umbrella target that runs the full local-equivalent of CI (BOTH CI jobs).
# Use this before pushing to catch regressions before GitHub runs them.
# test-core writes coverage.txt; cover-check consumes it (single test run).
ci: ci-core ci-examples
.PHONY: ci

