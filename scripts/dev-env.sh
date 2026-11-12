#!/usr/bin/env bash
# Source this file to use the project-local toolchain from .tools/:
#   source scripts/dev-env.sh
# Nothing is installed system-wide; caches live inside the repository.

_pp_root="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")/.." && pwd)"
export PUSHPAGE_ROOT="$_pp_root"
export GOROOT="$_pp_root/.tools/go"
export GOPATH="$_pp_root/.tools/gopath"
export GOCACHE="$_pp_root/.tools/gocache"
export GOMODCACHE="$_pp_root/.tools/gopath/pkg/mod"
export GOLANGCI_LINT_CACHE="$_pp_root/.tools/lintcache"
export GOTOOLCHAIN=local
export GOFLAGS=-modcacherw
export PATH="$_pp_root/.tools/bin:$GOROOT/bin:$GOPATH/bin:$PATH"
unset _pp_root
