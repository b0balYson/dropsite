# syntax=docker/dockerfile:1
#
# Caddy for Dropsite in every TLS mode: the stock release plus
#   - caddy-defender: blocks AI scrapers and cloud ranges (docs/security.md);
#   - caddy-ratelimit: the global per-client rate limit on site addresses;
#   - DNS provider plugins for TLS_MODE=wildcard (DNS-01 challenge).
# deploy/Caddyfile uses the first two, so the stock caddy image cannot run it.
# Build context is this directory:
#   docker build -t dropsite-caddy:local deploy/caddy
#
# Adding a DNS provider: append another "--with github.com/caddy-dns/<name>@<version>"
# to the xcaddy command below (pin the version) and a "(dns_<name>)" snippet to
# deploy/Caddyfile; see docs/tls.md.

ARG CADDY_VERSION=2.11.9
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
# caddy-defender v0.10.2 is built against Caddy 2.11.4 and works with 2.11.9;
# its IP ranges are generated into the module, so they change only with the
# pinned version. caddy-ratelimit has no recent tag: pin the commit.
ARG DEFENDER_VERSION=v0.10.2
ARG RATELIMIT_VERSION=v0.1.1-0.20260612195517-5625512f24f6
# Keep the plugin build usable on small hosts, as in deploy/Dockerfile.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS=$TARGETOS GOARCH=$TARGETARCH CGO_ENABLED=0 \
    GOMAXPROCS=1 GOGC=20 GOMEMLIMIT=256MiB GOFLAGS=-p=1 \
    xcaddy build v${CADDY_VERSION} \
      --output /out/caddy \
      --with github.com/caddy-dns/cloudflare@${CLOUDFLARE_VERSION} \
      --with pkg.jsn.cam/caddy-defender@${DEFENDER_VERSION} \
      --with github.com/mholt/caddy-ratelimit@${RATELIMIT_VERSION}

FROM ${RUNTIME_IMAGE}
COPY --from=build /out/caddy /usr/bin/caddy
LABEL org.opencontainers.image.title="dropsite-caddy" \
      org.opencontainers.image.description="Caddy for Dropsite: scraper blocking, rate limits and DNS plugins" \
      org.opencontainers.image.source="https://github.com/b0balYson/dropsite"
