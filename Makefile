# Version number
VERSION = $(shell cat .version)

# GO = /mnt/c/Dvl/go/bin/go.exe
GO = go

# DC = docker-compose.exe
DC = docker compose

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

# wlint:
# 	docker run --rm -v //c/wrk/development/go/src/gobpm://cmd -w //cmd golangci/golangci-lint golangci-lint run -v

# rundb:
# 	${DC} -f ./stand/db/docker-compose.yaml build
# 	${DC} -f ./stand/db/docker-compose.yaml up --detach --wait --remove-orphans

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
	@git push origin --tags
.PHONY: tag

clear:
	rm ./bin/*
.PHONY: clear

gen_mock_files:
	rm -rf generated/
	mockery
	go mod tidy
.PHONY: gen_mock_files
