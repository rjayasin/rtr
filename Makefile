BINARY := rtr
GO     := go
ARGS   ?=

.DEFAULT_GOAL := run

## run: compile and launch rtr (pass flags with ARGS="--config ...")
.PHONY: run
run: build
	./$(BINARY) $(ARGS)

## build: compile the binary
.PHONY: build
build:
	$(GO) build -o $(BINARY) .

## test: run the test suite
.PHONY: test
test:
	$(GO) test ./...

## vet: run go vet
.PHONY: vet
vet:
	$(GO) vet ./...

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
