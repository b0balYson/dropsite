#!/bin/sh
# Print the version of this checkout, the single rule for every build:
#   X.Y.Z          HEAD is exactly the tag vX.Y.Z (or vX.Y.Z-pre) and the tree is clean
#   dev+<sha>      any other commit
#   dev+<sha>.dirty  uncommitted changes
#   dev            no Git metadata at all
# Release builds in CI pass the tag explicitly; this script covers source builds.
set -eu

cd "$(dirname "$0")/.."
if ! sha=$(git rev-parse --short=7 HEAD 2>/dev/null); then
	echo dev
	exit 0
fi
dirty=""
git diff --quiet HEAD -- 2>/dev/null || dirty=".dirty"
tag=$(git describe --tags --exact-match --match 'v[0-9]*' HEAD 2>/dev/null || true)
if [ -n "$tag" ] && [ -z "$dirty" ] &&
	printf '%s\n' "$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'; then
	echo "${tag#v}"
else
	echo "dev+$sha$dirty"
fi
