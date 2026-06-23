BINARY  := rtr
GO      := go
ARGS    ?=
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.DEFAULT_GOAL := run

## run: compile and launch rtr (pass flags with ARGS="--config ...")
.PHONY: run
run: build
	./$(BINARY) $(ARGS)

## build: compile the binary
.PHONY: build
build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BINARY) .

## test: run the test suite (with the race detector, matching CI)
.PHONY: test
test:
	$(GO) test -race ./...

## vet: run go vet
.PHONY: vet
vet:
	$(GO) vet ./...

## lint: run staticcheck with all checks (including ST stylechecks)
.PHONY: lint
lint:
	$(GO) run honnef.co/go/tools/cmd/staticcheck@latest -checks=all ./...

## fmt: format all Go sources
.PHONY: fmt
fmt:
	gofmt -w .

## install: install rtr to GOBIN/PATH
.PHONY: install
install:
	$(GO) install .

## clean: remove the built binary
.PHONY: clean
clean:
	rm -f $(BINARY)

## help: list available targets
.PHONY: help
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //'
