PLATFORM_TAG := $(shell uname -s | tr '[:upper:]' '[:lower:]')-$(shell uname -m | tr '[:upper:]' '[:lower:]')
TOOLS_ROOT := $(CURDIR)/.tooling
TOOLS_DIR := $(TOOLS_ROOT)/$(PLATFORM_TAG)
GO_VERSION ?= 1.24.1
GOLANGCI_LINT_VERSION ?= 1.64.8
ZIG_VERSION ?= 0.15.2
GHOSTTY_VT_GHOSTTY_COMMIT ?= bebca84668947bfc92b9a30ed58712e1c34eee1d
GO := $(TOOLS_DIR)/go/$(GO_VERSION)/bin/go
GOLANGCI_LINT := $(TOOLS_DIR)/bin/golangci-lint
ZIG := $(TOOLS_DIR)/zig/$(ZIG_VERSION)/zig

export PATH := $(TOOLS_DIR)/go/$(GO_VERSION)/bin:$(TOOLS_DIR)/bin:$(PATH)
export GOMODCACHE := $(TOOLS_DIR)/gopath/pkg/mod
export GOCACHE := $(TOOLS_DIR)/gocache
export GOLANGCI_LINT_CACHE := $(TOOLS_DIR)/golangci-lint-cache

.PHONY: init ghostty-vt fmt lint test build run tui smoke package package-formula verify clean

PACKAGE_VERSION ?= 0.0.0-dev
RELEASE_TAG ?= v$(PACKAGE_VERSION)
RELEASE_REPO ?= brianrackle/codelima
DIST_DIR ?= $(CURDIR)/dist
FORMULA_OUTPUT ?= $(DIST_DIR)/codelima.rb

init:
	./scripts/install_go.sh $(GO_VERSION) $(TOOLS_DIR) $(CURDIR)/tmp
	./scripts/install_golangci_lint.sh $(GOLANGCI_LINT_VERSION) $(TOOLS_DIR) $(CURDIR)/tmp
	./scripts/install_zig.sh $(ZIG_VERSION) $(TOOLS_DIR) $(CURDIR)/tmp
	./scripts/install_ghostty_vt.sh $(GHOSTTY_VT_GHOSTTY_COMMIT) $(ZIG) $(TOOLS_DIR) $(CURDIR)/tmp
	$(GO) mod download

ghostty-vt:
	./scripts/install_zig.sh $(ZIG_VERSION) $(TOOLS_DIR) $(CURDIR)/tmp
	./scripts/install_ghostty_vt.sh $(GHOSTTY_VT_GHOSTTY_COMMIT) $(ZIG) $(TOOLS_DIR) $(CURDIR)/tmp

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

tui: build
	./bin/codelima $(ARGS)

smoke: build
	./scripts/smoke_3_layers.sh

package: init
	./scripts/package_release.sh $(PACKAGE_VERSION) $(GO) $(TOOLS_DIR) $(DIST_DIR)

package-formula: init
	./scripts/render_homebrew_formula.sh $(RELEASE_REPO) $(RELEASE_TAG) $(DIST_DIR) $(FORMULA_OUTPUT) $(GO)

verify: fmt lint test build

clean:
	rm -rf bin dist
