#!/bin/sh
# Maintain CHANGELOG.md (Keep a Changelog) in the current directory.
#
#   scripts/changelog.sh add X.Y.Z [YYYY-MM-DD]
#       Add the section of release X.Y.Z before tagging it: the notes of the
#       commits since the previous release tag (scripts/release-notes.sh),
#       after anything written by hand under [Unreleased], which moves into
#       the new section. Also updates the compare links at the bottom.
#   scripts/changelog.sh show X.Y.Z
#       Print the body of the section of X.Y.Z (the GitHub Release notes).
set -eu

REPO_URL=https://github.com/b0balYson/dropsite
FILE=CHANGELOG.md
SEMVER_RE='^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'

die() {
	printf 'changelog: %s\n' "$*" >&2
	exit 1
}

check_version() {
	printf '%s\n' "$1" | grep -Eq "$SEMVER_RE" || die "not a release version (X.Y.Z): $1"
}

has_section() { grep -Eq "^## \[$(printf '%s' "$1" | sed 's/\./\\./g')\]" "$FILE"; }

show() {
	check_version "$1"
	has_section "$1" || die "$FILE has no section for $1"
	awk -v v="$1" '
		index($0, "## [" v "]") == 1 { on = 1; next }
		on && /^## \[/ { exit }
		on && /^\[[^]]+\]: / { exit }
		on { lines[++n] = $0 }
		END {
			s = 1; while (s <= n && lines[s] == "") s++
			e = n; while (e >= s && lines[e] == "") e--
			for (i = s; i <= e; i++) print lines[i]
		}' "$FILE"
}

add() {
	version=$1
	date=${2:-$(date -u +%Y-%m-%d)}
	check_version "$version"
	[ -f "$FILE" ] || die "$FILE is missing"
	grep -q '^## \[Unreleased\]' "$FILE" || die "$FILE has no '## [Unreleased]' section"
	! has_section "$version" || die "$FILE already has a section for $version"
	case "$version" in
	*-*) prev=$(git describe --tags --abbrev=0 --match 'v[0-9]*' HEAD 2>/dev/null || true) ;;
	*) prev=$(git describe --tags --abbrev=0 --match 'v[0-9]*' --exclude 'v*-*' HEAD 2>/dev/null || true) ;;
	esac
	notes=$(mktemp)
	out=$(mktemp)
	trap 'rm -f "$notes" "$out"' EXIT
	if [ -n "$prev" ]; then
		sh "$(dirname "$0")/release-notes.sh" "$prev" HEAD >"$notes"
		compare="$REPO_URL/compare/$prev...v$version"
	else
		# The first release: the whole history.
		sh "$(dirname "$0")/release-notes.sh" HEAD >"$notes"
		compare="$REPO_URL/releases/tag/v$version"
	fi
	awk -v v="$version" -v d="$date" -v notes="$notes" -v compare="$compare" -v repo="$REPO_URL" '
		function flush_release(   line) {
			print "## [" v "] - " d
			print ""
			if (manual != "") printf "%s\n", manual
			while ((getline line < notes) > 0) print line
			print ""
		}
		/^## \[Unreleased\]/ { print; print ""; inunrel = 1; next }
		inunrel && /^## \[/ { flush_release(); inunrel = 0; done = 1 }
		inunrel && /^\[[^]]+\]: / { flush_release(); inunrel = 0; done = 1 }
		inunrel { if ($0 != "" || manual != "") manual = manual $0 "\n"; next }
		/^\[Unreleased\]: / { next }
		/^\[[^]]+\]: / && !links { print "[Unreleased]: " repo "/compare/v" v "...HEAD"; print "[" v "]: " compare; links = 1 }
		{ print }
		END {
			if (inunrel) { flush_release(); done = 1 }
			if (!links) { print "[Unreleased]: " repo "/compare/v" v "...HEAD"; print "[" v "]: " compare }
		}' "$FILE" | awk 'NF || !blank { print } { blank = !NF }' >"$out"
	cat "$out" >"$FILE"
	printf 'changelog: added %s to %s; review it, then commit\n' "$version" "$FILE" >&2
}

[ $# -ge 2 ] || die "usage: changelog.sh add X.Y.Z [YYYY-MM-DD] | show X.Y.Z"
cmd=$1
shift
case "$cmd" in
add) add "$@" ;;
show) show "$1" ;;
*) die "unknown command: $cmd" ;;
esac
