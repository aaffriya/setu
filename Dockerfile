# syntax=docker/dockerfile:1

# Setu ships as a single static Go binary that embeds the web UI. The build is
# three stages: build the frontend, build the binary (embedding the frontend),
# then copy just the binary into a tiny final image.

# --- Stage 1: build the Svelte frontend → /web/dist ---
# Output is just static JS/CSS (arch-independent), so always build on the
# native BUILDPLATFORM — never under QEMU emulation for the target arch.
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /web
# Copy manifests first for dependency-layer caching.
COPY web/package.json web/package-lock.json* ./
RUN npm ci || npm install
COPY web/ ./
RUN npm run build

# --- Stage 2: build the static Go binary (embeds web/dist) ---
# Build stage native runner par chalta hai, Go cross-compile karta hai.
FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS build
ARG TARGETOS TARGETARCH TARGETVARIANT
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Bring in the freshly built frontend so //go:embed picks it up.
COPY --from=web /web/dist ./web/dist
# TARGETVARIANT = v5/v6/v7 (arm ke liye); "v" hata ke GOARM banata hai.
# CGO off → fully static binary; -s -w + -trimpath shrink it.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOARM=${TARGETVARIANT#v} \
    go build -trimpath -ldflags="-s -w" -o /out/setu ./cmd/setu

# --- Stage 3: tiny final image (just the binary + a default config) ---
# scratch works for EVERY target arch (386, arm/v5·v6·v7, mipsle…). Distroless
# isn't published for those exotic router archs, and the binary is fully static
# (CGO off) so it needs nothing else — no libc, no shell.
FROM scratch AS final
COPY --from=build /out/setu /usr/local/bin/setu
# A default config; mount your own over it (see README "Deployment").
COPY config.yaml /etc/setu/config.yaml
EXPOSE 80
EXPOSE 443
ENTRYPOINT ["/usr/local/bin/setu", "-config", "/etc/setu/config.yaml"]

# Multi-arch build (cross-compiled, no QEMU for the Go/JS builds):
#
#   docker buildx build \
#     --platform linux/amd64,linux/arm64,linux/arm/v7,linux/arm/v5 \
#     -t setu:latest .
#
# Setu needs L2 access to the LAN: it reads the ARP table and sends UDP
# broadcasts / mDNS. So its network namespace must sit ON the LAN broadcast
# domain (not NAT'd / routed). Two deployments:
#
# • Plain Docker / Podman (x86 or Linux host) — host networking + a mounted config:
#
#     docker run --rm --network host \
#       -v $PWD/config.yaml:/etc/setu/config.yaml:ro setu
#
# For a Unix-socket listener, mount a writable dir for the socket, e.g.
#   -v /run/setu:/run   and set listen: "unix:/run/setu.sock" in the config.
