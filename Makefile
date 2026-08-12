# TemperCI common developer targets.
# Go toolchain required (1.22+).

MODULE   := github.com/TwanLuttik/TemperCI
BIN_DIR  := bin
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -X main.version=$(VERSION)

.PHONY: all build test lint clean

all: build

build:
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/temperci-control ./cmd/temperci-control
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/temperci-agent ./cmd/temperci-agent

test:
	go test ./...

# Placeholder until golangci-lint (or equivalent) is configured.
lint:
	@echo "lint: no linter configured yet (noop)"
	@go vet ./...

clean:
	rm -rf $(BIN_DIR)
