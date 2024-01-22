# Version number
VERSION = $(shell cat .version)

# GO = /mnt/c/Dvl/go/bin/go.exe
GO = go

# DC = docker-compose.exe
DC = docker compose

build:
	${GO} build -o ./bin/ "./..." 

.PHONY: update_modules lint tag clear

update_modules:
	@go get -u ./...
	@go mod tidy

lint:
	golangci-lint run ./...
#   docker run --rm -v ./:/cmd -w /cmd golangci/golangci-lint golangci-lint run -v

# rundb:
# 	${DC} -f ./stand/db/docker-compose.yaml build
# 	${DC} -f ./stand/db/docker-compose.yaml up --detach --wait --remove-orphans

tag: 
	@git tag -a ${VERSION} -m "version ${VERSION}"
	@git push origin --tags

clear:
	rm ./bin/modelsrv*
