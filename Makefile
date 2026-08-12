# ============================================================================
# WProxyman — build script (macOS & Linux)
#
# Targets:
#   make            build production binary (default)
#   make dev        run in development mode (hot reload)
#   make test       run all tests (Go tests + frontend type check)
#   make deps       install Go + frontend dependencies
#   make clean      remove build artifacts
#   make doctor     verify the Wails toolchain
#   make tidy       tidy Go modules
#
# Output: build/bin/WProxyman
# Requires: Go 1.25+, Node.js 20+, Wails v2 CLI (auto-installed if missing)
# ============================================================================

APP_NAME := WProxyman
GO       ?= go
NPM      ?= npm

.PHONY: all build dev test test-go test-frontend deps deps-go deps-frontend clean doctor tidy check-tools

all: build

# --- build -------------------------------------------------------------------

build: check-tools deps-frontend
	@echo "==> Building $(APP_NAME) (production)"
	@wails build
	@echo "==> Done. Binary: build/bin/$(APP_NAME)"

# --- dev ---------------------------------------------------------------------

dev: check-tools
	@wails dev

# --- test --------------------------------------------------------------------

test: test-go test-frontend

test-go:
	@echo "==> Go tests"
	@$(GO) test ./...

test-frontend:
	@echo "==> Frontend type check"
	@cd frontend && npx --no-install tsc --noEmit

# --- dependencies -------------------------------------------------------------

deps: deps-go deps-frontend

deps-go:
	@echo "==> Go dependencies"
	@$(GO) mod download

deps-frontend:
	@echo "==> Frontend dependencies"
	@cd frontend && $(NPM) install

# --- maintenance ---------------------------------------------------------------

clean:
	@echo "==> Cleaning build artifacts"
	@rm -rf build/bin frontend/dist

doctor: check-tools
	@wails doctor

# macOS：本地 ad-hoc 打包（生成 WProxyman_darwin_<arch>.dmg，个人自用）
dmg:
	@bash scripts/build-macos.sh

tidy:
	@$(GO) mod tidy

# --- toolchain -----------------------------------------------------------------

check-tools:
	@command -v $(GO) >/dev/null 2>&1 || { echo "ERROR: Go not found. Install Go 1.25+ from https://go.dev/dl/"; exit 1; }
	@command -v $(NPM) >/dev/null 2>&1 || { echo "ERROR: Node.js/npm not found. Install from https://nodejs.org/"; exit 1; }
	@command -v wails >/dev/null 2>&1 || { \
		echo "Wails CLI not found — installing..."; \
		$(GO) install github.com/wailsapp/wails/v2/cmd/wails@latest; \
	}
	@command -v wails >/dev/null 2>&1 || { echo "ERROR: wails not found in PATH after install. Add $$(go env GOPATH)/bin to your PATH, or run: go install github.com/wailsapp/wails/v2/cmd/wails@latest"; exit 1; }
