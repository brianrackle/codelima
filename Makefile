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
MISE_VERSION ?= 2026.10.18
VAXIS_CGO_LDFLAGS := $(CGO_LDFLAGS)
ifeq ($(PLATFORM_TAG),linux-aarch64)
# Upstream's Go 1.25 external link defaults to gold; minimal Linux guests
# provide BFD. The last compiler flag selects the installed system linker.
VAXIS_CGO_LDFLAGS += -fuse-ld=bfd
endif
GOLANGCI_LINT_VERSION ?= 1.64.8
ZIG_VERSION ?= 0.16.0
PKGCONF_VERSION ?= 2.5.1
GHOSTTY_VT_GHOSTTY_COMMIT ?= 82232ecde55405559dec29c5466cb9e39938cb41
GHOSTTY_VT_FEATURES ?= -all,+formatter,+selection,+render-state,+input-encode,+color,+grid-introspection,+snapshot,+search,+kitty-graphics
GHOSTTY_VT_OPTIMIZE ?= ReleaseSmall
GHOSTTY_VT_TARGET ?= native
GHOSTTY_VT_CPU ?= baseline
GHOSTTY_VT_BUILD_JOBS ?= 4
GHOSTTY_VT_TEST_JOBS ?= 1
GHOSTTY_VT_UNIT_FILTER ?=
GHOSTTY_TEST_FILTER ?= Ghostty
GHOSTTY_VT_CURRENT := $(TOOLS_DIR)/ghostty-vt/current
GO := $(TOOLS_DIR)/go/$(GO_VERSION)/bin/go
GOFMT := $(TOOLS_DIR)/go/$(GO_VERSION)/bin/gofmt
GOPLS := $(TOOLS_DIR)/bin/gopls
GOLANGCI_LINT := $(TOOLS_DIR)/bin/golangci-lint
ZIG := $(TOOLS_DIR)/zig/$(ZIG_VERSION)/zig
PKG_CONFIG := $(TOOLS_DIR)/bin/pkg-config

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
export PKG_CONFIG
export CODELIMA_TOOLING_GO := $(GO)
export ZIG_VERSION PKGCONF_VERSION
export GHOSTTY_VT_FEATURES GHOSTTY_VT_OPTIMIZE GHOSTTY_VT_TARGET GHOSTTY_VT_CPU GHOSTTY_VT_BUILD_JOBS
export PKG_CONFIG_PATH := $(GHOSTTY_VT_CURRENT)/share/pkgconfig:$(PKG_CONFIG_PATH)

.PHONY: init pkg-config ghostty-vt test-pkgconf test-ghostty-vt test-ghostty-vt-build test-ghostty-vt-schema test-ghostty-bridge test-ghostty-adapter benchmark-ghostty-compression test-renderer-boundary test-installers test-package gopls tidy fmt fmt-check lint test test-race test-integration test-lima-native build run tui smoke diagnose-terminal-freeze package package-formula verify clean clean-all

# Source roots handed to gofmt. Directories (not the ./... package pattern) so
# build-tag-gated files such as tests/daemon_integration_test.go are covered;
# .tooling/, tmp/ and the module cache are deliberately outside this list.
FMT_DIRS := cmd internal tests scripts/tooling_lock.go scripts/renderer_build.go
FMT_DIRS += third_party/vaxis/encoded_kitty_image.go third_party/vaxis/encoded_kitty_image_test.go third_party/vaxis/image.go third_party/vaxis/vaxis.go
FMT_DIRS += third_party/vaxis/render_fallback_width_test.go
FMT_DIRS += third_party/vaxis/widgets/textinput

PACKAGE_VERSION ?= 0.0.0-dev
RENDERER_LDFLAGS = $(if $(wildcard $(GHOSTTY_VT_CURRENT)/.native-build-id),-X github.com/brianrackle/codelima/internal/rendererbuild.identityOverride=$(shell cat $(GHOSTTY_VT_CURRENT)/.native-build-id))
VERSION_LDFLAGS = -X github.com/brianrackle/codelima/internal/codelima.Version=$(PACKAGE_VERSION) $(RENDERER_LDFLAGS)
RELEASE_TAG ?= v$(PACKAGE_VERSION)
export RELEASE_TAG
RELEASE_REPO ?= brianrackle/codelima
DIST_DIR ?= $(CURDIR)/dist
FORMULA_OUTPUT ?= $(DIST_DIR)/codelima.rb
INTEGRATION_TMP ?= $(CURDIR)/tmp/i
GOPLS_ARGS ?= version
TUI_INPUT_TEST_FILTER ?= ^TestTUI(FocusToggle|Tab|TerminalTabKey|HandleKeyOptionShiftT|Shortcut|Search|Dialog|HandleEvent)

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
KVM_ARGS ?= id

.PHONY: init-qemu-guest run-kvm test-qemu-guest
init-qemu-guest:
	bash scripts/install_qemu_guest.sh '$(TOOLS_DIR)' '$(CURDIR)/tmp'

run-kvm:
	bash scripts/with_kvm.sh $(KVM_ARGS)

test-qemu-guest: init-qemu-guest
	@printf '%s\n' '{"execute":"qmp_capabilities"}' '{"execute":"query-kvm"}' '{"execute":"quit"}' | bash scripts/with_kvm.sh '$(TOOLS_DIR)/qemu/bin/qemu-system-aarch64' -machine virt -accel kvm -cpu host -m 256 -nodefaults -display none -S -qmp stdio

.PHONY: go-toolchain release-metadata test-release
go-toolchain:
	./scripts/install_go.sh $(GO_VERSION) $(TOOLS_DIR) $(CURDIR)/tmp

pkg-config: go-toolchain
	./scripts/install_zig.sh $(ZIG_VERSION) $(TOOLS_DIR) $(CURDIR)/tmp
	./scripts/install_pkgconf.sh $(PKGCONF_VERSION) '$(TOOLS_DIR)' '$(CURDIR)/tmp' '$(ZIG)'

init: pkg-config
	./scripts/install_gopls.sh $(GOPLS_VERSION) $(GO) $(TOOLS_DIR) $(CURDIR)/tmp
	./scripts/install_golangci_lint.sh $(GOLANGCI_LINT_VERSION) $(TOOLS_DIR) $(CURDIR)/tmp
	./scripts/install_ghostty_vt.sh $(GHOSTTY_VT_GHOSTTY_COMMIT) $(ZIG) $(TOOLS_DIR) $(CURDIR)/tmp
	$(GO) mod download

ghostty-vt: pkg-config
	./scripts/install_ghostty_vt.sh $(GHOSTTY_VT_GHOSTTY_COMMIT) $(ZIG) $(TOOLS_DIR) $(CURDIR)/tmp

test-pkgconf: pkg-config
	$(GO) test ./internal/release -run Pkgconf -count=1

# Run the upstream tests against the exact patched source installed with the
# archive. Its dependency hashes remain intact, and caches stay project-local.
# Unit execution and compile-only tests require Debug's tracked-pin safety
# instrumentation. Schema/bridge checks use the selected production profile.
test-ghostty-vt test-ghostty-vt-build test-ghostty-vt-schema: ghostty-vt
	cd $(GHOSTTY_VT_CURRENT)/source && ZIG_GLOBAL_CACHE_DIR=$(TOOLS_DIR)/cache/zig-global ZIG_LOCAL_CACHE_DIR=$(TOOLS_DIR)/cache/ghostty-native-tests $(ZIG) build $(patsubst test-ghostty-vt%,test-lib-vt%,$@) -j$(GHOSTTY_VT_TEST_JOBS) -Demit-lib-vt=true -Demit-xcframework=false -Dversion-string=1.3.2-dev+$(GHOSTTY_VT_GHOSTTY_COMMIT) -Dlib-version-string=0.1.0-dev+$(GHOSTTY_VT_GHOSTTY_COMMIT) -Doptimize=$(if $(filter test-ghostty-vt test-ghostty-vt-build,$@),Debug,$(GHOSTTY_VT_OPTIMIZE)) -Dtarget=$(GHOSTTY_VT_TARGET) -Dcpu=$(GHOSTTY_VT_CPU) -Dvt-features=$(GHOSTTY_VT_FEATURES) $(if $(GHOSTTY_VT_UNIT_FILTER),-Dtest-filter='$(GHOSTTY_VT_UNIT_FILTER)')

test-ghostty-bridge: ghostty-vt
	@set -eu; mkdir -p '$(CURDIR)/tmp'; \
	bridge_test_tmp=$$(mktemp -d '$(CURDIR)/tmp/ghostty-bridge.XXXXXX'); \
	trap 'rm -rf "$$bridge_test_tmp"' EXIT; \
	$(CC) -std=c11 -Wall -Wextra -Werror $$('$(PKG_CONFIG)' --cflags libghostty-vt-static) internal/ghostty/testdata/ghostty_bridge_test.c $$('$(PKG_CONFIG)' --libs libghostty-vt-static) -lpthread -lm -o "$$bridge_test_tmp/test"; \
	"$$bridge_test_tmp/test"

benchmark-ghostty-compression: ghostty-vt
	@set -eu; mkdir -p '$(CURDIR)/tmp'; \
	compression_bench_tmp=$$(mktemp -d '$(CURDIR)/tmp/ghostty-compression.XXXXXX'); \
	trap 'rm -rf "$$compression_bench_tmp"' EXIT; \
	$(CC) -std=c11 -O2 -Wall -Wextra -Werror $$('$(PKG_CONFIG)' --cflags libghostty-vt-static) internal/ghostty/testdata/ghostty_compression_bench.c $$('$(PKG_CONFIG)' --libs libghostty-vt-static) -lpthread -lm -o "$$compression_bench_tmp/bench"; \
	"$$compression_bench_tmp/bench" off; \
	"$$compression_bench_tmp/bench" on

test-installers:
	$(GO) test ./internal/release -run Installer

test-renderer-boundary:
	$(GO) test ./internal/release -run 'TestRenderer(NativeDependency|PortablePackages|ApplicationNative)'

.PHONY: test-vaxis-fork
test-vaxis-fork:
	$(GO) test go.rockorager.dev/vaxis/...

.PHONY: test-vaxis-upstream init-mise
init-mise:
	sh scripts/install_mise.sh $(MISE_VERSION) '$(TOOLS_DIR)' '$(CURDIR)/tmp'

test-vaxis-upstream: init-mise
	@set -eu; \
	  mkdir -p '$(CURDIR)/tmp'; \
	  vaxis_check_tmp="$$(mktemp -d '$(CURDIR)/tmp/.vaxis-upstream.XXXXXX')"; \
	  trap 'rm -rf "$$vaxis_check_tmp"' EXIT; \
	  cd third_party/vaxis; \
	  MISE_DATA_DIR='$(TOOLS_DIR)/mise-data' MISE_CACHE_DIR='$(TOOLS_DIR)/mise-cache' \
	  MISE_CONFIG_DIR='$(TOOLS_DIR)/mise-config' MISE_STATE_DIR='$(TOOLS_DIR)/mise-state' \
	  MISE_TRUSTED_CONFIG_PATHS='$(CURDIR)/third_party/vaxis' TMPDIR="$$vaxis_check_tmp" GOMAXPROCS=2 MISE_JOBS=2 \
	  CGO_LDFLAGS='$(VAXIS_CGO_LDFLAGS)' \
	  '$(TOOLS_DIR)/mise/$(MISE_VERSION)/bin/mise' run check

test-ghostty-adapter: ghostty-vt
	$(GO) test -ldflags "$(RENDERER_LDFLAGS)" ./internal/ghostty ./internal/codelima ./internal/terminalstate -run '$(GHOSTTY_TEST_FILTER)' -count=1

# Exercise the actual release pair without replacing development executables.
PACKAGE_SMOKE_TEST = $(GO) test -ldflags "$(RENDERER_LDFLAGS)" -tags=packageintegration ./internal/codelima -run '^TestPackagedStaticRenderer$$' -count=1

test-package: init
	@set -eu; mkdir -p '$(CURDIR)/tmp'; \
	package_test_tmp=$$(mktemp -d '$(CURDIR)/tmp/package-test.XXXXXX'); \
	trap 'rm -rf "$$package_test_tmp"' EXIT; \
	PKG_CONFIG="$$package_test_tmp/unavailable-pkg-config" /bin/sh ./scripts/package_release.sh 0.0.0-package-test '$(GO)' '$(TOOLS_DIR)' "$$package_test_tmp/dist" "$$package_test_tmp/build/codelima" '$(PLATFORM_TAG)' "$$package_test_tmp/build/codelima-renderer-worker"; \
	CODELIMA_PACKAGE_TEST_DIST="$$package_test_tmp/dist" $(PACKAGE_SMOKE_TEST)

.PHONY: test-package-artifact
test-package-artifact: init
	CODELIMA_PACKAGE_TEST_DIST='$(DIST_DIR)' $(PACKAGE_SMOKE_TEST)

gopls: init
	$(GOPLS) $(GOPLS_ARGS)

.PHONY: go-doc
go-doc: init
	$(GO) doc $(GO_DOC_ARGS)

.PHONY: go-get
go-get: init
	$(GO) get $(GO_GET_ARGS)

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
	$(GO) test -ldflags "$(RENDERER_LDFLAGS)" -p $(GO_TEST_PARALLEL) -parallel $(GO_TEST_PARALLEL) ./...

.PHONY: test-tui-input
test-tui-input: init
	$(GO) test -ldflags "$(RENDERER_LDFLAGS)" ./internal/codelima -run '$(TUI_INPUT_TEST_FILTER)' -count=1

.PHONY: test-terminal-attach
test-terminal-attach: init
	$(GO) test -ldflags "$(RENDERER_LDFLAGS)" ./internal/codelima ./internal/ghostty ./internal/terminalstate -run 'Test(Raw|GhosttyAttach|VTBoundary)' -count=1 -timeout=60s

.PHONY: test-host-capacity test-manager-settings test-metrics
.PHONY: test-node-logs
.PHONY: test-node-spec
.PHONY: test-bootstrap-steps
.PHONY: test-node-operations
.PHONY: test-activity
.PHONY: test-create-node
.PHONY: test-template-editor
.PHONY: test-recipes
.PHONY: test-environment-runs
.PHONY: test-dependency-hints
.PHONY: test-system-doctor
.PHONY: test-system-daemon
test-system-daemon: init
	mkdir -p '$(CURDIR)/tmp/.go-work'
	TMPDIR='$(CURDIR)/tmp/.go-work' $(GO) test ./internal/codelima ./internal/codelima/daemon ./internal/diagnosticskill -run '^Test(DiagnoseTerminal|DaemonOverview|DaemonStop|DaemonControl|DaemonUpdate|DaemonDiagnostics|TUISystemDaemon|TUIDaemonControl)' -count=1 -timeout=90s

.PHONY: test-ports
PORTS_TEST_FILTER ?= ^Test(PublishedTCP|Forwarding|DynamicForward|RenderLimaTemplate|LimaForwarding|NodeForwarding|TUIPorts)
PORTS_TEST_FLAGS ?=
test-ports: init
	mkdir -p '$(CURDIR)/tmp/.go-work'
	TMPDIR='$(CURDIR)/tmp/.go-work' $(GO) test $(PORTS_TEST_FLAGS) ./internal/codelima -run '$(PORTS_TEST_FILTER)' -count=1 -timeout=120s

test-system-doctor: init
	mkdir -p '$(CURDIR)/tmp/.go-work'
	TMPDIR='$(CURDIR)/tmp/.go-work' $(GO) test ./internal/codelima -run '^Test(TUIDoctor|TUIHost|TUIManagerSettings)' -count=1 -timeout=90s

test-dependency-hints: init
	mkdir -p '$(CURDIR)/tmp/.go-work'
	TMPDIR='$(CURDIR)/tmp/.go-work' $(GO) test ./internal/codelima -run '^Test(EnvironmentDependency|TUIRecipeDependency)' -count=1 -timeout=90s

test-environment-runs: init
	mkdir -p '$(CURDIR)/tmp/.go-work'
	TMPDIR='$(CURDIR)/tmp/.go-work' $(GO) test ./internal/codelima -run '^Test(EnvironmentTest|NodeOperationsEnvironment|TUIEnvironmentTest)' -count=1 -timeout=90s

test-recipes: init
	mkdir -p '$(CURDIR)/tmp/.go-work'
	TMPDIR='$(CURDIR)/tmp/.go-work' $(GO) test ./internal/codelima -run '^Test(TUIRecipe|TUIEnvironmentEditor|NodeSpec|EnvironmentConfig)' -count=1

test-template-editor: init
	mkdir -p '$(CURDIR)/tmp/.go-work'
	TMPDIR='$(CURDIR)/tmp/.go-work' $(GO) test ./internal/codelima -run '^Test(TUITemplate|TUICommandEditor|TUIDialogSubmitUnderHeldLock|LimaTemplateNames)' -count=1

test-create-node: init
	mkdir -p '$(CURDIR)/tmp/.go-work'
	TMPDIR='$(CURDIR)/tmp/.go-work' $(GO) test ./internal/codelima -run '^Test(NodeCreate|NodeOperationsCreate|TUICreate|TUITemplatePicker)' -count=1

test-activity: init
	mkdir -p '$(CURDIR)/tmp/.go-work'
	TMPDIR='$(CURDIR)/tmp/.go-work' $(GO) test ./internal/codelima -run '^Test(ActivityJournal|ActivityPage|ActivityNative|TUIActivity|DaemonLiveUpdate|DaemonImport)' -count=1
	TMPDIR='$(CURDIR)/tmp/.go-work' $(GO) test ./internal/codelima/daemon -run '^TestHandoff' -count=1

test-node-operations: init
	mkdir -p '$(CURDIR)/tmp/.go-work'
	TMPDIR='$(CURDIR)/tmp/.go-work' $(GO) test ./internal/codelima -run '^Test(NodeOperations|NodeStop|TUIActivity|TUILifecycle|CLIOperation|DaemonStartupRecovery)' -count=1

test-bootstrap-steps: init
	$(GO) test ./internal/codelima -run 'Test(BootstrapSteps|NodeStart|NodeClone|TUISpec)' -count=1

test-node-spec: init
	$(GO) test ./internal/codelima -run 'Test(NodeSpec|TUI.*Spec)' -count=1

test-node-logs: init
	$(GO) test ./internal/codelima -run 'Test(NodeLog|BootstrapLog|TUI.*Logs)' -count=1

test-metrics: init
	$(GO) test ./internal/codelima ./internal/codelima/daemon -run 'Test(NodeMetrics|ForwarderMetrics|TUI.*Metrics)' -count=1

test-host-capacity: init
	$(GO) test ./internal/hostinfo ./internal/codelima -run 'Test(ReadVolume|ReadMissingVolume|LinuxMemory|LinuxCPU|LinuxVolume|TUIHost|TUIManagerSettings)' -count=1
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build ./internal/hostinfo
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(GO) build ./internal/hostinfo

test-manager-settings: init
	$(GO) test -ldflags "$(RENDERER_LDFLAGS)" ./internal/codelima -run '^Test(ManagerSettings|Shell|TUIManagerMenu|TUIManagerSettings)' -count=1

.PHONY: test-tui-manager
test-tui-manager: init
	$(GO) test -ldflags "$(RENDERER_LDFLAGS)" ./internal/codelima -run '^Test(TUI(Fleet|Overview|Manager|Resize|Shortcut|Tab|FocusToggle)|PushedReload|TerminalTab)' -count=1

.PHONY: test-tui-help
TUI_HELP_TEST_FLAGS ?=
test-tui-help: init
	@mkdir -p '$(CURDIR)/tmp/.go-work'
	TMPDIR='$(CURDIR)/tmp/.go-work' $(GO) test $(TUI_HELP_TEST_FLAGS) -ldflags "$(RENDERER_LDFLAGS)" ./internal/codelima -run '^Test(TUIManager(Help|Palette)|TUIDaemonControlLogReload)' -count=1 -timeout=90s

.PHONY: test-tui-palette
TUI_PALETTE_TEST_FLAGS ?=
test-tui-palette: init
	@mkdir -p '$(CURDIR)/tmp/.go-work'
	TMPDIR='$(CURDIR)/tmp/.go-work' $(GO) test $(TUI_PALETTE_TEST_FLAGS) -ldflags "$(RENDERER_LDFLAGS)" ./internal/codelima -run '^Test(TUIManager(Palette|Help)|TUINodeRestart|TUIFleet|TUIDaemonControl)' -count=1 -timeout=120s

.PHONY: test-shell-chooser
SHELL_CHOOSER_TEST_FLAGS ?=
test-shell-chooser: init
	@mkdir -p '$(CURDIR)/tmp/.go-work'
	TMPDIR='$(CURDIR)/tmp/.go-work' $(GO) test $(SHELL_CHOOSER_TEST_FLAGS) -ldflags "$(RENDERER_LDFLAGS)" ./internal/codelima -run '^Test(TUIManager(Shell|Palette|Menu)|TUIRaw|ManagedGuest|Shell|ResolveRawShell)' -count=1 -timeout=120s

.PHONY: test-embedded-shell
EMBEDDED_SHELL_TEST_FLAGS ?=
test-embedded-shell: init
	@mkdir -p '$(CURDIR)/tmp/.go-work'
	TMPDIR='$(CURDIR)/tmp/.go-work' $(GO) test $(EMBEDDED_SHELL_TEST_FLAGS) -ldflags "$(RENDERER_LDFLAGS)" ./internal/codelima -run '^Test(TUIEmbedded|TUIManager|TUIResize|TUIHostTerminal|TUIDrawActiveHost|TUITerminalTab|TUIShortcut|TUIHandleKeyOption|TUIFocusToggle|TUIHandleKeyF6|TUIBackgroundNodeOperation|TUIDrawTerminal)' -count=1 -timeout=120s

.PHONY: test-node-confirmations test-clone test-node-batch test-node-delete-sessions
NODE_CONFIRMATION_TEST_FLAGS ?=
CLONE_TEST_FLAGS ?=
NODE_BATCH_TEST_FLAGS ?=
NODE_DELETE_SESSION_TEST_FLAGS ?=
test-node-delete-sessions: init
	@mkdir -p '$(CURDIR)/tmp/.go-work'
	TMPDIR='$(CURDIR)/tmp/.go-work' $(GO) test $(NODE_DELETE_SESSION_TEST_FLAGS) -ldflags "$(RENDERER_LDFLAGS)" ./internal/codelima -run '^TestDaemonNodeDelete' -count=1 -timeout=120s

test-node-batch: init
	@mkdir -p '$(CURDIR)/tmp/.go-work'
	TMPDIR='$(CURDIR)/tmp/.go-work' $(GO) test $(NODE_BATCH_TEST_FLAGS) -ldflags "$(RENDERER_LDFLAGS)" ./internal/codelima -run '^Test(NodeBatch|TUIBatch|LimaIdentity|TUIManagerPalette.*Batch|TUINodeRestart)' -count=1 -timeout=120s

test-clone: init
	mkdir -p '$(CURDIR)/tmp/.go-work'
	TMPDIR='$(CURDIR)/tmp/.go-work' $(GO) test $(CLONE_TEST_FLAGS) -ldflags "$(RENDERER_LDFLAGS)" ./internal/codelima -run '^Test(NodeClone|NodeOperations(Clone|CreateStart)|TUIClone)' -count=1 -timeout=120s

test-node-confirmations: init
	@mkdir -p '$(CURDIR)/tmp/.go-work'
	TMPDIR='$(CURDIR)/tmp/.go-work' $(GO) test $(NODE_CONFIRMATION_TEST_FLAGS) -ldflags "$(RENDERER_LDFLAGS)" ./internal/codelima -run '^Test(TUINode(Confirmation|Restart|Actions)|TUIStopOverlay|TUIManagerSettings|TUIManagerPalette|TUIFleetSession)' -count=1 -timeout=120s

.PHONY: test-fleet-evidence
.PHONY: test-overview
OVERVIEW_TEST_FLAGS ?=
test-overview: init
	mkdir -p '$(CURDIR)/tmp/.go-work'
	TMPDIR='$(CURDIR)/tmp/.go-work' $(GO) test $(OVERVIEW_TEST_FLAGS) -ldflags "$(RENDERER_LDFLAGS)" ./internal/codelima -run '^Test(TUIOverview|TUIFleet|TUIManager|FleetEvidence|ResolveRawShell)' -count=1 -timeout=120s

FLEET_EVIDENCE_TEST_FLAGS ?=
test-fleet-evidence: init
	mkdir -p '$(CURDIR)/tmp/.go-work'
	TMPDIR='$(CURDIR)/tmp/.go-work' $(GO) test $(FLEET_EVIDENCE_TEST_FLAGS) ./internal/codelima -run '^Test(FleetEvidence|FleetPreview|TUIFleet)' -count=1 -timeout=120s

.PHONY: test-prompt-idle
PROMPT_IDLE_TEST_FLAGS ?=
test-prompt-idle: init
	mkdir -p '$(CURDIR)/tmp/.go-work'
	TMPDIR='$(CURDIR)/tmp/.go-work' $(GO) test $(PROMPT_IDLE_TEST_FLAGS) -ldflags "$(RENDERER_LDFLAGS)" ./internal/codelima ./internal/codelima/daemon -run '^Test(TerminalPromptIdle|TUIFleet|ActivityNative|DaemonImport|IsolatedTerminalPromptIdle|Handoff)' -count=1 -timeout=180s

.PHONY: test-empty-nodes
EMPTY_NODES_TEST_FLAGS ?=
test-empty-nodes: init
	mkdir -p '$(CURDIR)/tmp/.go-work'
	TMPDIR='$(CURDIR)/tmp/.go-work' $(GO) test $(EMPTY_NODES_TEST_FLAGS) ./internal/codelima -run '^TestTUIManagerEmpty' -count=1 -timeout=90s

.PHONY: test-node-restart
NODE_RESTART_TEST_FLAGS ?=
test-node-restart: init
	mkdir -p '$(CURDIR)/tmp/.go-work'
	TMPDIR='$(CURDIR)/tmp/.go-work' $(GO) test $(NODE_RESTART_TEST_FLAGS) ./internal/codelima -run '^Test(NodeRestart|TUINodeRestart|NodeOperationsClientExit|CLIOperationConnection|RenderFooterUsesAvailable|TUIAvailableActions)' -count=1 -timeout=90s

.PHONY: test-tui-resize
test-tui-resize: init
	$(GO) test -ldflags "$(RENDERER_LDFLAGS)" ./internal/codelima -run '^Test(TUI(Resize|HandleResize)|Daemon.*Resize|IsolatedTerminalResize)' -count=1

.PHONY: test-native-agents
test-native-agents: init
	$(GO) test -ldflags "$(RENDERER_LDFLAGS)" ./internal/codelima -run 'Test(CodexBubblewrapProvisioning|NativeAgentInstallerExecution|EveryLegacyAgentEnvironmentMigratesToNative|Untouched|BuiltInEnvironmentConfigsSeed|NodeStartRepairsCompletedLegacyBuiltInAgentBootstrap|AgentEnvironmentBootstrapCannotComplete)' -count=1

test-race: init
	$(GO) test -ldflags "$(RENDERER_LDFLAGS)" -race -p $(GO_RACE_TEST_PARALLEL) -parallel $(GO_RACE_TEST_PARALLEL) ./...

test-integration: build
	mkdir -p $(INTEGRATION_TMP)
	CODELIMA_TEST_BIN=$(CODELIMA_BIN) CODELIMA_TEST_TMP=$(INTEGRATION_TMP) $(GO) test -ldflags "$(RENDERER_LDFLAGS)" -p $(GO_TEST_PARALLEL) -parallel $(GO_TEST_PARALLEL) -tags=integration ./tests
	rm -rf $(INTEGRATION_TMP)

test-lima-native: init
	@set -eu; native_lima_tmp='$(CURDIR)/tmp/native-lima'; \
	trap 'rm -rf "$$native_lima_tmp"' EXIT; \
	mkdir -p "$$native_lima_tmp"; \
	CODELIMA_NATIVE_LIMA=1 TMPDIR="$$native_lima_tmp" $(GO) test -ldflags "$(RENDERER_LDFLAGS)" -run '^TestNativeLimaTemplateValidation$$' ./internal/codelima

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

package-formula: go-toolchain
	./scripts/render_homebrew_formula.sh $(RELEASE_REPO) $(RELEASE_TAG) $(DIST_DIR) $(FORMULA_OUTPUT) $(GO)

release-metadata: go-toolchain
	@$(GO) run ./cmd/codelima-release metadata --tag "$$RELEASE_TAG"

test-release: go-toolchain
	$(GO) test ./internal/release ./cmd/codelima-release

verify: fmt-check lint test test-vaxis-fork build

clean:
	rm -rf $(BIN_DIR) $(DIST_DIR)

clean-all:
	rm -rf $(BIN_ROOT) $(DIST_DIR)

.PHONY: test-clipboard
test-clipboard: init
	$(GO) test -ldflags "$(RENDERER_LDFLAGS)" ./internal/ghostty ./internal/codelima ./internal/codelima/daemon -run Clipboard -count=1
