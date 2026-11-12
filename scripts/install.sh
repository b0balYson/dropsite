#!/bin/sh
# Install the dropsite CLI from GitHub Releases, without sudo.
#
#   curl -fsSL https://raw.githubusercontent.com/b0balYson/dropsite/main/scripts/install.sh | sh
#
# With a connect code from the "Connect AI" page it also signs in and connects
# the AI assistants found on this computer:
#
#   curl -fsSL .../install.sh | sh -s -- --server https://api.example.com --code ABCD-EFGH-JKMNP
#
# Arguments (or environment variables):
#   --server URL            DROPSITE_SERVER: server to sign in to
#   --code CODE             DROPSITE_CODE: one-time connect code
#
# Environment:
#   DROPSITE_VERSION        release tag to install (default: latest)
#   DROPSITE_INSTALL_DIR    target directory (default: $HOME/.local/bin)
#   DROPSITE_DOWNLOAD_BASE  where binaries and SHA256SUMS live (overrides the
#                           GitHub URL; file:// and http:// work, for testing)
set -eu

REPO="b0balYson/dropsite"
NAME="dropsite"

say() { printf '%s\n' "$*" >&2; }
die() { say "install: $*"; exit 1; }

detect_os() {
	case "$(uname -s)" in
	Linux) echo linux ;;
	Darwin) echo darwin ;;
	MINGW* | MSYS* | CYGWIN*) echo windows ;;
	*) die "unsupported OS: $(uname -s) (use install.ps1 on Windows)" ;;
	esac
}

detect_arch() {
	case "$(uname -m)" in
	x86_64 | amd64) echo amd64 ;;
	aarch64 | arm64) echo arm64 ;;
	*) die "unsupported architecture: $(uname -m)" ;;
	esac
}

download_base() {
	if [ -n "${DROPSITE_DOWNLOAD_BASE:-}" ]; then
		echo "${DROPSITE_DOWNLOAD_BASE%/}"
	elif [ -n "${DROPSITE_VERSION:-}" ] && [ "$DROPSITE_VERSION" != latest ]; then
		echo "https://github.com/$REPO/releases/download/$DROPSITE_VERSION"
	else
		echo "https://github.com/$REPO/releases/latest/download"
	fi
}

# fetch URL DEST
fetch() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --proto '=https,http,file' -o "$2" "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O "$2" "$1"
	else
		die "curl or wget is required"
	fi
}

# sha256 FILE: print the hex digest
sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{print $1}'
	elif command -v openssl >/dev/null 2>&1; then
		openssl dgst -sha256 "$1" | awk '{print $NF}'
	else
		die "sha256sum, shasum or openssl is required to verify the download"
	fi
}

# verify FILE ASSET SUMS
verify() {
	expected=$(awk -v f="$2" '$2 == f || $2 == "*" f {print $1}' "$3")
	[ -n "$expected" ] || die "no checksum for $2 in SHA256SUMS"
	actual=$(sha256 "$1")
	[ "$expected" = "$actual" ] || die "checksum mismatch for $2: expected $expected, got $actual"
}

path_hint() {
	case ":$PATH:" in
	*":$1:"*) ;;
	*)
		say ""
		say "$1 is not in your PATH. Add it, e.g.:"
		say "  echo 'export PATH=\"$1:\$PATH\"' >> ~/.profile   # or ~/.bashrc, ~/.zshrc"
		;;
	esac
}

# parse_args sets server and code from arguments, falling back to the environment.
parse_args() {
	server="${DROPSITE_SERVER:-}"
	code="${DROPSITE_CODE:-}"
	while [ $# -gt 0 ]; do
		case "$1" in
		--server) [ $# -ge 2 ] || die "--server needs a value"; server="$2"; shift 2 ;;
		--server=*) server="${1#--server=}"; shift ;;
		--code) [ $# -ge 2 ] || die "--code needs a value"; code="$2"; shift 2 ;;
		--code=*) code="${1#--code=}"; shift ;;
		*) die "unknown argument: $1" ;;
		esac
	done
	if [ -n "$code" ] && [ -z "$server" ]; then
		die "--code needs --server"
	fi
}

# connect signs in with the connect code and connects the assistants.
connect() {
	bin="$1"
	say ""
	say "Signing in to $server"
	"$bin" login --server "$server" --code "$code" ||
		die "sign-in failed. Press Connect on the Connect AI page again and run the new command (codes work once, for 10 minutes)."
	say ""
	say "Connecting AI assistants"
	"$bin" setup || die "connecting the assistants failed; run '$NAME setup' to retry"
}

main() {
	parse_args "$@"
	os=$(detect_os)
	arch=$(detect_arch)
	ext=""
	[ "$os" = windows ] && ext=".exe"
	asset="$NAME-$os-$arch$ext"
	base=$(download_base)
	dir="${DROPSITE_INSTALL_DIR:-$HOME/.local/bin}"

	tmp=$(mktemp -d 2>/dev/null || mktemp -d -t dropsite)
	trap 'rm -rf "$tmp"' EXIT INT TERM

	say "Downloading $asset from $base"
	fetch "$base/$asset" "$tmp/$asset" || die "download failed: $base/$asset"
	fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" || die "download failed: $base/SHA256SUMS"
	verify "$tmp/$asset" "$asset" "$tmp/SHA256SUMS"

	mkdir -p "$dir" || die "cannot create $dir"
	chmod 0755 "$tmp/$asset"
	mv -f "$tmp/$asset" "$dir/$NAME$ext" || die "cannot write to $dir (set DROPSITE_INSTALL_DIR)"

	say "Installed $("$dir/$NAME$ext" version 2>/dev/null || echo "$NAME") to $dir/$NAME$ext"
	if [ -n "$code" ]; then
		connect "$dir/$NAME$ext"
		path_hint "$dir"
		return
	fi
	path_hint "$dir"
	say ""
	say "Next: press Connect on the Connect AI page of your $NAME web interface and run the command shown there,"
	say "or: $NAME login --server https://api.<your-domain> --token -"
}

main "$@"
