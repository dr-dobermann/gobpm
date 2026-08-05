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
#
# Sixth/seventh regexes: defensive constructor-error propagation (FIX-026).
# Library paths call the error-returning New*/Ready* constructors instead of
# the panicking Must* twins; where every input is valid by construction the
# propagation return is unreachable, yet errcheck rightly demands it. Two
# forms are excluded: a return whose LAST call is a named `*Err(...)` builder
# (the builders themselves are unit-tested directly — datumErr, cloneErr,
# payloadErr, jobOutputErr, ...), and a bare `return [nil,|"",|false,]* err`
# relay of an already-classified error. Reachable error paths keep their
# tests; only the single propagation line leaves the denominator.
#
# Ninth regex: a call to errs.Invariant — the project's ONE named construct for
# "this state is unreachable; the engine wired itself wrong" (FIX-034 §3.2.3).
# Such a branch cannot be driven from any input, so demanding a test for it
# would mean building broken engine states whose only purpose is to prove the
# impossible. The exclusion is deliberately tied to the constructor rather than
# to a file, a linter or a comment: `grep -rn "errs.Invariant("` lists every
# excluded line, silencing the gate requires saying so in code, and the
# constructor itself is unit-tested (pkg/errs). failInvariant is the
# instance-loop wrapper around it (fault the instance, stop the tracks) and is
# excluded on the same grounds.
#
# Tenth regex: a bare `return`. Like the closing brace below it carries no
# logic — it only ends a void function's guard — so excluding it can never hide
# untested behaviour; the statement that DID something sits on the line above.
#
# Eleventh regex: a `t.Fatal`/`t.Fatalf` call. It appears in non-test files
# only inside a shipped test-helper package (pkg/repository/repositorytest,
# SRD-078 FR-6): the suite's failure diagnostics fire only when an adapter
# under test VIOLATES the contract, so on a green run they are structurally
# unreachable — the same grounds as the log-call exclusions. The guarding
# condition's own line stays in the gate.
#
# Eighth regex: a bare closing brace. A `}` line carries no statement — it is
# counted only because it sits inside a profile block's line span (e.g. the
# excluded propagation return's block). Excluding it can never hide untested
# logic: every statement line stays in the gate.
# ^\s*observability\.Attr excludes a line that carries only vocabulary
# attributes — a map-literal entry or the continuation of a log/Fact/errs
# call. The patterns are matched per LINE, so an excluded construct wider
# than 120 columns (every multi-attribute log call) was excluded only on the
# line holding the call and counted on the rest. That under-exclusion caused
# three red gates before it was named. It cannot hide logic: the enclosing
# statement's own line is still measured, and a matching line is always an
# argument or a map entry, never a condition or a unit of work.
COVER_EXCLUDE ?= ^\s*observability\.Attr,\.(logger|Logger\(\))\.(Debug|Info|Warn|Error)\(,func \(.*\) Option\(\) \{\},func \(.*\) mappedOutcome\(\) \{\},func \(.*\) (Lock|Unlock)\(\) \{\},func \(.*\) isLoopCharacteristics\(\) \{\},^\s*return .*[a-z]Err\(.*err\)$$,^\s*return (nil. |\"\". |false. )*err$$,^\s*\}$$,errs\.Invariant\(,failInvariant\(,^\s*return$$,^\s*t\.Fatal

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

# Every examples/* directory MUST carry its own go.mod. EXAMPLE_MODULES is
# derived from find -name go.mod, so a directory without one silently leaves
# the sweep: it is compiled by the core build and NEVER RUN. examples/usertask
# sat outside the gate that way, which is the gap examples-module-check closes
# — the same reason link-check is blocking, since nothing failed while it rotted.
EXAMPLE_DIRS := $(shell /usr/bin/find ./examples -mindepth 1 -maxdepth 1 -type d)

# Root-module packages eligible for unit-test coverage. Generated mocks are
# drift-checked; examples are build-checked, and standalone example modules are
# also run end-to-end. Neither belongs in the coverage denominator. The
# end-anchored alternative also excludes a package located directly at
# generated/ or examples/.
COVER_PACKAGES = $$($(GO) list ./... | grep -Ev '/(generated|examples)(/|$$)')

# Every module writes its OWN profile (test-all), and the gate reads all of
# them. Until FIX-034 the profile was written only in the root's special case,
# so runtime/ and the three adapters were never diff-gated at all — two of them
# landed after COVER_MIN reached 95 and CI stayed green regardless of what they
# added. Derived from CORE_MODULES, so a module added tomorrow is gated the day
# it appears rather than when someone remembers this line. Missing profiles are
# skipped: `make cover-check` may run after a scoped test-all, and covercheck
# would reject a path that does not exist.
COVER_PROFILES = $$(for d in $(CORE_MODULES); do \
		[ -f "$$d/coverage.txt" ] && printf '%s/coverage.txt,' "$$d"; \
	done | sed 's/,$$//')

# ---------------------------------------------------------------------------
# Tooling — versions are the single source of truth, mirrored by the
# "Install tools" step in .github/workflows/check.yml. `make tools` installs
# them locally so a developer's environment matches CI exactly.
#
# require-go-tool guards every target that shells out to one of these binaries:
# without it a missing tool makes the step a silent no-op (e.g. `vuln`
# "passing" locally because govulncheck was never installed, while CI fails).
# Presence alone is insufficient: an older binary can accept the command name
# but reject newer flags/config. Read the module version embedded by the Go
# linker and require the exact CI pin.
# ---------------------------------------------------------------------------

MOCKERY_VERSION     := v3.5.0
GOLANGCI_VERSION    := v2.11.4
GOVULNCHECK_VERSION := v1.6.0
COVERCHECK_VERSION  := v0.2.0
LINKCHECK_VERSION   := v0.1.2

define require-go-tool
@tool_bin="$$(command -v "$(1)" 2>/dev/null)" || { \
	echo "ERROR: '$(1)' not found in PATH. Run 'make tools' (installs CI-pinned versions)."; \
	exit 1; \
}; \
actual_version="$$($(GO) version -m "$$tool_bin" 2>/dev/null | \
	awk '$$1 == "mod" && $$2 == "$(2)" { print $$3; exit }')"; \
if [ "$$actual_version" != "$(3)" ]; then \
	echo "ERROR: '$(1)' version $${actual_version:-unknown} found at $$tool_bin; CI requires $(3). Run 'make tools'."; \
	exit 1; \
fi
endef

define require-command
@command -v "$(1)" >/dev/null 2>&1 || { \
	echo "ERROR: '$(1)' not found in PATH. $(2)"; \
	exit 1; \
}
endef

tools:
	$(GO) install github.com/vektra/mockery/v3@$(MOCKERY_VERSION)
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/$(GOLANGCI_VERSION)/install.sh \
		| sh -s -- -b "$$($(GO) env GOPATH)/bin" $(GOLANGCI_VERSION)
	$(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	$(GO) install github.com/dr-dobermann/covercheck/cmd/covercheck@$(COVERCHECK_VERSION)
	$(GO) install github.com/dr-dobermann/linkcheck/cmd/linkcheck@$(LINKCHECK_VERSION)
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
	$(call require-go-tool,golangci-lint,github.com/golangci/golangci-lint/v2,$(GOLANGCI_VERSION))
	golangci-lint run --timeout=10m cmd/... internal/... pkg/...
.PHONY: lint

lint_fix:
	$(call require-go-tool,golangci-lint,github.com/golangci/golangci-lint/v2,$(GOLANGCI_VERSION))
	golangci-lint run --timeout=10m --fix cmd/... internal/... pkg/...
.PHONY: lint_fix

lint_all:
	$(call require-go-tool,golangci-lint,github.com/golangci/golangci-lint/v2,$(GOLANGCI_VERSION))
	golangci-lint run --timeout=10m ./...
.PHONY: lint_all

# Mocks are committed under generated/ (FIX-023), so `go test` runs directly —
# no mockery pre-step. Regenerate with `make gen_mock_files` when an interface
# changes. COVER_PACKAGES keeps generated packages and every examples/ package
# out of the coverage numbers.
test:
	$(GO) test -v -count=1 -cover $(COVER_PACKAGES)
.PHONY: test

test_coverage:
	$(GO) test -v -count=1 -coverprofile=c.out $(COVER_PACKAGES)
	go tool cover -html=c.out
	rm c.out
.PHONY: test_coverage

# BPMN_SPEC_SHA is the tree hash of the vendored spec extract the tag is cut
# against. SAD-001 §14 requires each released version to be pinned to a
# BPMN-spec snapshot "so conformance claims are reproducible" — the extract is
# vendored and it changes (a §10.3.4.1 erratum was corrected in it), so
# "v0.11.0 implements the element set" is ambiguous without saying WHICH
# snapshot it was checked against. Recording it in the tag object itself makes
# the pin automatic rather than a step someone has to remember.
BPMN_SPEC_SHA = $(shell git rev-parse HEAD:docs/bpmn-spec)

tag:
	@git tag -a ${VERSION} -m "version ${VERSION}" -m "bpmn-spec snapshot: ${BPMN_SPEC_SHA}"
	@echo "Tag ${VERSION} created locally, pinned to bpmn-spec ${BPMN_SPEC_SHA}."
	@echo "Push manually: git push origin ${VERSION}"
.PHONY: tag

clear:
	rm -rf ./bin/
.PHONY: clear

# ---------------------------------------------------------------------------
# Disposable postgres for the adapters/postgres tests (SRD-078 FR-10): a
# plain docker container by decision — no testcontainers dependency. The
# DSN below is what the tests read from GOBPM_PG_TEST_DSN; CI provides the
# same database via a `services: postgres` container instead.
PG_CONTAINER = gobpm-pg-test
PG_PORT = 5499
PG_PASSWORD = gobpm-test
PG_IMAGE = postgres:17-alpine
PG_TEST_DSN = postgres://postgres:$(PG_PASSWORD)@localhost:$(PG_PORT)/postgres?sslmode=disable

pg-up:
	$(call require-command,docker)
	docker run -d --rm --name $(PG_CONTAINER) \
		-e POSTGRES_PASSWORD=$(PG_PASSWORD) -p $(PG_PORT):5432 $(PG_IMAGE)
	@until docker exec $(PG_CONTAINER) pg_isready -U postgres -q; do \
		sleep 0.5; done
	@echo "postgres is up — run the gated tests with:"
	@echo "  export GOBPM_PG_TEST_DSN='$(PG_TEST_DSN)'"
.PHONY: pg-up

pg-down:
	$(call require-command,docker)
	docker stop $(PG_CONTAINER)
.PHONY: pg-down

# Regenerate the committed mocks (FIX-023) — run when a mocked interface
# changes, then commit generated/. No `go mod tidy`: committed mocks add no
# deps (testify is already required), and tidy-check-all guards go.mod/go.sum
# separately, so tidy stays off the mock path (it was mutating the tree).
gen_mock_files:
	$(call require-go-tool,mockery,github.com/vektra/mockery/v3,$(MOCKERY_VERSION))
	rm -rf generated/
	mockery
.PHONY: gen_mock_files

# CI drift-guard: regenerate the mocks and fail if the committed tree differs
# from what the current interfaces produce (a changed interface not regenerated
# + committed). Deterministic output + a pinned mockery make git diff a reliable
# signal.
# Documentation link gate (FIX-034 §3.2.4): every RELATIVE Markdown link must
# resolve to a file that exists. Blocking, because the 78 dead references
# FIX-031 swept up accumulated precisely because nothing failed. Offline and
# repository-local: the checker is built from this repo, so it adds no tool to
# install and no network dependency that could redden the gate for reasons
# unrelated to the change. External URLs are deliberately out of scope.
link-check:
	$(call require-go-tool,linkcheck,github.com/dr-dobermann/linkcheck,$(LINKCHECK_VERSION))
	linkcheck -root .
.PHONY: link-check

mock-check:
	$(call require-go-tool,mockery,github.com/vektra/mockery/v3,$(MOCKERY_VERSION))
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
		(cd $$dir && GOMAXPROCS=$(TEST_CPUS) $(GO) test -race -count=1 -coverprofile=coverage.txt $(COVER_PACKAGES)) || exit 1; \
		echo "::endgroup::"; \
	done
.PHONY: test-all

lint-all-modules:
	$(call require-go-tool,golangci-lint,github.com/golangci/golangci-lint/v2,$(GOLANGCI_VERSION))
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
	$(call require-go-tool,govulncheck,golang.org/x/vuln,$(GOVULNCHECK_VERSION))
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
# below COVER_MIN. Consumes the per-module coverage.txt files test-all produces
# — run `make test-all` first, or use `make ci` which orders them. Judges only
# changed lines, so the untouched-code backlog never blocks it.
cover-check:
	$(call require-go-tool,covercheck,github.com/dr-dobermann/covercheck,$(COVERCHECK_VERSION))
	covercheck -min $(COVER_MIN) -base $(COVER_BASE) \
		-exclude-lines '$(COVER_EXCLUDE)' \
		-exclude-paths '^(generated|examples)/' \
		-profiles $(COVER_PROFILES)
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

# A future example that genuinely blocks on stdin goes here (FIX-029 §5);
# empty today — consinp degrades gracefully on EOF, and the run loop closes
# stdin so a read gets EOF, never a terminal hang.
EXAMPLE_RUN_SKIP :=
# Generous per-example ceiling: the slowest (timer-driven) examples finish
# well under it; a hang is cut at the ceiling with GNU timeout's exit 124.
EXAMPLE_RUN_TIMEOUT := 90s
# GNU coreutils installs this command as `timeout` on Linux and `gtimeout` on
# macOS/Homebrew. Prefer the native name, then the Homebrew-prefixed fallback;
# retain `timeout` as the final value so the guard below gives a useful error.
EXAMPLE_TIMEOUT ?= $(shell command -v timeout 2>/dev/null || command -v gtimeout 2>/dev/null || printf '%s' timeout)

# run-examples executes every example module end-to-end (FIX-029): a runtime
# regression — deadlock, panic, model drift — fails the gate that `go build`
# alone kept green (the FIX-002 class). Stdout is discarded (the examples
# narrate); stderr stays visible inside the group fold.
run-examples:
	$(call require-command,$(EXAMPLE_TIMEOUT),On macOS install GNU coreutils with 'brew install coreutils' (provides gtimeout).)
	@set -e; for dir in $(filter-out $(EXAMPLE_RUN_SKIP),$(EXAMPLE_MODULES)); do \
		echo "::group::run $$dir"; \
		(cd $$dir && "$(EXAMPLE_TIMEOUT)" $(EXAMPLE_RUN_TIMEOUT) $(GO) run . < /dev/null > /dev/null) || exit 1; \
		echo "::endgroup::"; \
	done
.PHONY: run-examples

# The examples sweep: tidy + lint + build + run over every examples/* module.
# No per-example test loop (the examples carry no tests — the run step
# executes each `main` end-to-end instead, FIX-029) and no per-example
# govulncheck (each example consumes the library through `replace ../..`, so
# the core `vuln` scan already covers the shared dependency graph; scanning it
# 35 more times added minutes of CI for no new signal). Runs as CI's parallel
# non-blocking job AND inside the local `make ci`.
ci-examples:
	@$(MAKE) tidy-check-all lint-all-modules build-all MODULES="$(EXAMPLE_MODULES)"
	@$(MAKE) run-examples
.PHONY: ci-examples

# examples-module-check guards the run sweep's completeness: an example
# directory without a go.mod is invisible to EXAMPLE_MODULES, so it is built
# but never executed. It lives in the REQUIRED core gate rather than the
# non-blocking examples job precisely because a regression here is a hole in
# THAT job — a guard in the half that can go red unnoticed guards nothing.
.PHONY: examples-module-check
examples-module-check:
	@missing=""; \
	for d in $(EXAMPLE_DIRS); do \
		[ -f "$$d/go.mod" ] || missing="$$missing $$d"; \
	done; \
	if [ -n "$$missing" ]; then \
		echo "example directories without a go.mod — these are NEVER run by 'make run-examples':"; \
		for d in $$missing; do echo "    $$d"; done; \
		echo "each example is its own module (SAD-001 §9); add a go.mod with:"; \
		echo "    replace github.com/dr-dobermann/gobpm => ../.."; \
		exit 1; \
	fi; \
	echo "example-module check: all $(words $(EXAMPLE_DIRS)) example directories are modules"

# The core gate — everything the REQUIRED CI job runs, in the same order.
ci-core: mock-check link-check examples-module-check tidy-check-core lint-core build-core consumer-smoke test-core cover-check vuln-core
.PHONY: ci-core

# Umbrella target that runs the full local-equivalent of CI (BOTH CI jobs).
# Use this before pushing to catch regressions before GitHub runs them.
# test-core writes coverage.txt; cover-check consumes it (single test run).
ci: ci-core ci-examples
.PHONY: ci
