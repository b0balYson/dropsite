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
for v in "ubuntu 22.04" "ubuntu 24.04" "ubuntu 26.04" "ubuntu 24.10" "debian 12" "debian 13"; do
	# shellcheck disable=SC2086 # "ID VERSION" as two arguments
	check "os $v" os_supported $v
done
for v in "ubuntu 20.04" "ubuntu 21.10" "debian 11" "centos 9" "ubuntu" "ubuntu x.y" "debian 12.5"; do
	# shellcheck disable=SC2086
	check_not "os $v" os_supported $v
done
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
# --lang wins over DROPSITE_LANG and the locale; DROPSITE_LANG over the locale.
lang_of() { (parse_args "$@" && pick_lang) 2>/dev/null; }
check "--lang ru wins over DROPSITE_LANG and LANG" test "$(DROPSITE_LANG=zh LANG=en_US.UTF-8 LC_ALL='' lang_of --lang ru)" = ru
check "--lang=zh form" test "$(DROPSITE_LANG=en LANG=ru_RU.UTF-8 LC_ALL='' lang_of --lang=zh)" = zh
check "DROPSITE_LANG wins over the locale" test "$(DROPSITE_LANG=zh LANG=ru_RU.UTF-8 LC_ALL='' lang_of)" = zh
check "locale without flag or env" test "$(DROPSITE_LANG='' LANG=ru_RU.UTF-8 LC_ALL='' lang_of)" = ru
for v in de RU "" ru_RU; do
	rc=0
	(parse_args --lang "$v") 2>/dev/null || rc=$?
	check "bad --lang '$v' exits 2" test "$rc" = 2
done
out=$( (parse_args --lang de) 2>&1 || true)
check "bad --lang message names en, ru, zh" sh -c 'printf "%s" "$1" | grep -q "en, ru or zh"' _ "$out"
check "--verbose flag" test "$( (parse_args --verbose && echo "$VERBOSE"))" = 1
check "DROPSITE_VERBOSE=1" test "$( (DROPSITE_VERBOSE=1 parse_args && echo "$VERBOSE"))" = 1
check "quiet by default" test "$( (DROPSITE_VERBOSE='' parse_args && echo "$VERBOSE"))" = 0

# --- admin interface from the internet ----------------------------------------
pa_of() { (parse_args "$@" && printf '%s' "$PUBLIC_ADMIN") 2>/dev/null; }
check "public admin: not decided by default" test "$(DROPSITE_PUBLIC_ADMIN='' pa_of)" = ""
check "public admin: --public-admin" test "$(DROPSITE_PUBLIC_ADMIN='' pa_of --public-admin)" = 1
check "public admin: --no-public-admin" test "$(DROPSITE_PUBLIC_ADMIN='' pa_of --no-public-admin)" = 0
check "public admin: DROPSITE_PUBLIC_ADMIN=1" test "$(DROPSITE_PUBLIC_ADMIN=1 pa_of)" = 1
check "public admin: DROPSITE_PUBLIC_ADMIN=0" test "$(DROPSITE_PUBLIC_ADMIN=0 pa_of)" = 0
check "public admin: the flag wins over the variable" test "$(DROPSITE_PUBLIC_ADMIN=1 pa_of --no-public-admin)" = 0
rc=0
(DROPSITE_PUBLIC_ADMIN=yes parse_args) >/dev/null 2>&1 || rc=$?
check "public admin: DROPSITE_PUBLIC_ADMIN=yes exits 2" test "$rc" = 2
# The question: default No; never asked without a terminal or with --yes.
check "public admin: no answer without a terminal" test "$( (HAVE_TTY=0 ASSUME_YES=0 PUBLIC_ADMIN='' DOMAIN=r3100.com &&
	ask_public_admin && printf '%s' "$PUBLIC_ADMIN"))" = ""
check "public admin: no question with --yes" test "$( (HAVE_TTY=1 ASSUME_YES=1 PUBLIC_ADMIN='' DOMAIN=r3100.com &&
	ask_public_admin && printf '%s' "$PUBLIC_ADMIN"))" = ""
check "public admin: Enter means No" test "$( (HAVE_TTY=1 ASSUME_YES=0 PUBLIC_ADMIN='' DOMAIN=r3100.com &&
	no_yes() { return 1; } && ask_public_admin && printf '%s' "$PUBLIC_ADMIN"))" = 0
check "public admin: y means Yes" test "$( (HAVE_TTY=1 ASSUME_YES=0 PUBLIC_ADMIN='' DOMAIN=r3100.com &&
	no_yes() { return 0; } && ask_public_admin && printf '%s' "$PUBLIC_ADMIN"))" = 1
check "public admin: a flag skips the question" test "$( (HAVE_TTY=1 ASSUME_YES=0 PUBLIC_ADMIN=0 DOMAIN=r3100.com &&
	no_yes() { return 0; } && ask_public_admin && printf '%s' "$PUBLIC_ADMIN"))" = 0
check_not "no_yes: no terminal answers No" sh -c 'HAVE_TTY=0; . "$1"; no_yes q' _ "$ROOT/scripts/install-server.sh"
for l in en ru zh; do
	q=$( ("msgs_$l" && msgf "$M_q_public_admin" r3100.com))
	check "public admin: $l question names the address and defaults to No" sh -c \
		'printf "%s" "$1" | grep -qF "https://admin.r3100.com" && printf "%s" "$1" | grep -qF "[y/N]"' _ "$q"
done
msgs_en

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

# --- the server name ---------------------------------------------------------
# name_for DOMAIN [NAME]: the name chosen without a terminal, in a fresh HOME
# whose ~/.ssh/config already has "Host taken".
name_for() {
	(
		HOME="$WORK/names"
		mkdir -p "$HOME/.ssh"
		printf 'Host taken\n    HostName 192.0.2.9\n' >"$HOME/.ssh/config"
		HAVE_TTY=0 VERBOSE=0 DOMAIN=$1 NAME=${2:-}
		choose_name
		printf '%s' "$NAME"
	)
}
check "name from the domain, no question" test "$(name_for r3100.com)" = r3100
check "name from a subdomain" test "$(name_for sites.example.com)" = sites
check "taken name gets a free alternative" test "$(name_for taken.example.com)" = dropsite-taken
check "invalid name from the domain becomes dropsite" test "$(name_for 123.example.com)" = dropsite
check "--name wins over the domain" test "$(name_for r3100.com myvps)" = myvps
check "--name is lowercased" test "$(name_for r3100.com MyVps)" = myvps
rc=0
(name_for r3100.com Bad_Name) >/dev/null 2>&1 || rc=$?
check "invalid --name without a terminal exits 2" test "$rc" = 2
rc=0
(name_for r3100.com taken) >/dev/null 2>&1 || rc=$?
check "taken --name without a terminal exits 2" test "$rc" = 2

# --- whole runs with the server stubbed ------------------------------------------
# sim LANG MODE ARGS...: run main against stubs (no network, no server) with a
# fresh HOME; MODE ok or fail. Output in $WORK/sim-LANG-MODE.out, exit code
# in .rc, HOME in sim-LANG-MODE.home.
SIM_PW=SimPass0123456789abcdefgh
SIM_CODE=SIMC0DE0123AB
sim() {
	l=$1 mode=$2
	shift 2
	h="$WORK/sim-$l-$mode.home"
	rm -rf "$h" "$WORK/sim-$l-$mode.kssh"
	mkdir -p "$h"
	rc=0
	(
		HOME=$h
		export HOME DROPSITE_LANG="$l" DROPSITE_NO_TTY=1 DROPSITE_BOOTSTRAP="$ROOT/scripts/server-bootstrap.sh"
		export SIM_MODE="$mode"
		key_works() { true; }
		port_listening() { false; }
		points_to() { true; }
		resolve() { :; }
		https_works() { true; }
		kssh() {
			printf '%s\n' "$*" >>"$WORK/sim-$l-$mode.kssh"
			case "$*" in
			*"cat >/root/dropsite-bootstrap.sh"*) cat >/dev/null ;;
			*"bash /root/dropsite-bootstrap.sh"*)
				printf '==> Installing base packages\n==> Adding swap (the server has 961 MB of memory)\n==> Writing /opt/dropsite/deploy/.env\n' >&2
				if [ "$SIM_MODE" = fail ]; then
					printf 'E: Unable to locate package docker-ce\nerror: command failed: apt-get (full log: /var/log/dropsite-install.log)\n' >&2
					return 1
				fi
				# Even if the new password leaked into the progress stream, the
				# installer scrubs it from the log once it knows it.
				printf 'note: leaked %s\n==> Dropsite is running\n' "$SIM_PW" >&2
				printf 'DROPSITE_RESULT domain=r3100.com\nDROPSITE_RESULT api_host=api.r3100.com\n'
				printf 'DROPSITE_RESULT admin_email=me@example.com\nDROPSITE_RESULT ipv4=186.243.218.235\nDROPSITE_RESULT ipv6=\n'
				printf 'DROPSITE_RESULT admin_password=%s\nDROPSITE_RESULT admin_password_new=1\n' "$SIM_PW"
				printf 'DROPSITE_RESULT connect_url=https://api.r3100.com/mcp/c/%s\n' "$SIM_CODE"
				case "$*" in
				*"DROPSITE_PUBLIC_ADMIN='1'"*) printf 'DROPSITE_RESULT public_admin_url=https://admin.r3100.com\n' ;;
				*) printf 'DROPSITE_RESULT public_admin_url=\n' ;;
				esac
				;;
			*os-release*) printf 'ubuntu 24.04' ;;
			*) : ;;
			esac
		}
		main --host 186.243.218.235 --domain r3100.com --email me@example.com --yes "$@"
	) >"$WORK/sim-$l-$mode.out" 2>&1 </dev/null || rc=$?
	printf '%s' "$rc" >"$WORK/sim-$l-$mode.rc"
}
has() { grep -qF -- "$2" "$WORK/sim-$1.out"; }
hasnt() { ! grep -qiE -- "$2" "$WORK/sim-$1.out"; }
simlog() { printf '%s' "$WORK/sim-$1.home/.dropsite/install-r3100.log"; }

if command -v ssh-keygen >/dev/null 2>&1; then
	sim en ok
	check "run: exits 0" test "$(cat "$WORK/sim-en-ok.rc")" = 0
	check "run: short output (<= 20 non-empty lines)" test "$(grep -c . "$WORK/sim-en-ok.out")" -le 20
	check "run: no technical words on screen" hasnt en-ok 'swap|firewall|\.env|docker|localforward|bootstrap|==>'
	check "run: connect step" has en-ok "✓ Connected to the server"
	check "run: install step" has en-ok "✓ Installed Dropsite"
	check "run: HTTPS step" has en-ok "✓ Turned on HTTPS"
	check "run: two A records" test "$(grep -c '^  A      [@*] ' "$WORK/sim-en-ok.out")" = 2
	check "run: Cloudflare hint" has en-ok 'DNS only'
	check "run: name from the domain in the final block" has en-ok "1. Run: ssh r3100 "
	check "run: password shown once" test "$(grep -c "$SIM_PW" "$WORK/sim-en-ok.out")" = 1
	check "run: ssh config block for the derived name" grep -q '^Host r3100$' "$WORK/sim-en-ok.home/.ssh/config"
	lg=$(simlog en-ok)
	check "log: written" test -s "$lg"
	check "log: mode 600" test "$(ls -l "$lg" | cut -c1-10)" = -rw-------
	check "log: directory 700" test "$(ls -ld "$(dirname "$lg")" | cut -c1-10)" = drwx------
	check "log: has the server output" grep -q '^==> Adding swap' "$lg"
	check "log: has the technical lines" grep -q 'admin interface on local port' "$lg"
	check "log: never the admin password" sh -c '! grep -q "$1" "$2"' _ "$SIM_PW" "$lg"
	check "run: connect URL shown once" test "$(grep -c "mcp/c/$SIM_CODE" "$WORK/sim-en-ok.out")" = 1
	check "log: never the connect URL code" sh -c '! grep -q "$1" "$2"' _ "$SIM_CODE" "$lg"
	check "log: no private key" sh -c '! grep -q "PRIVATE KEY" "$1"' _ "$lg"
	check "run: no public admin choice passed by default" sh -c '! grep -q DROPSITE_PUBLIC_ADMIN "$1"' _ "$WORK/sim-en-ok.kssh"
	check "run: no public address by default" hasnt en-ok 'admin\.r3100\.com'

	sim en public --public-admin
	check "public: exits 0" test "$(cat "$WORK/sim-en-public.rc")" = 0
	check "public: the choice reaches the server" grep -qF "DROPSITE_PUBLIC_ADMIN='1'" "$WORK/sim-en-public.kssh"
	check "public: the final block shows the HTTPS address" has en-public "Or from anywhere: https://admin.r3100.com"
	check "public: the SSH tunnel stays" has en-public "1. Run: ssh r3100 "
	check "public: the tunnel is configured" grep -q 'LocalForward' "$WORK/sim-en-public.home/.ssh/config"
	sim en private --no-public-admin
	check "private: the choice reaches the server" grep -qF "DROPSITE_PUBLIC_ADMIN='0'" "$WORK/sim-en-private.kssh"
	check "private: output as without the question" test "$(grep -vc 'Log\|log' "$WORK/sim-en-private.out")" = "$(grep -vc 'Log\|log' "$WORK/sim-en-ok.out")"
	check "private: no public address" hasnt en-private 'admin\.r3100\.com'

	sim en verbose --verbose
	check "verbose: exits 0" test "$(cat "$WORK/sim-en-verbose.rc")" = 0
	check "verbose: server steps shown" has en-verbose "==> Adding swap"
	check "verbose: summary shown" has en-verbose "We are going to install:"
	check "verbose: full final block" has en-verbose "ssh r3100 dropsite-update"
	check "verbose: log path shown" has en-verbose "install-r3100.log"
	check "verbose: connect URL with its command" has en-verbose "ssh r3100 dropsite-connect"
	check "verbose: log never has the connect URL code" sh -c '! grep -q "$1" "$2"' _ "$SIM_CODE" "$(simlog en-verbose)"

	sim en fail
	check "failure: exits 1" test "$(cat "$WORK/sim-en-fail.rc")" = 1
	check "failure: plain sentence" has en-fail "The installation on the server stopped with an error."
	check "failure: step marked" has en-fail "✗ Installing Dropsite on the server"
	check "failure: last log lines" has en-fail "  error: command failed: apt-get"
	check "failure: log path" has en-fail "Full log: $(simlog en-fail)"
	check "failure: run again" has en-fail "Run the same command again: finished steps are skipped."
	check "failure: no password in the log" sh -c '! grep -q "$1" "$2"' _ "$SIM_PW" "$(simlog en-fail)"

	sim ru ok
	check "ru: exits 0" test "$(cat "$WORK/sim-ru-ok.rc")" = 0
	check "ru: steps in Russian" has ru-ok "✓ Установил Dropsite"
	check "ru: final block in Russian" has ru-ok "1. Выполните: ssh r3100"
	check "ru: connect URL in Russian" has ru-ok "Подключить чат (бета)"
	sim zh ok
	check "zh: final block in Chinese" has zh-ok "1. 运行：ssh r3100"
	check "zh: connect URL in Chinese" has zh-ok "连接聊天应用（测试版）"
	if [ -n "${SHOW_SIM:-}" ]; then
		for f in en-ok ru-ok en-fail; do
			printf '\n----- %s -----\n' "$f"
			sed "s/$SIM_PW/<password>/" "$WORK/sim-$f.out"
		done
	fi
fi

# --- server-bootstrap.sh: the first administrator password ---------------------
# The bootstrap removes ADMIN_PASSWORD from .env after the first healthy start
# and recreates control; docker compose is stubbed here.
if command -v bash >/dev/null 2>&1; then
	app="$WORK/app"
	mkdir -p "$app/deploy"
	envf="$app/deploy/.env"
	printf 'BASE_DOMAIN=example.com\nADMIN_EMAIL=me@example.com\nADMIN_PASSWORD=Secret123456789abcdef\nUI_URL=\n' >"$envf"
	chmod 600 "$envf"
	out=$(DROPSITE_BOOTSTRAP_LIB=1 DROPSITE_DIR="$app" bash -c '
		. "$1"
		WORK=$2
		LOG=$WORK/bootstrap.log
		compose() { printf "compose %s\n" "$*" >>"$WORK/compose.calls"; }
		forget_admin_password 2>/dev/null
		forget_admin_password 2>/dev/null
		get_env BASE_DOMAIN
	' _ "$ROOT/scripts/server-bootstrap.sh" "$WORK") || out="bash failed"
	check "bootstrap: ADMIN_PASSWORD removed from .env" sh -c '! grep -q "^ADMIN_PASSWORD=" "$1"' _ "$envf"
	check "bootstrap: other settings kept" test "$out" = example.com
	check "bootstrap: .env stays 600" test "$(ls -l "$envf" | cut -c1-10)" = -rw-------
	check "bootstrap: control recreated once" test "$(grep -c 'up -d --no-build --wait .* control$' "$WORK/compose.calls")" = 1
	check "bootstrap: the password is not logged" sh -c '! grep -q Secret123456789abcdef "$1" 2>/dev/null' _ "$WORK/bootstrap.log"

	# The public admin answer is applied once through control, never via .env.
	pa_calls() { # pa_calls VALUE: the compose calls apply_public_admin makes
		rm -f "$WORK/pa.calls"
		DROPSITE_BOOTSTRAP_LIB=1 DROPSITE_DIR="$app" DROPSITE_PUBLIC_ADMIN="$1" bash -c '
			. "$1"
			WORK=$2
			LOG=$WORK/bootstrap.log
			compose() { printf "compose %s\n" "$*" >>"$WORK/pa.calls"; }
			apply_public_admin 2>/dev/null
		' _ "$ROOT/scripts/server-bootstrap.sh" "$WORK"
		cat "$WORK/pa.calls" 2>/dev/null || true
	}
	check "bootstrap: public admin on" test "$(pa_calls 1)" = "compose exec -T control /control public-admin on"
	check "bootstrap: public admin off" test "$(pa_calls 0)" = "compose exec -T control /control public-admin off"
	check "bootstrap: no answer, no change" test -z "$(pa_calls '')"
	check "bootstrap: the answer is not stored in .env" sh -c '! grep -q PUBLIC_ADMIN "$1"' _ "$envf"
	rc=0
	DROPSITE_BOOTSTRAP_LIB=1 DROPSITE_PUBLIC_ADMIN=yes bash -c '. "$1"; validate_inputs' _ "$ROOT/scripts/server-bootstrap.sh" >/dev/null 2>&1 || rc=$?
	check "bootstrap: invalid DROPSITE_PUBLIC_ADMIN refused" test "$rc" != 0
	url=$(DROPSITE_BOOTSTRAP_LIB=1 DROPSITE_DIR="$app" bash -c '
		. "$1"
		compose() { echo on; }
		public_admin_url
	' _ "$ROOT/scripts/server-bootstrap.sh")
	check "bootstrap: public address reported while on" test "$url" = https://admin.example.com
	url=$(DROPSITE_BOOTSTRAP_LIB=1 DROPSITE_DIR="$app" bash -c '
		. "$1"
		compose() { echo off; }
		public_admin_url
	' _ "$ROOT/scripts/server-bootstrap.sh")
	check "bootstrap: no public address while off" test -z "$url"
fi

# --- dropsite-update-watch: the update request from the web interface ---------
# The host job against a request file a compromised control could have
# written: only version=X.Y.Z of an existing newer tag and admin=<id> pass;
# nothing from the file runs. docker, setpriv, flock and timeout are stubbed;
# dropsite-update is a stub that records its arguments.
if command -v bash >/dev/null 2>&1 && command -v git >/dev/null 2>&1; then
	uw="$WORK/update-watch"
	mkdir -p "$uw/bin" "$uw/stub" "$uw/volume" "$uw/app/deploy"
	git -C "$uw/app" init -q
	git -C "$uw/app" -c user.name=t -c user.email=t@example.invalid commit -q --allow-empty -m init
	git -C "$uw/app" tag v0.4.0
	git -C "$uw/app" tag v0.5.0
	git -C "$uw/app" tag v0.5.0-rc.1
	DROPSITE_BOOTSTRAP_LIB=1 DROPSITE_DIR="$uw/app" bash -c '
		. "$1"
		BIN_DIR=$2/bin CRON_FILE=$2/cron-backup UPDATE_CRON_FILE=$2/cron-update SYSTEMD_DIR=$2/systemd
		install_update_unit() { :; }
		install_helpers 2>/dev/null
	' _ "$ROOT/scripts/server-bootstrap.sh" "$uw"
	check "watch: installed" test -x "$uw/bin/dropsite-update-watch"
	check "watch: cron every minute" grep -q '^\* \* \* \* \* root /usr/local/bin/dropsite-update-watch' "$uw/cron-update"
	cat >"$uw/stub/docker" <<'EOF'
#!/bin/sh
case "$1 $2" in
"volume inspect") printf '%s\n' "$UW_VOLUME" ;;
"compose ps") echo ctr1 ;;
"exec ctr1") echo "dropsite control $UW_CURRENT" ;;
*) exit 1 ;;
esac
EOF
	cat >"$uw/stub/setpriv" <<'EOF'
#!/bin/sh
while [ "$1" != -- ]; do shift; done
shift
exec "$@"
EOF
	printf '#!/bin/sh\nexit 0\n' >"$uw/stub/flock"
	printf '#!/bin/sh\nshift\nexec "$@"\n' >"$uw/stub/timeout"
	cat >"$uw/dropsite-update" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$UW_CALLS"
printf '==> Backing up\nquote " backslash \\ tab\there\n'
exit "${UW_RC:-0}"
EOF
	chmod +x "$uw/stub/"* "$uw/dropsite-update"
	req="$uw/volume/update/request"
	st="$uw/volume/update/status"
	# watch REQUEST [CURRENT]: write the request (no file when "-"), run the job.
	watch() {
		rm -f "$st" "$uw/calls"
		mkdir -p "$uw/volume/update"
		[ "$1" = - ] || printf '%b' "$1" >"$req"
		PATH="$uw/stub:$PATH" UW_VOLUME="$uw/volume" UW_CURRENT="${2:-0.4.0}" UW_CALLS="$uw/calls" \
			DROPSITE_UPDATE_BIN="$uw/dropsite-update" DROPSITE_UPDATE_LOG="$uw/update.log" \
			DROPSITE_UPDATE_LOCK="$uw/lock" bash "$uw/bin/dropsite-update-watch" >/dev/null 2>&1
	}
	state() { sed -n 's/^{"state":"\([a-z]*\)".*/\1/p' "$st" 2>/dev/null; }
	valid_json() {
		if command -v python3 >/dev/null 2>&1; then python3 -c 'import json,sys; json.load(open(sys.argv[1]))' "$1"; else test -s "$1"; fi
	}

	watch -
	check "watch: heartbeat written" grep -Eq '^[0-9]{9,}$' "$uw/volume/update/heartbeat"
	check "watch: no request, no status" test ! -e "$st"
	check "watch: no request, no update" test ! -e "$uw/calls"

	watch 'version=0.5.0\nadmin=7\n'
	check "watch: valid request runs dropsite-update --to 0.5.0" test "$(cat "$uw/calls" 2>/dev/null)" = "--to 0.5.0"
	check "watch: request removed" test ! -e "$req"
	check "watch: status done" test "$(state)" = "done"
	check "watch: status is valid JSON with escaped log lines" valid_json "$st"
	check "watch: status names the versions" grep -q '"from":"0.4.0","to":"0.5.0","admin":"7"' "$st"
	check "watch: status keeps the last log lines" grep -q 'Backing up' "$st"
	check "watch: log written" grep -q 'update to 0.5.0 done' "$uw/update.log"
	watch 'version=0.5.0\nadmin=7'
	check "watch: request without a final newline accepted" test "$(state)" = "done"

	UW_RC=3
	export UW_RC
	watch 'version=0.5.0\nadmin=7\n'
	unset UW_RC
	check "watch: failed update reported" test "$(state)" = failed
	check "watch: failed status is valid JSON" valid_json "$st"

	for bad in \
		'version=0.5.0;touch PWNED\nadmin=7\n' \
		'version=$(touch PWNED)\nadmin=7\n' \
		'version=`touch PWNED`\nadmin=7\n' \
		'version=0.5.0\nadmin=7;touch PWNED\n' \
		'version=0.5.0 \nadmin=7\n' \
		'version=v0.5.0\nadmin=7\n' \
		'version=0.5.0-rc.1\nadmin=7\n' \
		'version=0.5\nadmin=7\n' \
		'version=9.9.9\nadmin=7\n' \
		'version=0.4.0\nadmin=7\n' \
		'version=0.3.9\nadmin=7\n' \
		'version=0.5.0\n' \
		'version=0.5.0\nadmin=0\n' \
		'version=0.5.0\nadmin=-1\n' \
		'version=0.5.0\nversion=0.5.0\n' \
		'version=0.5.0\nadmin=7\nextra=1\n' \
		'admin=7\nversion=0.5.0\nadmin=8\n' \
		'#!/bin/sh\ntouch PWNED\n' \
		'' \
		'version=0.5.0\r\nadmin=7\r\n'; do
		(cd "$uw" && watch "$bad")
		check "watch: refused $(printf '%s' "$bad" | tr '\n' ' ' | cut -c1-40)" sh -c '
			[ ! -e "$1/calls" ] && [ ! -e "$1/PWNED" ] && [ ! -e "$1/volume/update/PWNED" ] && [ ! -e "$2" ] &&
				grep -q "^{\"state\":\"failed\"" "$3"' _ "$uw" "$req" "$st"
	done
	check "watch: refusals are valid JSON" valid_json "$st"
	watch 'version=0.5.0\nadmin=7\n' dev+abc123
	check "watch: a development build may update to a release" test "$(state)" = "done"
	watch 'version=0.5.0\nadmin=7\n' 0.5.0
	check "watch: the running version is refused" test "$(state)" = failed

	# The request is read and removed as control's user, never followed by
	# root: a link to another file is just refused content here.
	ln -s "$uw/app/.git/HEAD" "$uw/volume/update/request"
	watch -
	check "watch: a link as the request is refused" test "$(state)" = failed
	check "watch: the link target is untouched" test -e "$uw/app/.git/HEAD"

	for args in "--to 1.2" "--to 1.2.3;id" "--to" "--bogus" "--to v1.2.3" "--to=1.2.3-rc.1"; do
		rc=0
		# shellcheck disable=SC2086 # the arguments are split on purpose
		bash "$uw/bin/dropsite-update" $args >/dev/null 2>&1 || rc=$?
		check "dropsite-update refuses '$args'" test "$rc" = 2
	done
fi

if [ "$FAILS" != 0 ]; then
	printf '\n%s check(s) failed\n' "$FAILS"
	exit 1
fi
printf '\nall installer unit checks passed\n'
