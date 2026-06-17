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

build-amd64: web ## Cross-compile a static linux/amd64 binary (MikroTik / Pi / OpenWrt)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
		go build -trimpath -ldflags="$(LDFLAGS)" -o bin/$(BINARY)-linux-amd64 $(PKG)

build-mipsle: web ## Cross-compile a static linux/mipsle binary (TP-Link Archer C6U / OpenWrt)
	GOOS=linux GOARCH=mipsle GOMIPS=softfloat CGO_ENABLED=0 \
		go build -trimpath -ldflags="$(LDFLAGS)" -o bin/$(BINARY)-linux-mipsle $(PKG)

push-ssh: web build-mipsle ## Build the linux/mipsle binary and push it to a OpenWrt device over SSH
	scp -O bin/$(BINARY)-linux-mipsle root@192.168.1.1:/mnt/usb/opt/bin/$(BINARY)
	scp -O config.yaml root@192.168.1.1:/mnt/usb/etc/setu/config.yaml
	@echo "Binary and config pushed. SSH into the device and run: /mnt/usb/opt/bin/$(BINARY) -config /mnt/usb/etc/setu/config.yaml"

run: build ## Build everything and run with ./config.yaml
	./bin/$(BINARY) -config config.yaml

dev: ## How to run the hot-reload dev setup (two terminals)
	@echo "Terminal 1:  go run $(PKG) -config config.yaml"
	@echo "Terminal 2:  cd $(WEB) && npm run dev   # Vite proxies /api,/ws -> :8080"

daemon: ## Run the built binary as a background daemon (with ./config.yaml)
	@echo "Starting $(BINARY) in the background..."
	@touch ./tmp/$(BINARY).log
	@nohup ./bin/$(BINARY) -config config.yaml > ./tmp/$(BINARY).log 2>&1 &
	@echo "Daemon started. Logs are being written to ./tmp/$(BINARY).log"

stop-daemon: ## Stop the background daemon (with ./config.yaml)
	@echo "Stopping $(BINARY)..."
	@pkill -f "$(BINARY)" || true
	@echo "Daemon stopped."

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
