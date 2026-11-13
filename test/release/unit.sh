#!/bin/sh
# Checks of the release scripts against throwaway Git repositories: version
# string, release notes grouping, CHANGELOG generation and the release
# consistency check. Run: make release-test
# shellcheck disable=SC2016 # literal ${DROPSITE_VERSION...} in fixtures
set -eu

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT INT TERM

FAILS=0
ok() { printf 'ok   %s\n' "$1"; }
fail() {
	printf 'FAIL %s\n' "$1"
	FAILS=$((FAILS + 1))
}
check() { # check DESCRIPTION COMMAND...
	d=$1
	shift
	if "$@" >/dev/null 2>&1; then ok "$d"; else fail "$d"; fi
}
check_not() {
	d=$1
	shift
	if "$@" >/dev/null 2>&1; then fail "$d"; else ok "$d"; fi
}
contains() { printf '%s\n' "$1" | grep -qF -- "$2"; }

g() { git -c user.name=test -c user.email=test@example.invalid -c commit.gpgsign=false -c tag.gpgsign=false "$@"; }
commit() { g commit -q --allow-empty -m "$1"; }

# A repository shaped like the real one, so the scripts run unchanged.
REPO=$WORK/repo
mkdir -p "$REPO/scripts" "$REPO/deploy" "$REPO/internal/brand" "$REPO/docs"
cp "$ROOT/scripts/version.sh" "$ROOT/scripts/release-notes.sh" "$ROOT/scripts/changelog.sh" \
	"$ROOT/scripts/release-check.sh" "$REPO/scripts/"
printf 'X=1\nDROPSITE_VERSION=latest\n' >"$REPO/deploy/.env.example"
printf 'services:\n  control:\n    image: ghcr.io/b0balyson/dropsite-control:${DROPSITE_VERSION:-latest}\n' >"$REPO/deploy/docker-compose.yml"
printf 'services:\n  caddy:\n    image: ghcr.io/b0balyson/dropsite-caddy:${DROPSITE_VERSION:-latest}\n' >"$REPO/deploy/compose.wildcard.yml"
printf 'package brand\n\nvar Version = "dev"\n' >"$REPO/internal/brand/brand.go"
printf '# Install\n\nPin a release with DROPSITE_VERSION=X.Y.Z.\n' >"$REPO/docs/install.md"
printf '# Changelog\n\n## [Unreleased]\n\n### Added\n\n- A hand-written note.\n' >"$REPO/CHANGELOG.md"
cd "$REPO"
g init -q -b main
g add -A
commit "feat: initial import"

# --- version.sh ---------------------------------------------------------------
v=$(sh scripts/version.sh)
check "untagged commit is dev+<sha>" contains "$v" "dev+$(git rev-parse --short=7 HEAD)"
g tag v0.1.0
check "clean release tag is X.Y.Z" test "$(sh scripts/version.sh)" = 0.1.0
printf '\n' >>docs/install.md
check "dirty tree is marked" contains "$(sh scripts/version.sh)" ".dirty"
g checkout -q -- docs/install.md

# --- release-notes.sh -----------------------------------------------------------
commit "feat(cli): add the version hint"
commit "fix: keep the old symlink on failure"
commit "perf(store): batch inserts"
commit "docs: explain releases"
commit "chore: bump tools"
commit "ci: pin actions"
commit "feat(api)!: drop the v0 endpoint"
g commit -q --allow-empty -m "refactor: rename the settings key" -m "BREAKING CHANGE: the old key is ignored"
commit "Merge-free subject without a type"
notes=$(sh scripts/release-notes.sh)
check "notes: breaking group" contains "$notes" "### Breaking changes"
check "notes: bang marks breaking" contains "$notes" "- **api:** drop the v0 endpoint"
check "notes: footer marks breaking" contains "$(printf '%s\n' "$notes" | sed -n '/Breaking/,/###/p')" "rename the settings key"
check "notes: feat with scope under Added" contains "$(printf '%s\n' "$notes" | sed -n '/### Added/,/###/p')" "- **cli:** add the version hint"
check "notes: fix under Fixed" contains "$(printf '%s\n' "$notes" | sed -n '/### Fixed/,/###/p')" "keep the old symlink"
check "notes: perf under Changed" contains "$(printf '%s\n' "$notes" | sed -n '/### Changed/,/###/p')" "batch inserts"
check "notes: docs under Documentation" contains "$notes" "### Documentation"
check "notes: non-conventional under Other" contains "$(printf '%s\n' "$notes" | sed -n '/### Other/,$p')" "Merge-free subject"
check_not "notes: chore left out" contains "$notes" "bump tools"
check_not "notes: ci left out" contains "$notes" "pin actions"
check_not "notes: previous release left out" contains "$notes" "initial import"

# --- changelog.sh -----------------------------------------------------------------
check "changelog add" sh scripts/changelog.sh add 0.2.0 2026-11-13
log=$(cat CHANGELOG.md)
check "changelog: release heading" contains "$log" "## [0.2.0] - 2026-11-13"
check "changelog: Unreleased kept on top" test "$(grep -n '^## ' CHANGELOG.md | head -n 1 | cut -d: -f2-)" = "## [Unreleased]"
check "changelog: hand-written note moved into the release" contains "$(sh scripts/changelog.sh show 0.2.0)" "A hand-written note."
check "changelog: generated notes" contains "$(sh scripts/changelog.sh show 0.2.0)" "add the version hint"
check "changelog: compare link" contains "$log" "[0.2.0]: https://github.com/b0balYson/dropsite/compare/v0.1.0...v0.2.0"
check "changelog: Unreleased link" contains "$log" "[Unreleased]: https://github.com/b0balYson/dropsite/compare/v0.2.0...HEAD"
check_not "changelog: no duplicate section" sh scripts/changelog.sh add 0.2.0
check_not "changelog: bad version" sh scripts/changelog.sh add 0.2
check_not "changelog: show unknown" sh scripts/changelog.sh show 9.9.9
g add -A
commit "chore(release): v0.2.0"
g tag v0.2.0

# --- release-check.sh -------------------------------------------------------------
check "release-check: tree" sh scripts/release-check.sh
check "release-check: tag with changelog section" sh scripts/release-check.sh v0.2.0
check_not "release-check: tag without changelog section" sh scripts/release-check.sh v0.3.0
check_not "release-check: malformed tag" sh scripts/release-check.sh 0.2.0
check "release-check: tag from GITHUB_REF_NAME" env GITHUB_REF_TYPE=tag GITHUB_REF_NAME=v0.2.0 sh scripts/release-check.sh
check_not "release-check: GITHUB_REF_NAME without section" env GITHUB_REF_TYPE=tag GITHUB_REF_NAME=v0.4.0 sh scripts/release-check.sh

sed 's/^DROPSITE_VERSION=latest$/DROPSITE_VERSION=0.2.0/' deploy/.env.example >"$WORK/env" && cp "$WORK/env" deploy/.env.example
check_not "release-check: pinned .env.example" sh scripts/release-check.sh
g checkout -q -- deploy/.env.example

printf 'var Version = "latest"\n' >internal/brand/brand.go
check_not "release-check: brand.Version not dev" sh scripts/release-check.sh
g checkout -q -- internal/brand/brand.go

printf 'image: ghcr.io/b0balyson/dropsite-control:0.1.0\n' >>docs/install.md
g add docs/install.md
check_not "release-check: old version in a doc" sh scripts/release-check.sh v0.2.0
g checkout -q HEAD -- docs/install.md
printf 'curl .../releases/download/v0.2.0/SHA256SUMS\n' >>docs/install.md
g add docs/install.md
check "release-check: the released version itself is allowed" sh scripts/release-check.sh v0.2.0
check_not "release-check: but not outside that release" sh scripts/release-check.sh
g checkout -q HEAD -- docs/install.md

sed 's/:-latest}/:-0.2.0}/' deploy/docker-compose.yml >"$WORK/c" && cp "$WORK/c" deploy/docker-compose.yml
check_not "release-check: compose pinned" sh scripts/release-check.sh
g checkout -q -- deploy/docker-compose.yml

# --- server-bootstrap.sh: release selection ----------------------------------------
# The functions of the server script without its last line (main), against a
# clone of the repository above with the tags v0.1.0, v0.2.0 and v0.3.0-rc.1.
commit "feat: next"
g tag v0.3.0-rc.1
g clone -q "$REPO" "$WORK/app"
sed '$d' "$ROOT/scripts/server-bootstrap.sh" >"$WORK/bootstrap-lib.sh"
cat >"$WORK/select.sh" <<'EOF'
# bash select.sh LIB APP WANT [BUILD]: prints "<rc> <DROPSITE_VERSION> <checked-out tag>"
. "$1"
APP_DIR=$2 LOG=$2.log
published_images() { echo ghcr.io/b0balyson/dropsite-control:x; }
get_env() { :; }
export DROPSITE_VERSION=$3 DROPSITE_BUILD=${4:-}
rc=0
select_release || rc=$?
printf '%s %s %s\n' "$rc" "$DROPSITE_VERSION" "$(git -C "$APP_DIR" describe --tags --exact-match 2>/dev/null || echo none)"
EOF
select_in() { bash "$WORK/select.sh" "$WORK/bootstrap-lib.sh" "$WORK/app" "$@" 2>/dev/null || true; }
check "server: latest selects the newest final tag" test "$(select_in latest)" = "0 0.2.0 v0.2.0"
check "server: a pinned version selects its tag" test "$(select_in 0.1.0)" = "0 0.1.0 v0.1.0"
check "server: a v prefix is accepted" test "$(select_in v0.1.0)" = "0 0.1.0 v0.1.0"
check "server: a pre-release can be pinned" test "$(select_in 0.3.0-rc.1)" = "0 0.3.0-rc.1 v0.3.0-rc.1"
check "server: DROPSITE_BUILD=1 keeps the checkout" test "$(select_in latest 1 | cut -d' ' -f1)" = 1
check "server: an unknown release is not selected" test "$(select_in 9.9.9 | cut -c1)" != 0
check "server: a malformed version is not selected" test "$(select_in 1.2 | cut -c1)" != 0

if [ "$FAILS" -gt 0 ]; then
	printf '\n%s check(s) failed\n' "$FAILS"
	exit 1
fi
printf '\nall release checks passed\n'
