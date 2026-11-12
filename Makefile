SHELL := /usr/bin/env bash
.SHELLFLAGS := -euo pipefail -c

ENV := source scripts/dev-env.sh &&
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/b0balYson/dropsite/internal/brand.Version=$(VERSION)
CLI_PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

.PHONY: tools test cover lint build cli-release e2e dev-up dev-down fmt

tools:
	./scripts/install-tools.sh

fmt:
	$(ENV) gofmt -w cmd internal test

test:
	$(ENV) go test -race -count=1 ./...

cover:
	$(ENV) go test -count=1 -coverprofile=coverage.out ./... && go tool cover -func=coverage.out | tail -1

lint:
	$(ENV) golangci-lint run ./...

build:
	$(ENV) CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/control ./cmd/control
	$(ENV) CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/pushpage ./cmd/pushpage

cli-release:
	@rm -rf dist && mkdir -p dist
	@for p in $(CLI_PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ "$$os" = windows ] && ext=.exe; \
		$(ENV) CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' \
			-o dist/pushpage-$$os-$$arch$$ext ./cmd/pushpage; \
		echo "dist/pushpage-$$os-$$arch$$ext"; \
	done
	@cd dist && shasum -a 256 pushpage-* > SHA256SUMS

e2e:
	$(ENV) go test -count=1 -timeout 20m -tags e2e ./test/e2e/...

dev-up:
	docker compose -f deploy/docker-compose.yml -f deploy/compose.local.yml --env-file deploy/local.env up -d --build

dev-down:
	docker compose -f deploy/docker-compose.yml -f deploy/compose.local.yml --env-file deploy/local.env down
