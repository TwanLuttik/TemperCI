# TemperCI common developer targets.
# Go 1.22+ and Node.js 20+ (for the Vite dashboard under web/).

MODULE   := github.com/TwanLuttik/TemperCI
BIN_DIR  := bin
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -X main.version=$(VERSION)
WEB_DIR  := web
UI_DIST  := internal/webui/dist/index.html

.PHONY: all build build-ui build-go build-linux test lint clean

all: build

# Full product build: Vite dashboard → embed → Go binaries.
build: build-ui build-go

# Install JS deps (if needed) and produce internal/webui/dist for go:embed.
build-ui:
	@command -v npm >/dev/null 2>&1 || { echo "error: npm is required to build the dashboard (install Node.js 20+)"; exit 1; }
	@if [ ! -d $(WEB_DIR)/node_modules ]; then \
		echo "npm ci (web/)"; \
		cd $(WEB_DIR) && npm ci; \
	fi
	@echo "vite build → $(UI_DIST)"
	cd $(WEB_DIR) && npm run build
	@test -f $(UI_DIST) || { echo "error: dashboard build missing $(UI_DIST)"; exit 1; }

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

test: build-ui
	go test ./...
	bash -n deploy/ubuntu/install.sh deploy/ubuntu/prepare-guest-image.sh deploy/ubuntu/install-cache-ca.sh
	bash deploy/ubuntu/install_test.sh
	bash deploy/ubuntu/install_cache_ca_test.sh

# Placeholder until golangci-lint (or equivalent) is configured.
lint:
	@echo "lint: no linter configured yet (noop)"
	@go vet ./...

clean:
	rm -rf $(BIN_DIR)
	rm -rf internal/webui/dist
	rm -rf $(WEB_DIR)/node_modules
