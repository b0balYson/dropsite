#!/usr/bin/env bash
# Runs control natively (no Docker) for UI and API development.
# Data lives in .tools/devdata (sites volume and state); delete it to start over.
#   UI:  http://127.0.0.1:18082   login admin@dropsite.localhost / local-dev-password
#   API: http://127.0.0.1:18081/api/v1 (no Caddy in front)
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=/dev/null
source "$root/scripts/dev-env.sh"
data="${DROPSITE_DEV_DATA:-$root/.tools/devdata}"
mkdir -p "$data/sites" "$data/state"
export BASE_DOMAIN="${BASE_DOMAIN:-dropsite.localhost}"
export ADMIN_EMAIL="${ADMIN_EMAIL:-admin@dropsite.localhost}"
export ADMIN_PASSWORD="${ADMIN_PASSWORD:-local-dev-password}"
export SITES_DIR="$data/sites" STATE_DIR="$data/state"
export PUBLIC_ADDR="${PUBLIC_ADDR:-127.0.0.1:18081}"
export INTERNAL_ADDR="${INTERNAL_ADDR:-127.0.0.1:18083}"
export ADMIN_ADDR="${ADMIN_ADDR:-127.0.0.1:18082}"
export UI_URL="${UI_URL:-http://127.0.0.1:18082}"
export SITES_HTTPS_PORT="${SITES_HTTPS_PORT:-18443}"
export LOG_LEVEL="${LOG_LEVEL:-debug}"
exec go run "$root/cmd/control" serve
