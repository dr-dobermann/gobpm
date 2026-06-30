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
COVER_EXCLUDE ?= \.(logger|Logger\(\))\.(Debug|Info|Warn|Error)\(

# All Go modules in the monorepo (each with its own go.mod).
# Discovered dynamically so adding a new module needs no Makefile edit.
MODULES := $(shell /usr/bin/find . -name go.mod -not -path './.git/*' -exec dirname {} \;)

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
GOVULNCHECK_VERSION := latest
COVERCHECK_VERSION  := v0.1.2

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

build:
	${GO} build -o ./bin/ "./..."

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

test: gen_mock_files
	go test -v -count=1 -cover ./...
.PHONY: test

test_coverage: gen_mock_files
	go test -v -count=1 -coverprofile=c.out ./...
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

gen_mock_files:
	$(call require-tool,mockery,$(GO) install github.com/vektra/mockery/v3@$(MOCKERY_VERSION))
	rm -rf generated/
	mockery
	go mod tidy
.PHONY: gen_mock_files

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

test-all: gen_mock_files
	@set -e; for dir in $(MODULES); do \
		echo "::group::test $$dir"; \
		if [ "$$dir" = "." ]; then \
			(cd $$dir && $(GO) test -race -count=1 -coverprofile=coverage.txt ./...) || exit 1; \
		else \
			(cd $$dir && $(GO) test -race -count=1 ./...) || exit 1; \
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

tidy-check-all: gen_mock_files
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

# Diff-coverage gate: fail when the lines this change adds/modifies are covered
# below COVER_MIN. Consumes the coverage.txt that test-all produces (root
# module) — run `make test-all` first, or use `make ci` which orders them.
# Judges only changed lines, so the untouched-code backlog never blocks it.
cover-check:
	$(call require-tool,covercheck,$(GO) install github.com/dr-dobermann/covercheck/cmd/covercheck@$(COVERCHECK_VERSION))
	covercheck -min $(COVER_MIN) -base $(COVER_BASE) \
		-exclude-lines '$(COVER_EXCLUDE)' -profiles coverage.txt
.PHONY: cover-check

# Umbrella target that runs the full local-equivalent of CI.
# Use this before pushing to catch regressions before GitHub runs them.
# test-all writes coverage.txt; cover-check consumes it (single test run).
ci: tidy-check-all lint-all-modules build-all test-all cover-check vuln
.PHONY: ci

