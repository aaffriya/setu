# Setu — build the embedded frontend, then the single static Go binary.

BINARY  := setu
PKG     := ./cmd/setu
WEB     := web
LDFLAGS := -s -w

.PHONY: all web build build-arm64 run dev docker fmt vet test tidy clean clean-web help

all: build ## Build frontend + host binary (default)

web: ## Build the Svelte frontend into web/dist
	cd $(WEB) && npm install && npm run build
	@touch $(WEB)/dist/.gitkeep   # Vite empties dist on build; keep the tracked marker

build: web ## Build the host binary (embeds web/dist) into bin/
	go build -trimpath -ldflags="$(LDFLAGS)" -o bin/$(BINARY) $(PKG)

build-arm64: web ## Cross-compile a static linux/arm64 binary (MikroTik / Pi / OpenWrt)
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
		go build -trimpath -ldflags="$(LDFLAGS)" -o bin/$(BINARY)-linux-arm64 $(PKG)

run: build ## Build everything and run with ./config.yaml
	./bin/$(BINARY) -config config.yaml

dev: ## How to run the hot-reload dev setup (two terminals)
	@echo "Terminal 1:  go run $(PKG) -config config.yaml"
	@echo "Terminal 2:  cd $(WEB) && npm run dev   # Vite proxies /api,/ws -> :8080"

docker: ## Build the Docker image (tag: setu)
	docker build -t $(BINARY) .

fmt: ## Format Go sources
	gofmt -w .

vet: ## Run go vet
	go vet ./...

test: ## Run Go tests
	go test ./...

tidy: ## Tidy go.mod / go.sum
	go mod tidy

clean: ## Remove built binaries
	rm -rf bin

clean-web: ## Remove the built frontend (run `make web` to rebuild)
	find $(WEB)/dist -mindepth 1 ! -name .gitkeep -delete

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n",$$1,$$2}'
