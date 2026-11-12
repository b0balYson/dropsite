#!/usr/bin/env bash
# Source this file to use the project-local toolchain from .tools/:
#   source scripts/dev-env.sh
# Nothing is installed system-wide; caches live inside the repository.

_ds_root="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")/.." && pwd)"
export DROPSITE_ROOT="$_ds_root"
export GOROOT="$_ds_root/.tools/go"
export GOPATH="$_ds_root/.tools/gopath"
export GOCACHE="$_ds_root/.tools/gocache"
export GOMODCACHE="$_ds_root/.tools/gopath/pkg/mod"
export GOLANGCI_LINT_CACHE="$_ds_root/.tools/lintcache"
export GOTOOLCHAIN=local
export GOFLAGS=-modcacherw
export PATH="$_ds_root/.tools/bin:$GOROOT/bin:$GOPATH/bin:$PATH"
unset _ds_root
