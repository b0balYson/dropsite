SHELL := /usr/bin/env bash
.SHELLFLAGS := -euo pipefail -c

ENV := source scripts/dev-env.sh &&
# X.Y.Z on a clean release tag, dev+<commit> otherwise (scripts/version.sh);
# the release workflow passes VERSION=X.Y.Z explicitly.
VERSION ?= $(shell sh scripts/version.sh)
LDFLAGS := -s -w -X github.com/b0balYson/dropsite/internal/brand.Version=$(VERSION)
CLI_PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

.PHONY: tools test cover lint lint-sh lint-actions install-test install-e2e build cli-release e2e dev-up dev-down \
	fmt fmt-check release-check release-test release-notes changelog

tools:
	./scripts/install-tools.sh

fmt:
	$(ENV) gofmt -w cmd internal test deploy

# Fails when a Go file is not gofmt-formatted (CI).
fmt-check:
	@$(ENV) out=$$(gofmt -l cmd internal test deploy); \
	if [ -n "$$out" ]; then echo "not gofmt-formatted:"; echo "$$out"; exit 1; fi

test:
	$(ENV) go test -race -count=1 ./...

cover:
	$(ENV) go test -count=1 -coverprofile=coverage.out ./... && go tool cover -func=coverage.out | tail -1

SHELL_SCRIPTS := scripts/install.sh scripts/install-server.sh scripts/server-bootstrap.sh \
	scripts/version.sh scripts/release-check.sh scripts/release-notes.sh scripts/changelog.sh \
	test/install/unit.sh test/install/e2e.sh test/install/entrypoint.sh test/release/unit.sh

lint: lint-sh
	$(ENV) golangci-lint run ./...

# shellcheck from .tools/bin (preferred) or PATH; skipped with a note when absent.
lint-sh:
	@$(ENV) if command -v shellcheck >/dev/null 2>&1; then \
		shellcheck -x -P SCRIPTDIR/../.. $(SHELL_SCRIPTS) && echo "shellcheck: ok"; \
	else \
		echo "shellcheck not found: shell scripts not checked (put a shellcheck binary into .tools/bin)"; \
	fi

# GitHub Actions workflows: actionlint from .tools/bin (make tools), else the
# pinned version through go run.
ACTIONLINT_VERSION := v1.7.12
lint-actions:
	@$(ENV) if command -v actionlint >/dev/null 2>&1; then actionlint; \
	else go run github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION); fi
	@echo "actionlint: ok"

# Release consistency (docs/releasing.md). TAG=vX.Y.Z checks a release.
release-check:
	sh scripts/release-check.sh $(TAG)

release-test:
	sh test/release/unit.sh

# Release notes of the commits since the previous tag (or between FROM and TO).
release-notes:
	@sh scripts/release-notes.sh $(FROM) $(TO)

# Add the section of release X.Y.Z to CHANGELOG.md: make changelog V=1.4.2
changelog:
	sh scripts/changelog.sh add $(V)

# Installer checks without a server: validation, languages, ~/.ssh/config editing.
install-test:
	sh test/install/unit.sh

# Whole installer against a disposable Ubuntu 24.04 container on the local
# Docker engine (10-30 minutes). Never targets a real server.
install-e2e: install-test
	$(ENV) sh test/install/e2e.sh

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
	DROPSITE_BUILD_VERSION=$(VERSION) docker compose -f deploy/docker-compose.yml -f deploy/compose.local.yml --env-file deploy/local.env up -d --build

dev-down:
	docker compose -f deploy/docker-compose.yml -f deploy/compose.local.yml --env-file deploy/local.env down
