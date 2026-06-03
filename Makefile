# Version number
VERSION = $(shell cat .version)

# GO = /mnt/c/Dvl/go/bin/go.exe
GO = go

# DC = docker-compose.exe
DC = docker compose

# All Go modules in the monorepo (each with its own go.mod).
# Discovered dynamically so adding a new module needs no Makefile edit.
MODULES := $(shell /usr/bin/find . -name go.mod -not -path './.git/*' -exec dirname {} \;)

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
	go test -v -cover ./...
.PHONY: test

test_coverage: gen_mock_files
	go test -v -coverprofile=c.out ./...
	go tool cover -html=c.out
	rm c.out
.PHONY: test_coverage

tag:
	@git tag -a ${VERSION} -m "version ${VERSION}"
	@echo "Tag ${VERSION} created locally. Push manually: git push origin ${VERSION}"
.PHONY: tag

clear:
	rm ./bin/*
.PHONY: clear

gen_mock_files:
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
			(cd $$dir && $(GO) test -race -coverprofile=coverage.txt ./...) || exit 1; \
		else \
			(cd $$dir && $(GO) test -race ./...) || exit 1; \
		fi; \
		echo "::endgroup::"; \
	done
.PHONY: test-all

lint-all-modules:
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
	govulncheck ./...
.PHONY: vuln

# Umbrella target that runs the full local-equivalent of CI.
# Use this before pushing to catch regressions before GitHub runs them.
ci: tidy-check-all lint-all-modules build-all test-all vuln
.PHONY: ci

