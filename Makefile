SHELL := /usr/bin/env bash
.SHELLFLAGS := -euo pipefail -c

ENV := source scripts/dev-env.sh &&
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/b0balYson/dropsite/internal/brand.Version=$(VERSION)
CLI_PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

.PHONY: tools test cover lint lint-sh install-test install-e2e build cli-release e2e dev-up dev-down fmt

tools:
	./scripts/install-tools.sh

fmt:
	$(ENV) gofmt -w cmd internal test

test:
	$(ENV) go test -race -count=1 ./...

cover:
	$(ENV) go test -count=1 -coverprofile=coverage.out ./... && go tool cover -func=coverage.out | tail -1

SHELL_SCRIPTS := scripts/install.sh scripts/install-server.sh scripts/server-bootstrap.sh \
	test/install/unit.sh test/install/e2e.sh test/install/entrypoint.sh

lint: lint-sh
	$(ENV) golangci-lint run ./...

# shellcheck from .tools/bin (preferred) or PATH; skipped with a note when absent.
lint-sh:
	@$(ENV) if command -v shellcheck >/dev/null 2>&1; then \
		shellcheck -x -P SCRIPTDIR/../.. $(SHELL_SCRIPTS) && echo "shellcheck: ok"; \
	else \
		echo "shellcheck not found: shell scripts not checked (put a shellcheck binary into .tools/bin)"; \
	fi

# Installer checks without a server: validation, languages, ~/.ssh/config editing.
install-test:
	sh test/install/unit.sh

# Whole installer against a disposable Ubuntu 24.04 container on the local
# Docker engine (10-30 minutes). Never targets a real server.
install-e2e: install-test
	sh test/install/e2e.sh

build:
	$(ENV) CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/control ./cmd/control
	$(ENV) CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/dropsite ./cmd/dropsite

cli-release:
	@rm -rf dist && mkdir -p dist
	@for p in $(CLI_PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ "$$os" = windows ] && ext=.exe; \
		$(ENV) CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' \
			-o dist/dropsite-$$os-$$arch$$ext ./cmd/dropsite; \
		echo "dist/dropsite-$$os-$$arch$$ext"; \
	done
	@cd dist && shasum -a 256 dropsite-* > SHA256SUMS

e2e:
	$(ENV) go test -count=1 -timeout 20m -tags e2e ./test/e2e/...

dev-up:
	docker compose -f deploy/docker-compose.yml -f deploy/compose.local.yml --env-file deploy/local.env up -d --build

dev-down:
	docker compose -f deploy/docker-compose.yml -f deploy/compose.local.yml --env-file deploy/local.env down
