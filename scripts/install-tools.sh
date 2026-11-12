#!/usr/bin/env bash
# Installs development tools into .tools/ (never system-wide).
set -euo pipefail

GO_VERSION="${GO_VERSION:-go1.27.4}"
GOLANGCI_LINT_VERSION="${GOLANGCI_LINT_VERSION:-v2.15.0}"

root="$(cd "$(dirname "$0")/.." && pwd)"
tools="$root/.tools"
mkdir -p "$tools/bin"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$arch" in
  x86_64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
esac

if [ ! -x "$tools/go/bin/go" ] || [ "$("$tools/go/bin/go" env GOVERSION)" != "$GO_VERSION" ]; then
  echo "installing $GO_VERSION into .tools/go"
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  file="$GO_VERSION.$os-$arch.tar.gz"
  sha="$(curl -fsSL 'https://go.dev/dl/?mode=json&include=all' |
    python3 -I -c 'import json,sys; f=sys.argv[1]; print(next(x["sha256"] for r in json.load(sys.stdin) for x in r["files"] if x["filename"]==f))' "$file")"
  curl -fsSL -o "$tmp/$file" "https://go.dev/dl/$file"
  echo "$sha  $tmp/$file" | shasum -a 256 -c -
  rm -rf "$tools/go"
  tar -C "$tools" -xzf "$tmp/$file"
fi

# shellcheck source=/dev/null
source "$root/scripts/dev-env.sh"

if ! golangci-lint version 2>/dev/null | grep -q "${GOLANGCI_LINT_VERSION#v}"; then
  echo "installing golangci-lint $GOLANGCI_LINT_VERSION into .tools/bin"
  GOBIN="$tools/bin" go install "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$GOLANGCI_LINT_VERSION"
fi

go version
golangci-lint version
