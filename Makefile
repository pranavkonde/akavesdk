SHELL := /bin/bash

.PHONY: build
build:
	@go build -o bin/akavecli ./cmd/akavecli/

.PHONY: test
test:
	@go test -v -race -count=1 ./...

.PHONY: test_quick
test_quick:
	@go test -v ./...

.PHONY: check
check: # for local usage
	@golangci-lint run ./... 
	@linelint .

.PHONY: lint_fix
lint_fix: # for local usage
	@golangci-lint run --fix ./...
	@linelint -a .

.PHONY: fix
fix: lint_fix

.PHONY: gen
gen: # for local usage
	@go generate ./...

.PHONY: install_tools
install_tools: # for local usage
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.56.2
	@go install github.com/fernandrone/linelint@0.0.6
