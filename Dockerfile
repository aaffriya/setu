# syntax=docker/dockerfile:1

# Setu ships as a single static Go binary that embeds the web UI. The build is
# three stages: build the frontend, build the binary (embedding the frontend),
# then copy just the binary into a tiny final image.

# --- Stage 1: build the Svelte frontend → /web/dist ---
FROM node:22-alpine AS web
WORKDIR /web
# Copy manifests first for dependency-layer caching.
COPY web/package.json web/package-lock.json* ./
RUN npm ci || npm install
COPY web/ ./
RUN npm run build

# --- Stage 2: build the static Go binary (embeds web/dist) ---
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Bring in the freshly built frontend so //go:embed picks it up.
COPY --from=web /web/dist ./web/dist
# CGO off → fully static binary; -s -w + -trimpath shrink it.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/setu ./cmd/setu

# --- Stage 3: tiny final image (just the binary + a default config) ---
FROM gcr.io/distroless/static-debian12:nonroot AS final
COPY --from=build /out/setu /usr/local/bin/setu
# A default config; mount your own over it (see README "Deployment").
COPY config.yaml /etc/setu/config.yaml
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/setu", "-config", "/etc/setu/config.yaml"]

# NOTE: Setu needs the host network to reach LAN devices, read the ARP table,
# and (later) send UDP broadcasts / mDNS. Run with host networking and mount a
# config, e.g.:
#
#   docker run --rm --network host \
#     -v $PWD/config.yaml:/etc/setu/config.yaml:ro \
#     setu
#
# For a Unix-socket listener, also mount a writable dir for the socket, e.g.
#   -v /run/setu:/run   and set listen: "unix:/run/setu.sock" in the config.
