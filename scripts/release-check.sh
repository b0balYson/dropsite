#!/bin/sh
# Release consistency check, run from the repository root (make release-check,
# CI on every push, and the release workflow before anything is published).
#
#   scripts/release-check.sh            the tree as it is (no release)
#   scripts/release-check.sh v1.4.2     the tree that is being released as v1.4.2
#
# In GitHub Actions a tag push supplies the tag through GITHUB_REF_NAME.
# The single source of the version is the Git tag vX.Y.Z; everything else must
# either follow it or stay on the moving markers:
#   - deploy/.env.example has DROPSITE_VERSION=latest;
#   - the compose files name the release images by ${DROPSITE_VERSION:-latest};
#   - brand.Version defaults to the dev marker "dev" (ldflags set the rest);
#   - no doc, script or deploy file hardcodes a release version, except the
#     one being released;
#   - CHANGELOG.md has an [Unreleased] section, and a section for the release.
# shellcheck disable=SC2016 # ${DROPSITE_VERSION...} is literal compose text
set -eu

SEMVER_RE='[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?'
fail=0

problem() {
	printf 'release-check: %s\n' "$*" >&2
	fail=1
}

tag=${1:-}
if [ -z "$tag" ] && [ "${GITHUB_REF_TYPE:-}" = tag ]; then
	tag=${GITHUB_REF_NAME:-}
fi
version=""
if [ -n "$tag" ]; then
	if printf '%s\n' "$tag" | grep -Eq "^v$SEMVER_RE\$"; then
		version=${tag#v}
	else
		problem "tag $tag is not vX.Y.Z (optionally with a pre-release suffix such as -rc.1)"
	fi
fi

# 1. The release channel of new installations.
if [ "$(grep -c '^DROPSITE_VERSION=' deploy/.env.example 2>/dev/null || true)" != 1 ] ||
	! grep -qx 'DROPSITE_VERSION=latest' deploy/.env.example; then
	problem "deploy/.env.example must contain exactly one line DROPSITE_VERSION=latest"
fi

# 2. Compose names the release images through DROPSITE_VERSION.
grep -q 'image: ghcr.io/b0balyson/dropsite-control:${DROPSITE_VERSION:-latest}$' deploy/docker-compose.yml ||
	problem 'deploy/docker-compose.yml: control must use image ghcr.io/b0balyson/dropsite-control:${DROPSITE_VERSION:-latest}'
grep -q 'image: ghcr.io/b0balyson/dropsite-caddy:${DROPSITE_VERSION:-latest}$' deploy/docker-compose.yml ||
	problem 'deploy/docker-compose.yml: caddy must use image ghcr.io/b0balyson/dropsite-caddy:${DROPSITE_VERSION:-latest}'

# 3. Source builds without ldflags report the dev marker.
grep -qx 'var Version = "dev"' internal/brand/brand.go ||
	problem 'internal/brand/brand.go: the default must stay: var Version = "dev"'

# 4. Hardcoded release versions in docs, scripts and deploy files.
pattern="(dropsite-(control|caddy):v?|DROPSITE_VERSION=v?|releases/download/v|--branch v|checkout v|dropsite v)$SEMVER_RE"
files=$(git ls-files -- '*.md' '*.sh' '*.ps1' '*.yml' '*.yaml' '*Dockerfile' 'Makefile' '*.env.example' '*.env' |
	grep -v -e '^CHANGELOG\.md$' -e '^test/release/' -e '^scripts/release-check\.sh$' || true)
if [ -n "$files" ]; then
	# shellcheck disable=SC2086 # one tracked path per word, no spaces in them
	hits=$(grep -EnoH "$pattern" $files 2>/dev/null || true)
	old_ifs=$IFS
	IFS='
'
	for hit in $hits; do
		found=$(printf '%s\n' "$hit" | grep -Eo "$SEMVER_RE\$")
		[ -n "$version" ] && [ "$found" = "$version" ] && continue
		problem "hardcoded version $found in ${hit%%:*}:$(printf '%s' "$hit" | cut -d: -f2) (use latest, X.Y.Z or <version>)"
	done
	IFS=$old_ifs
fi

# 5. The changelog.
if [ ! -f CHANGELOG.md ]; then
	problem "CHANGELOG.md is missing"
else
	grep -q '^## \[Unreleased\]' CHANGELOG.md || problem "CHANGELOG.md has no '## [Unreleased]' section"
	if [ -n "$version" ]; then
		escaped=$(printf '%s' "$version" | sed 's/\./\\./g')
		grep -Eq "^## \[$escaped\] - [0-9]{4}-[0-9]{2}-[0-9]{2}\$" CHANGELOG.md ||
			problem "CHANGELOG.md has no section '## [$version] - YYYY-MM-DD' (make changelog V=$version)"
	fi
fi

if [ "$fail" != 0 ]; then
	exit 1
fi
printf 'release-check: ok%s\n' "${version:+ (release $version)}"
