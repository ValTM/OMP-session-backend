.PHONY: help dev build test fmt check clean

GO ?= go
SERVER_PKG ?= ./cmd/server
BINARY ?= bin/omp-session-viewer-server
ADDR ?= 127.0.0.1:8080
OMP_ROOT ?= ~/.omp/agent
FRONTEND_ORIGIN ?= http://localhost:5173

help:
	@echo "Backend targets:"
	@echo "  make dev      Run the API server locally"
	@echo "  make build    Build the API server binary"
	@echo "  make test     Run backend tests"
	@echo "  make fmt      Format Go sources"
	@echo "  make check    Format, test, and build"
	@echo "  make clean    Remove build outputs"
	@echo ""
	@echo "Config:"
	@echo "  ADDR=$(ADDR)"
	@echo "  OMP_ROOT=$(OMP_ROOT)"
	@echo "  FRONTEND_ORIGIN=$(FRONTEND_ORIGIN)"
	@echo "  BINARY=$(BINARY)"

dev:
	GO="$(GO)" SERVER_PKG="$(SERVER_PKG)" BINARY="$(BINARY)" ADDR="$(ADDR)" OMP_ROOT="$(OMP_ROOT)" FRONTEND_ORIGIN="$(FRONTEND_ORIGIN)" ./scripts/dev.sh

build:
	mkdir -p $(dir $(BINARY))
	$(GO) build -o "$(BINARY)" $(SERVER_PKG)

test:
	$(GO) test ./...

fmt:
	gofmt -w ./cmd ./internal

check: fmt test build

clean:
	rm -rf "$(dir $(BINARY))"
