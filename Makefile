TOOLS_DIR := $(CURDIR)/.tooling
GO_VERSION ?= 1.24.1
GOLANGCI_LINT_VERSION ?= 1.64.8
GO := $(TOOLS_DIR)/go/$(GO_VERSION)/bin/go
GOLANGCI_LINT := $(TOOLS_DIR)/bin/golangci-lint

export PATH := $(TOOLS_DIR)/go/$(GO_VERSION)/bin:$(TOOLS_DIR)/bin:$(PATH)
export GOMODCACHE := $(TOOLS_DIR)/gopath/pkg/mod
export GOCACHE := $(TOOLS_DIR)/gocache

.PHONY: init fmt lint test build run smoke verify clean

init:
	./scripts/install_go.sh $(GO_VERSION) $(TOOLS_DIR)
	./scripts/install_golangci_lint.sh $(GOLANGCI_LINT_VERSION) $(TOOLS_DIR)
	$(GO) mod download

fmt: init
	$(GO) fmt ./...

lint: init
	$(GOLANGCI_LINT) run ./...

test: init
	$(GO) test ./...

build: init
	mkdir -p bin
	$(GO) build -o bin/codelima ./cmd/codelima

run: build
	./bin/codelima $(ARGS)

smoke: build
	./scripts/smoke_3_layers.sh

verify: fmt lint test build

clean:
	rm -rf bin
