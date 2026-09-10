BINARY  := sshm
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.DEFAULT_GOAL := build

## setup: fetch dependencies (needs network, once)
.PHONY: setup
setup:
	go mod tidy

## build: compile ./sshm for this machine
.PHONY: build
build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/sshm

## install: build and install into $(go env GOPATH)/bin
.PHONY: install
install:
	go install -trimpath -ldflags "$(LDFLAGS)" ./cmd/sshm

## test: run the full test suite with the race detector
.PHONY: test
test:
	go test -race ./...

## bench: run the search and matcher benchmarks
.PHONY: bench
bench:
	go test -run=XXX -bench=. -benchmem ./internal/search/ ./internal/fuzzy/

## check: vet, test and confirm the binary starts fast
.PHONY: check
check: vet test build startup

.PHONY: vet
vet:
	go vet ./...
	gofmt -l . | tee /dev/stderr | (! read)

## startup: measure cold start, which is the whole point of the tool
.PHONY: startup
startup: build
	@echo "cold start (sshm list, 20 runs):"
	@start=$$(date +%s%N); \
	for i in $$(seq 1 20); do ./$(BINARY) list >/dev/null 2>&1 || true; done; \
	end=$$(date +%s%N); \
	echo "  $$(( (end - start) / 20000000 )).$$(( ((end - start) / 2000000) % 10 ))ms per run"

## clean: remove build output
.PHONY: clean
clean:
	rm -f $(BINARY)

## help: list targets
.PHONY: help
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //'
