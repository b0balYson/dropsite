#!/bin/sh
# Print release notes in Markdown for the commits of the Git repository in the
# current directory, grouped by their Conventional Commits type:
#
#   scripts/release-notes.sh                 commits since the newest release tag
#   scripts/release-notes.sh v1.4.2          the commits of release v1.4.2
#   scripts/release-notes.sh v1.3.0 v1.4.2   the commits between two refs
#
# With one argument it is the release ref and the previous release tag is
# found automatically (pre-release tags are skipped for a final release).
# Groups: Breaking changes (type! or a BREAKING CHANGE footer), Added (feat),
# Changed (perf, refactor, revert), Fixed (fix), Security (security),
# Documentation (docs), Other (subjects that are not Conventional Commits).
# build, chore, ci, style and test commits are left out.
set -eu

die() {
	printf 'release-notes: %s\n' "$*" >&2
	exit 1
}

# previous_tag REF: the newest v* tag reachable from REF's parent.
previous_tag() {
	exclude=""
	case "$1" in
	v*-*) ;;                # a pre-release: any earlier tag counts
	*) exclude='v*-*' ;; # a final release: compare with the previous final one
	esac
	if [ -n "$exclude" ]; then
		git describe --tags --abbrev=0 --match 'v[0-9]*' --exclude "$exclude" "$1^" 2>/dev/null || true
	else
		git describe --tags --abbrev=0 --match 'v[0-9]*' "$1^" 2>/dev/null || true
	fi
}

case $# in
0)
	to=HEAD
	from=$(git describe --tags --abbrev=0 --match 'v[0-9]*' --exclude 'v*-*' HEAD 2>/dev/null || true)
	;;
1)
	to=$1
	from=$(previous_tag "$1")
	;;
2)
	from=$1
	to=$2
	;;
*) die "usage: release-notes.sh [[FROM] TO]" ;;
esac
git rev-parse -q --verify "$to^{commit}" >/dev/null || die "unknown ref: $to"
if [ -n "$from" ]; then
	git rev-parse -q --verify "$from^{commit}" >/dev/null || die "unknown ref: $from"
	range="$from..$to"
else
	range=$to
fi

breaking=$(git log --no-merges --format='%h' --grep='^BREAKING[ -]CHANGE' "$range" | tr '\n' ' ')

git log --no-merges --reverse --format='%h %s' "$range" | awk -v breaking=" $breaking " '
function add(group, line) { items[group] = items[group] line "\n" }
{
	hash = $1
	subject = substr($0, length(hash) + 2)
	type = "other"; scope = ""; desc = subject; bang = 0
	if (match(subject, /^[a-z]+(\([^)]*\))?!?: /)) {
		head = substr(subject, 1, RLENGTH - 2)
		desc = substr(subject, RLENGTH + 1)
		if (substr(head, length(head), 1) == "!") { bang = 1; head = substr(head, 1, length(head) - 1) }
		type = head
		p = index(head, "(")
		if (p > 0) { type = substr(head, 1, p - 1); scope = substr(head, p + 1, length(head) - p - 1) }
	}
	line = "- " (scope != "" ? "**" scope ":** " : "") desc " (" hash ")"
	if (bang || index(breaking, " " hash " ")) group = "breaking"
	else if (type == "feat") group = "added"
	else if (type == "fix") group = "fixed"
	else if (type == "perf" || type == "refactor" || type == "revert") group = "changed"
	else if (type == "security") group = "security"
	else if (type == "docs") group = "docs"
	else if (type == "build" || type == "chore" || type == "ci" || type == "style" || type == "test") next
	else group = "other"
	add(group, line)
}
END {
	n = split("breaking added changed fixed security docs other", order, " ")
	title["breaking"] = "Breaking changes"; title["added"] = "Added"; title["changed"] = "Changed"
	title["fixed"] = "Fixed"; title["security"] = "Security"; title["docs"] = "Documentation"
	title["other"] = "Other"
	printed = 0
	for (i = 1; i <= n; i++) {
		g = order[i]
		if (items[g] == "") continue
		if (printed) printf "\n"
		printf "### %s\n\n%s", title[g], items[g]
		printed = 1
	}
	if (!printed) print "No user-facing changes."
}'
