# Version number
VERSION = $(shell cat .version)

# GO = /mnt/c/Dvl/go/bin/go.exe
GO = go

# DC = docker-compose.exe
DC = docker compose

build:
	${GO} build -o ./bin/ "./..." 

.PHONY: update_modules lint tag clear test cover

update_modules:
	@go get -u ./...
	@go mod tidy

lint:
	golangci-lint run -v ./...

# wlint:
# 	docker run --rm -v //c/wrk/development/go/src/gobpm://cmd -w //cmd golangci/golangci-lint golangci-lint run -v

# rundb:
# 	${DC} -f ./stand/db/docker-compose.yaml build
# 	${DC} -f ./stand/db/docker-compose.yaml up --detach --wait --remove-orphans

test:
	go test -v -cover ./...

cover:
	go test -v -coverprofile=c.out ./...
	go tool cover -html=c.out
	rm c.out

tag: 
	@git tag -a ${VERSION} -m "version ${VERSION}"
	@git push origin --tags

clear:
	rm ./bin/*

.PHONY: gen_mock_files
gen_mock_files:
	mockery
	go mod tidy
