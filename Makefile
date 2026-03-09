.PHONY: all build web web-install web-build go-build clean dev

# Default target: build everything
all: build

# Full build: frontend + Go binary
build: web go-build

# ---- Frontend ----

WEB_DIR := web
STATIC_DIR := internal/server/static

# Install frontend dependencies
web-install:
	cd $(WEB_DIR) && pnpm install --frozen-lockfile

# Build frontend → output to internal/server/static/
web-build:
	cd $(WEB_DIR) && pnpm run build

# Install + build frontend
web: web-install web-build

# ---- Go ----

# Build Go binary with embedded frontend
go-build:
	CGO_ENABLED=0 go build -ldflags "-s -w" -o Pcapchu ./cmd/main.go

# ---- Development ----

# Run frontend dev server (with API proxy to :8080)
dev:
	cd $(WEB_DIR) && pnpm run dev

# ---- Clean ----

clean:
	rm -f pcapchu
	rm -rf $(STATIC_DIR)/assets
	@# Restore placeholder index.html if it was overwritten
	@echo '<!DOCTYPE html><html><head><title>Pcapchu</title></head><body><h1>Frontend not built</h1><p>Run <code>make web</code> to build the frontend.</p></body></html>' > $(STATIC_DIR)/index.html

# ---- Lint ----

vet:
	go vet ./...
