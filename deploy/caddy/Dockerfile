# syntax=docker/dockerfile:1
#
# Caddy with DNS provider plugins, needed only for TLS_MODE=wildcard (the
# wildcard certificate requires the DNS-01 challenge). Build context is this
# directory:
#   docker build -t ghcr.io/b0balyson/dropsite-caddy:2.11.9 deploy/caddy
#
# Adding a DNS provider: append another "--with github.com/caddy-dns/<name>@<version>"
# to the xcaddy command below (pin the version) and a "(dns_<name>)" snippet to
# deploy/Caddyfile; see docs/tls.md.

ARG CADDY_VERSION=2.11.9
# Same Caddy release as the stock image used in ondemand mode.
ARG BUILDER_IMAGE=caddy:2.11.9-builder@sha256:345264d8c6b50f44f4a4d507d4e6b6bcae40eef2f210d70f76e717fd8cc31baf
ARG RUNTIME_IMAGE=caddy:2.11.9@sha256:e40b94d0a43b5ff69cb20568d73f10c70703df7b0ffc2298eb3d723cdc9d0bd1

# The builder runs on the build platform and cross-compiles (pure Go, no CGO),
# so multi-arch builds do not need emulation:
#   docker buildx build --platform linux/amd64,linux/arm64 ...
FROM --platform=$BUILDPLATFORM ${BUILDER_IMAGE} AS build
ARG CADDY_VERSION
ARG TARGETOS
ARG TARGETARCH
ARG CLOUDFLARE_VERSION=v0.2.4
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS=$TARGETOS GOARCH=$TARGETARCH CGO_ENABLED=0 \
    xcaddy build v${CADDY_VERSION} \
      --output /out/caddy \
      --with github.com/caddy-dns/cloudflare@${CLOUDFLARE_VERSION}

FROM ${RUNTIME_IMAGE}
COPY --from=build /out/caddy /usr/bin/caddy
LABEL org.opencontainers.image.title="dropsite-caddy" \
      org.opencontainers.image.description="Caddy with DNS provider plugins for dropsite wildcard TLS" \
      org.opencontainers.image.source="https://github.com/b0balYson/dropsite"
