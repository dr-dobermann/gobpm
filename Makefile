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
	golangci-lint run -v ./...
.PHONY: lint

# wlint:
# 	docker run --rm -v //c/wrk/development/go/src/gobpm://cmd -w //cmd golangci/golangci-lint golangci-lint run -v

# rundb:
# 	${DC} -f ./stand/db/docker-compose.yaml build
# 	${DC} -f ./stand/db/docker-compose.yaml up --detach --wait --remove-orphans

test:
	go test -v -cover ./...
.PHONY: test

test_coverage:
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
	rm -rf .generated/
	mockery
	go mod tidy
.PHONY: gen_mock_files
