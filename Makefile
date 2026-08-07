PLATFORM_TAG := $(shell uname -s | tr '[:upper:]' '[:lower:]')-$(shell uname -m | tr '[:upper:]' '[:lower:]')
TOOLS_ROOT := $(CURDIR)/.tooling
TOOLS_DIR := $(TOOLS_ROOT)/$(PLATFORM_TAG)
BIN_ROOT := $(CURDIR)/bin
BIN_DIR := $(BIN_ROOT)/$(PLATFORM_TAG)
CODELIMA_BIN := $(BIN_DIR)/codelima
CODELIMA_RENDERER_BIN := $(BIN_DIR)/codelima-renderer-worker
CODELIMA_COMPAT_BIN := $(BIN_ROOT)/codelima
GO_VERSION ?= 1.24.1
GOPLS_VERSION ?= v0.18.1
GOLANGCI_LINT_VERSION ?= 1.64.8
ZIG_VERSION ?= 0.15.2
GHOSTTY_VT_GHOSTTY_COMMIT ?= ae52f97dcac558735cfa916ea3965f247e5c6e9e
GO := $(TOOLS_DIR)/go/$(GO_VERSION)/bin/go
GOFMT := $(TOOLS_DIR)/go/$(GO_VERSION)/bin/gofmt
GOPLS := $(TOOLS_DIR)/bin/gopls
GOLANGCI_LINT := $(TOOLS_DIR)/bin/golangci-lint
ZIG := $(TOOLS_DIR)/zig/$(ZIG_VERSION)/zig

ifeq ($(origin CC),default)
  ifeq ($(shell command -v cc 2>/dev/null),)
    CC := $(ZIG) cc
  endif
endif

export PATH := $(TOOLS_DIR)/go/$(GO_VERSION)/bin:$(TOOLS_DIR)/bin:$(PATH)
export GOMODCACHE := $(TOOLS_DIR)/gopath/pkg/mod
export GOCACHE := $(TOOLS_DIR)/gocache
export GOLANGCI_LINT_CACHE := $(TOOLS_DIR)/golangci-lint-cache
export CGO_ENABLED := 1
export CC

.PHONY: init ghostty-vt gopls tidy fmt fmt-check lint test test-race test-integration test-lima-native build run tui smoke diagnose-terminal-freeze package package-formula verify clean clean-all

# Source roots handed to gofmt. Directories (not the ./... package pattern) so
# build-tag-gated files such as tests/daemon_integration_test.go are covered;
# .tooling/, tmp/ and the module cache are deliberately outside this list.
FMT_DIRS := cmd internal tests

PACKAGE_VERSION ?= 0.0.0-dev
VERSION_LDFLAGS := -X github.com/brianrackle/codelima/internal/codelima.Version=$(PACKAGE_VERSION)
RELEASE_TAG ?= v$(PACKAGE_VERSION)
RELEASE_REPO ?= brianrackle/codelima
DIST_DIR ?= $(CURDIR)/dist
FORMULA_OUTPUT ?= $(DIST_DIR)/codelima.rb
INTEGRATION_TMP ?= $(CURDIR)/tmp/i
GOPLS_ARGS ?= version

# This was pinned to 1 because ./internal/codelima could not survive a parallel
# run: daemon.Server bracketed its socket bind with a process-global
# syscall.Umask(0o177), so any concurrent t.TempDir() got a 0600 base directory
# (0700 &^ 0177) and every write beneath it failed with EACCES. A 2026-08-07
# probe at -p 4 -parallel 4 failed 3/3 (129, 130 and 1 failures, all
# "permission denied" on a just-created path). daemon/server.go now binds via
# listenPrivate (chmod after Listen, inside a verified 0700 directory) and holds
# no global umask, and the same probe passes 3/3 (~19s vs ~27s serial), so the
# default is 4. Full history: git log for this line and for listenPrivate.
GO_TEST_PARALLEL ?= 4
GO_RACE_TEST_PARALLEL ?= 1
DIAG_ARGS ?=

init:
	./scripts/install_go.sh $(GO_VERSION) $(TOOLS_DIR) $(CURDIR)/tmp
	./scripts/install_zig.sh $(ZIG_VERSION) $(TOOLS_DIR) $(CURDIR)/tmp
	./scripts/install_gopls.sh $(GOPLS_VERSION) $(GO) $(TOOLS_DIR) $(CURDIR)/tmp
	./scripts/install_golangci_lint.sh $(GOLANGCI_LINT_VERSION) $(TOOLS_DIR) $(CURDIR)/tmp
	./scripts/install_ghostty_vt.sh $(GHOSTTY_VT_GHOSTTY_COMMIT) $(ZIG) $(TOOLS_DIR) $(CURDIR)/tmp
	$(GO) mod download

ghostty-vt:
	./scripts/install_zig.sh $(ZIG_VERSION) $(TOOLS_DIR) $(CURDIR)/tmp
	./scripts/install_ghostty_vt.sh $(GHOSTTY_VT_GHOSTTY_COMMIT) $(ZIG) $(TOOLS_DIR) $(CURDIR)/tmp

gopls: init
	$(GOPLS) $(GOPLS_ARGS)

tidy: init
	$(GO) mod tidy

# fmt rewrites; fmt-check only reports. verify depends on fmt-check so CI fails
# on drift instead of silently reformatting and then building the rewrite.
fmt: init
	$(GOFMT) -l -w $(FMT_DIRS)

fmt-check: init
	@set -eu; \
	drift="$$($(GOFMT) -l $(FMT_DIRS))"; \
	if [ -n "$$drift" ]; then \
		printf 'gofmt drift (run "make fmt"):\n%s\n' "$$drift" >&2; \
		exit 1; \
	fi

lint: init
	$(GOLANGCI_LINT) run ./...

test: init
	$(GO) test -p $(GO_TEST_PARALLEL) -parallel $(GO_TEST_PARALLEL) ./...

test-race: init
	$(GO) test -race -p $(GO_RACE_TEST_PARALLEL) -parallel $(GO_RACE_TEST_PARALLEL) ./...

test-integration: build
	mkdir -p $(INTEGRATION_TMP)
	CODELIMA_TEST_BIN=$(CODELIMA_BIN) CODELIMA_TEST_TMP=$(INTEGRATION_TMP) $(GO) test -p $(GO_TEST_PARALLEL) -parallel $(GO_TEST_PARALLEL) -tags=integration ./tests
	rm -rf $(INTEGRATION_TMP)

test-lima-native: init
	@set -eu; native_lima_tmp='$(CURDIR)/tmp/native-lima'; \
	trap 'rm -rf "$$native_lima_tmp"' EXIT; \
	mkdir -p "$$native_lima_tmp"; \
	CODELIMA_NATIVE_LIMA=1 TMPDIR="$$native_lima_tmp" $(GO) test -run '^TestNativeLimaTemplateValidation$$' ./internal/codelima

build: init
	mkdir -p $(BIN_DIR)
	$(GO) build -ldflags "$(VERSION_LDFLAGS)" -o $(CODELIMA_BIN) ./cmd/codelima
	$(GO) build -ldflags "$(VERSION_LDFLAGS)" -o $(CODELIMA_RENDERER_BIN) ./cmd/codelima-renderer-worker
	cp scripts/codelima_dispatch.sh $(BIN_ROOT)/.codelima-dispatch.tmp
	chmod 0755 $(BIN_ROOT)/.codelima-dispatch.tmp
	mv -f $(BIN_ROOT)/.codelima-dispatch.tmp $(CODELIMA_COMPAT_BIN)

run: build
	$(CODELIMA_BIN) $(ARGS)

tui: build
	$(CODELIMA_BIN) $(ARGS)

smoke: build
	CODELIMA_BIN=$(CODELIMA_BIN) /bin/sh ./scripts/smoke_3_layers.sh

diagnose-terminal-freeze:
	/bin/sh ./.agents/skills/diagnose-codelima-terminal-freezes/scripts/capture.sh $(DIAG_ARGS)

package: init
	/bin/sh ./scripts/package_release.sh $(PACKAGE_VERSION) $(GO) $(TOOLS_DIR) $(DIST_DIR) $(CODELIMA_BIN) $(PLATFORM_TAG) $(CODELIMA_RENDERER_BIN)

package-formula: init
	./scripts/render_homebrew_formula.sh $(RELEASE_REPO) $(RELEASE_TAG) $(DIST_DIR) $(FORMULA_OUTPUT) $(GO)

verify: fmt-check lint test build

clean:
	rm -rf $(BIN_DIR) $(DIST_DIR)

clean-all:
	rm -rf $(BIN_ROOT) $(DIST_DIR)
