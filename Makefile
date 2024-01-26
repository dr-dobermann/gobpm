# Version number
VERSION = $(shell cat .version)

PWD_WIN="//c/wrk/development/go/src/gobpm"

# GO = /mnt/c/Dvl/go/bin/go.exe
GO = go

# DC = docker-compose.exe
DC = docker compose

build:
	${GO} build -o ./bin/ "./..." 

.PHONY: update_modules lint wlint tag clear

update_modules:
	@go get -u ./...
	@go mod tidy

lint:
	golangci-lint run -v ./...

wlint:
	docker run --rm -v //c/wrk/development/go/src/gobpm://cmd -w //cmd golangci/golangci-lint golangci-lint run -v

dlint:
	docker run --rm -v /Users/dober/wrk/development/go/src/gobpm://cmd -w //cmd golangci/golangci-lint golangci-lint run -v

# rundb:
# 	${DC} -f ./stand/db/docker-compose.yaml build
# 	${DC} -f ./stand/db/docker-compose.yaml up --detach --wait --remove-orphans

tag: 
	@git tag -a ${VERSION} -m "version ${VERSION}"
	@git push origin --tags

clear:
	rm ./bin/modelsrv*
