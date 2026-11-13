#!/bin/sh
# Fast checks of scripts/install-server.sh without a server: input
# validation, language detection and ~/.ssh/config editing against a
# temporary HOME. Run: make install-test
# shellcheck disable=SC2012,SC2016,SC2329 # ls for modes; sh -c bodies; stubs
set -eu

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT INT TERM
export HOME="$WORK/home"
mkdir -p "$HOME"

DROPSITE_INSTALLER_LIB=1
export DROPSITE_INSTALLER_LIB
# shellcheck source=scripts/install-server.sh
. "$ROOT/scripts/install-server.sh"
msgs_en

FAILS=0
ok() { printf 'ok   %s\n' "$1"; }
fail() {
	printf 'FAIL %s\n' "$1"
	FAILS=$((FAILS + 1))
}
check() { # check DESCRIPTION COMMAND...
	d=$1
	shift
	if "$@"; then ok "$d"; else fail "$d"; fi
}
check_not() {
	d=$1
	shift
	if "$@"; then fail "$d"; else ok "$d"; fi
}

# --- validation -------------------------------------------------------------
for v in 203.0.113.7 2001:db8::1 vps1.example.com; do check "host $v" valid_host "$v"; done
for v in 256.1.1.1 "1.2.3" "a b" "-x.com" ""; do check_not "bad host '$v'" valid_host "$v"; done
for v in example.com sites.example.co.uk xn--e1afmkfd.xn--p1ai a-b.io; do check "domain $v" valid_domain "$v"; done
for v in example "ex ample.com" -a.com a-.com "a..com" "exa_mple.com" "example.c"; do check_not "bad domain '$v'" valid_domain "$v"; done
for v in me@example.com first.last+tag@sub.example.org; do check "email $v" valid_email "$v"; done
for v in me@ "@example.com" "me@@example.com" "me example@x.com" "me@x" "m'e@example.com"; do check_not "bad email '$v'" valid_email "$v"; done
for v in sites vps-1 a; do check "name $v" valid_name "$v"; done
for v in 1vps Sites "my vps" "" -x aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa; do check_not "bad name '$v'" valid_name "$v"; done
check "port 22" valid_port 22
check_not "port 0" valid_port 0
check_not "port 70000" valid_port 70000
check "normalize domain" test "$(normalize_domain 'HTTPS://Sites.Example.COM./x')" = sites.example.com
check "quote for remote shell" test "$(sh -c "printf '%s' $(q "it's")")" = "it's"

# --- language ---------------------------------------------------------------
check "lang ru" test "$(LANG=ru_RU.UTF-8 LC_ALL='' DROPSITE_LANG='' detect_lang)" = ru
check "lang zh" test "$(LANG=zh_CN.UTF-8 LC_ALL='' DROPSITE_LANG='' detect_lang)" = zh
check "lang LC_ALL wins" test "$(LANG=ru_RU.UTF-8 LC_ALL=en_US.UTF-8 DROPSITE_LANG='' detect_lang)" = en
check "lang default" test "$(LANG=C LC_ALL='' LC_MESSAGES='' DROPSITE_LANG='' detect_lang)" = en
# Every language defines every message of the English table.
keys=$(sed -n 's/^	\(M_[a-z_0-9]*\)=.*/\1/p' "$ROOT/scripts/install-server.sh" | sort -u)
for l in ru zh; do
	missing=$(
		for k in $keys; do
			(
				unset "$k"
				"msgs_$l"
				eval "[ -n \"\${$k:-}\" ]"
			) || printf '%s ' "$k"
		done
	)
	check "messages $l complete ${missing:+(missing: $missing)}" test -z "$missing"
done

# --- ~/.ssh/config ----------------------------------------------------------
cfg="$HOME/.ssh/config"
check_not "no config yet" test -e "$cfg"
ensure_ssh_dir
check "ssh dir is 700" test "$(ls -ld "$HOME/.ssh" | cut -c1-10)" = drwx------
check "config is 600" test "$(ls -l "$cfg" | cut -c1-10)" = -rw-------

cat >"$cfg" <<'EOF'
Host other
    HostName 192.0.2.1
    LocalForward 8082 127.0.0.1:8082

Host second third
    LocalForward 127.0.0.1:8083 localhost:8082

Host *
    User nobody
EOF
check "existing host name is taken" host_taken other
check "alias is taken" host_taken third
check_not "free name" host_taken sites
check "used ports" test "$(used_forward_ports sites | tr '\n' ' ')" = "8082 8083 "

# Something listening on 8084 must be skipped too.
port_listening() { [ "$1" = 8084 ]; }
port=$(choose_local_port sites)
check "first free port is 8085 (8082/8083 forwarded, 8084 listening)" test "$port" = 8085

write_ssh_config sites 203.0.113.7 22 "$HOME/.ssh/dropsite_sites" "$port"
check "block written on top" test "$(head -n 1 "$cfg")" = "# BEGIN dropsite sites"
check "other hosts kept" grep -q '^Host other$' "$cfg"
check "Host * kept after the block" test "$(grep -n '^Host \*' "$cfg" | cut -d: -f1)" -gt "$(grep -n '^# END dropsite sites' "$cfg" | cut -d: -f1)"
check "no Port line for 22" test -z "$(block_value sites Port)"
check "LocalForward" grep -q "^    LocalForward 8085 127.0.0.1:8082$" "$cfg"
check "IdentitiesOnly" grep -q '^    IdentitiesOnly yes$' "$cfg"
check "User root" test "$(block_value sites User)" = root
check "config still 600" test "$(ls -l "$cfg" | cut -c1-10)" = -rw-------

# Re-run: update in place, keep the port, no duplicate block.
port_listening() { true; }
port=$(choose_local_port sites)
check "existing port kept on re-run" test "$port" = 8085
write_ssh_config sites 198.51.100.9 2222 "$HOME/my keys/dropsite_sites" "$port"
check "exactly one block" test "$(grep -c '^# BEGIN dropsite sites$' "$cfg")" = 1
check "HostName updated" test "$(block_value sites HostName)" = 198.51.100.9
check "Port written" test "$(block_value sites Port)" = 2222
check "IdentityFile with space quoted" grep -q "^    IdentityFile \"$HOME/my keys/dropsite_sites\"$" "$cfg"
check "own block is not 'taken'" test "$(host_taken sites && echo y || echo n)" = n

# A second server gets its own block and the next port.
port_listening() { false; }
p2=$(choose_local_port backup)
check "second server skips the first one's port" test "$p2" = 8084
write_ssh_config backup 203.0.113.8 22 "$HOME/.ssh/dropsite_backup" "$p2"
check "two blocks" test "$(grep -c '^# BEGIN dropsite ' "$cfg")" = 2
check "first block intact" test "$(block_value sites HostName)" = 198.51.100.9

# A symlinked config stays a symlink.
mv "$cfg" "$HOME/real-config"
ln -s "$HOME/real-config" "$cfg"
write_ssh_config backup 203.0.113.8 22 "$HOME/.ssh/dropsite_backup" "$p2"
check "symlink kept" test -L "$cfg"

# ssh itself accepts the result.
if command -v ssh >/dev/null 2>&1; then
	out=$(ssh -G -F "$cfg" sites 2>&1) || true
	check "ssh -G: LocalForward" sh -c 'printf "%s\n" "$1" | grep -Eq "^localforward 8085 \[?127\.0\.0\.1\]?:8082$"' _ "$out"
	check "ssh -G: Port" sh -c 'printf "%s\n" "$1" | grep -q "^port 2222$"' _ "$out"
	check "ssh -G: User" sh -c 'printf "%s\n" "$1" | grep -q "^user root$"' _ "$out"
	check "ssh -G: IdentityFile" sh -c 'printf "%s\n" "$1" | grep -q "^identityfile .*my keys/dropsite_sites$"' _ "$out"
fi

if [ "$FAILS" != 0 ]; then
	printf '\n%s check(s) failed\n' "$FAILS"
	exit 1
fi
printf '\nall installer unit checks passed\n'
