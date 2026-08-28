# Setu — build the embedded frontend, then the single static Go binary.

BINARY  := setu
PKG     := ./cmd/setu
WEB     := web
LDFLAGS := -s -w

.PHONY: all web build run dev docker fmt vet test tidy clean clean-web help

all: build ## Build frontend + host binary (default)

web: ## Build the Svelte frontend into web/dist
	cd $(WEB) && bun install --frozen-lockfile && bun run build
	@touch $(WEB)/dist/.gitkeep   # Vite empties dist on build; keep the tracked marker

build: web ## Build the host binary (embeds web/dist) into bin/
	go build -trimpath -ldflags="$(LDFLAGS)" -o bin/$(BINARY) $(PKG)

# Local runs keep their state under ./tmp so they never touch a real install.
DEV_ENV := SETU_STATE_DIR=$(PWD)/tmp/state

run: build ## Build everything and run (state in ./tmp/state)
	$(DEV_ENV) ./bin/$(BINARY)

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
