#!/bin/sh
# Install the pushpage CLI from GitHub Releases, without sudo.
#
#   curl -fsSL https://raw.githubusercontent.com/b0balYson/dropsite/main/scripts/install.sh | sh
#
# Environment:
#   PUSHPAGE_VERSION        release tag to install (default: latest)
#   PUSHPAGE_INSTALL_DIR    target directory (default: $HOME/.local/bin)
#   PUSHPAGE_DOWNLOAD_BASE  where binaries and SHA256SUMS live (overrides the
#                           GitHub URL; file:// and http:// work, for testing)
set -eu

REPO="b0balYson/dropsite"
NAME="pushpage"

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
	if [ -n "${PUSHPAGE_DOWNLOAD_BASE:-}" ]; then
		echo "${PUSHPAGE_DOWNLOAD_BASE%/}"
	elif [ -n "${PUSHPAGE_VERSION:-}" ] && [ "$PUSHPAGE_VERSION" != latest ]; then
		echo "https://github.com/$REPO/releases/download/$PUSHPAGE_VERSION"
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

main() {
	os=$(detect_os)
	arch=$(detect_arch)
	ext=""
	[ "$os" = windows ] && ext=".exe"
	asset="$NAME-$os-$arch$ext"
	base=$(download_base)
	dir="${PUSHPAGE_INSTALL_DIR:-$HOME/.local/bin}"

	tmp=$(mktemp -d 2>/dev/null || mktemp -d -t pushpage)
	trap 'rm -rf "$tmp"' EXIT INT TERM

	say "Downloading $asset from $base"
	fetch "$base/$asset" "$tmp/$asset" || die "download failed: $base/$asset"
	fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" || die "download failed: $base/SHA256SUMS"
	verify "$tmp/$asset" "$asset" "$tmp/SHA256SUMS"

	mkdir -p "$dir" || die "cannot create $dir"
	chmod 0755 "$tmp/$asset"
	mv -f "$tmp/$asset" "$dir/$NAME$ext" || die "cannot write to $dir (set PUSHPAGE_INSTALL_DIR)"

	say "Installed $("$dir/$NAME$ext" version 2>/dev/null || echo "$NAME") to $dir/$NAME$ext"
	path_hint "$dir"
	say ""
	say "Next: $NAME login --server https://api.<your-domain> --token -"
}

main "$@"
