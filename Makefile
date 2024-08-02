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

.PHONY: post_linter_checks 
post_linter_checks:
	@go run ci/check-imports/main.go
	@go run ci/check-copyright/main.go

.PHONY: check
check: # for local usage
	@golangci-lint run ./... 
	@$(MAKE) -s post_linter_checks
	@linelint .

.PHONY: lint_fix
lint_fix: # for local usage
	@golangci-lint run --fix ./...
	@linelint -a .

.PHONY: copyright_fix
copyright_fix: # for local usage
	@go run ci/check-copyright/main.go -fix=true

.PHONY: fix
fix: lint_fix copyright_fix

.PHONY: clean
clean: # for local usage
	@rm -rf bin/*

.PHONY: install_tools
install_tools: # for local usage
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.56.2
	@go install github.com/fernandrone/linelint@0.0.6
