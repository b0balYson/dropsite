# syntax=docker/dockerfile:1
#
# Image of the control server. Build context is the repository root:
#   docker build -f deploy/Dockerfile --build-arg VERSION="$(scripts/version.sh)" .
# Releases are built by .github/workflows/release.yml (docs/releasing.md).

# Go 1.27.4 on Alpine 3.24 (go.mod requires go 1.27.0).
ARG GO_IMAGE=golang:1.27.4-alpine3.24@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf
# Distroless static, Debian 13, nonroot (uid/gid 65532), no shell.
ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian13:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY skills ./skills
# SQLite compilation is memory-heavy on 1 GB hosts. Build one package at a
# time and collect garbage sooner; GOMEMLIMIT is a soft per-process target.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    GOMAXPROCS=1 GOGC=20 GOMEMLIMIT=256MiB \
    go build -p=1 -trimpath \
      -ldflags "-s -w -X github.com/b0balYson/dropsite/internal/brand.Version=${VERSION}" \
      -o /out/control ./cmd/control

# Mount points for the volumes. A new named volume copies ownership and mode
# from the image directory it is mounted on, so control (65532) can write to
# fresh volumes without a root init step.
RUN mkdir -p /out/volumes/data /out/volumes/state \
 && chmod 0755 /out/volumes/data \
 && chmod 0700 /out/volumes/state

FROM ${RUNTIME_IMAGE}
LABEL org.opencontainers.image.title="dropsite-control" \
      org.opencontainers.image.description="dropsite control server: API, web interface and publishing" \
      org.opencontainers.image.source="https://github.com/b0balYson/dropsite" \
      org.opencontainers.image.licenses="Apache-2.0"
COPY --from=build --chown=65532:65532 /out/volumes/ /
COPY --from=build /out/control /control
USER 65532:65532
EXPOSE 8080 8081 8082
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD ["/control", "healthcheck"]
ENTRYPOINT ["/control"]
CMD ["serve"]
