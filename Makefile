# TemperCI common developer targets.
# Go 1.22+ and Node.js 20+ (for the Vite dashboard under web/).

MODULE   := github.com/TwanLuttik/TemperCI
BIN_DIR  := bin
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -X main.version=$(VERSION)
WEB_DIR  := web
UI_DIST  := internal/webui/dist/index.html

UI_STAMP := internal/webui/dist/.build-stamp
UI_SRCS  := $(shell find $(WEB_DIR)/src -type f 2>/dev/null) \
	$(WEB_DIR)/index.html \
	$(WEB_DIR)/package.json \
	$(WEB_DIR)/package-lock.json \
	$(WEB_DIR)/vite.config.ts \
	$(WEB_DIR)/tsconfig.json \
	$(WEB_DIR)/tsconfig.app.json \
	$(WEB_DIR)/tsconfig.node.json

.PHONY: all build build-ui build-go build-linux test test-go test-sh lint clean

all: build

# Full product build: Vite dashboard → embed → Go binaries.
build: build-ui build-go

# Install JS deps (if needed) and produce internal/webui/dist for go:embed.
# Skips Vite when sources are unchanged (stamp is newer than UI_SRCS).
build-ui: $(UI_STAMP)

$(UI_STAMP): $(UI_SRCS)
	@command -v npm >/dev/null 2>&1 || { echo "error: npm is required to build the dashboard (install Node.js 20+)"; exit 1; }
	@if [ ! -d $(WEB_DIR)/node_modules ]; then \
		echo "npm ci (web/)"; \
		cd $(WEB_DIR) && npm ci; \
	fi
	@echo "vite build → $(UI_DIST)"
	cd $(WEB_DIR) && npm run build
	@test -f $(UI_DIST) || { echo "error: dashboard build missing $(UI_DIST)"; exit 1; }
	@touch $(UI_STAMP)

# Go binaries only (requires a prior build-ui so dist/ exists).
build-go:
	@test -f $(UI_DIST) || { echo "error: missing $(UI_DIST) — run: make build-ui"; exit 1; }
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/temperci-control ./cmd/temperci-control
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/temperci-agent ./cmd/temperci-agent
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/temperci-hostctl ./cmd/temperci-hostctl

# Cross-compile Linux amd64 binaries for release / install.sh (needs prior build-ui).
build-linux:
	@test -f $(UI_DIST) || { echo "error: missing $(UI_DIST) — run: make build-ui"; exit 1; }
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/temperci-control-linux-amd64 ./cmd/temperci-control
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/temperci-agent-linux-amd64 ./cmd/temperci-agent
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/temperci-hostctl-linux-amd64 ./cmd/temperci-hostctl

# Go tests only (requires an existing dashboard embed; does not rebuild Vite).
test-go:
	@test -f $(UI_DIST) || { echo "error: missing $(UI_DIST) — run: make build-ui"; exit 1; }
	go test ./...

test-sh:
	bash -n deploy/ubuntu/install.sh deploy/ubuntu/prepare-guest-image.sh deploy/ubuntu/install-cache-ca.sh
	bash deploy/ubuntu/install_test.sh
	bash deploy/ubuntu/install_cache_ca_test.sh
	bash deploy/ubuntu/docker-cache-wrapper_test.sh
	bash deploy/ubuntu/guest-agent/protocol_test.sh
	bash deploy/ubuntu/guest-agent/remap_exit_test.sh

test: $(UI_STAMP) test-go test-sh

# Placeholder until golangci-lint (or equivalent) is configured.
lint:
	@echo "lint: no linter configured yet (noop)"
	@go vet ./...

clean:
	rm -rf $(BIN_DIR)
	rm -rf internal/webui/dist
	rm -rf $(WEB_DIR)/node_modules
