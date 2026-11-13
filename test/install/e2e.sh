#!/bin/sh
# End-to-end test of the one-command installer against a DISPOSABLE server:
# a privileged Ubuntu 24.04 container (no systemd, sshd with a root password,
# Docker installed by the bootstrap itself) on the local Docker engine.
#
#   make install-e2e
#
# Never point this at a real server. Everything it creates (container,
# network, volume, image, temporary HOME with keys and ssh config) carries a
# unique name and is removed on exit. DNS cannot resolve in a test, so the
# installer runs with --skip-dns and the stack is checked through the
# server-local ports and the SSH tunnel.
#
# Checks: password sign-in once, dedicated key afterwards; ~/.ssh/config block
# with the next free local port; ssh <name> and the LocalForward tunnel to the
# admin UI; login with the generated password; short default output and the
# local install log (mode 600, no passwords); --verbose; .env and helper commands;
# backup, secrets key copy and cron; dropsite-admin-password; idempotent
# re-run with a key file; dropsite-update (Caddy restart on a Caddyfile change)
# and dropsite-rollback; --check-dns mode.
# shellcheck disable=SC2012,SC2016 # ls for file modes; remote commands in single quotes
set -eu

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
ID="dsi-e2e-$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')"
WORK=$(mktemp -d "${TMPDIR:-/tmp}/$ID.XXXXXX")
IMAGE="$ID-image"
NET="$ID-net"
VOL="$ID-docker"
CTR="$ID-server"
NAME=e2e
DOMAIN=e2e-install.test
EMAIL=admin@e2e-install.test
TUNNEL_PID=""
FAILS=0

log() { printf '\n[e2e] %s\n' "$*" >&2; }
ok() { printf '[e2e] ok   %s\n' "$*" >&2; }
fail() {
	printf '[e2e] FAIL %s\n' "$*" >&2
	FAILS=$((FAILS + 1))
}
check() {
	d=$1
	shift
	if "$@"; then ok "$d"; else fail "$d"; fi
}

cleanup() {
	rc=$?
	[ -z "$TUNNEL_PID" ] || kill "$TUNNEL_PID" 2>/dev/null || true
	if [ "${KEEP:-}" = 1 ]; then
		log "KEEP=1: leaving $CTR, $NET, $VOL, $IMAGE and $WORK"
		exit "$rc"
	fi
	log "cleaning up"
	docker rm -f "$CTR" >/dev/null 2>&1 || true
	docker network rm "$NET" >/dev/null 2>&1 || true
	docker volume rm "$VOL" >/dev/null 2>&1 || true
	docker image rm "$IMAGE" >/dev/null 2>&1 || true
	rm -rf "$WORK"
	exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

free_port() {
	while :; do
		p=$((20000 + $(od -An -N2 -tu2 /dev/urandom | tr -d ' ') % 20000))
		rc=0
		curl -s -o /dev/null --max-time 1 "http://127.0.0.1:$p/" || rc=$?
		[ "$rc" = 7 ] && { printf '%s' "$p"; return; }
	done
}

for t in docker ssh ssh-keygen curl git; do
	command -v "$t" >/dev/null 2>&1 || { echo "e2e: $t is required" >&2; exit 1; }
done
docker info >/dev/null 2>&1 || { echo "e2e: the local Docker engine is not running" >&2; exit 1; }

# --- temporary HOME: the installer must touch nothing of the real user ----
# Keep talking to the same Docker engine (the context lives in the real HOME).
if [ -z "${DOCKER_HOST:-}" ]; then
	DOCKER_HOST=$(docker context inspect --format '{{.Endpoints.docker.Host}}' 2>/dev/null || true)
	[ -z "$DOCKER_HOST" ] || export DOCKER_HOST
fi
export HOME="$WORK/home"
mkdir -p "$HOME/.ssh"
chmod 700 "$HOME/.ssh"
unset SSH_AUTH_SOCK
# Another server already forwards 8082: the installer must pick another port.
cat >"$HOME/.ssh/config" <<'EOF'
Host other-server
    HostName 192.0.2.1
    LocalForward 8082 127.0.0.1:8082
EOF
chmod 600 "$HOME/.ssh/config"
KNOWN="$HOME/.ssh/known_hosts"

# --- snapshot of the working tree as the "GitHub" repository ---------------
log "snapshot of the working tree"
SRC="$WORK/src"
mkdir -p "$SRC"
(cd "$ROOT" && git ls-files -z -co --exclude-standard | xargs -0 tar -cf - 2>/dev/null) | tar -xf - -C "$SRC"
git -C "$SRC" init -q -b main
git -C "$SRC" add -A
git -C "$SRC" -c user.name=e2e -c user.email=e2e@example.invalid commit -q -m snapshot

# --- disposable server -------------------------------------------------------
log "starting the disposable server $CTR"
docker build -q -t "$IMAGE" "$ROOT/test/install" >/dev/null
docker network create "$NET" >/dev/null
docker volume create "$VOL" >/dev/null
SSH_PORT=$(free_port)
docker run -d --name "$CTR" --hostname "$CTR" --privileged --network "$NET" \
	--mount "type=volume,src=$VOL,dst=/var/lib/docker" \
	-p "127.0.0.1:$SSH_PORT:22" "$IMAGE" >/dev/null
ROOT_PASSWORD="pw-$(od -An -N12 -tx1 /dev/urandom | tr -d ' \n')"
printf 'root:%s\n' "$ROOT_PASSWORD" | docker exec -i "$CTR" chpasswd
docker cp "$SRC/." "$CTR:/srv/dropsite-src" >/dev/null
for _ in $(seq 1 30); do
	ssh-keyscan -p "$SSH_PORT" 127.0.0.1 >/dev/null 2>&1 && break
	sleep 1
done

# The root password goes to ssh through SSH_ASKPASS, as a person would type it.
ASKPASS="$WORK/askpass.sh"
printf '#!/bin/sh\nprintf "%%s\\n" "%s"\n' "$ROOT_PASSWORD" >"$ASKPASS"
chmod 700 "$ASKPASS"

installer() {
	env DROPSITE_LANG=en DROPSITE_NO_TTY=1 \
		DROPSITE_REPO_URL=/srv/dropsite-src DROPSITE_REF=main \
		DROPSITE_SKIP_SWAP=1 \
		SSH_ASKPASS="$ASKPASS" SSH_ASKPASS_REQUIRE=force DISPLAY=:0 \
		sh "$ROOT/scripts/install-server.sh" "$@" </dev/null
}

# Output contains the generated password: kept in the work dir only, shown
# with the password masked.
show() { sed -e 's/\(Password: *\)[A-Za-z0-9]\{12,\}/\1********/' "$1" | tail -n "${2:-40}" >&2; }

# --- run 1: root password ---------------------------------------------------
log "run 1: install with the root password (10-20 minutes)"
rc=0
installer --host 127.0.0.1 --port "$SSH_PORT" --name "$NAME" --domain "$DOMAIN" \
	--email "$EMAIL" --skip-dns --yes >"$WORK/run1.log" 2>&1 || rc=$?
show "$WORK/run1.log" 60
check "run 1 exits 0" test "$rc" = 0
[ "$rc" = 0 ] || exit 1
PASSWORD=$(sed -n 's/.*Password: *\([A-Za-z0-9]\{12,\}\).*/\1/p' "$WORK/run1.log" | tail -n 1)
check "generated password shown" test "${#PASSWORD}" -ge 20

CFG="$HOME/.ssh/config"
block() { awk "/^# BEGIN dropsite $NAME\$/,/^# END dropsite $NAME\$/" "$CFG"; }
LOCAL_PORT=$(block | awk '$1 == "LocalForward" { print $2 }')
check "ssh config block written" test "$(grep -c "^# BEGIN dropsite $NAME$" "$CFG")" = 1
check "other server kept" grep -q '^Host other-server$' "$CFG"
check "local port is not the taken 8082 ($LOCAL_PORT)" test -n "$LOCAL_PORT" -a "$LOCAL_PORT" != 8082
block_has() { block | grep -qx "$1"; }
check "Port line" block_has "    Port $SSH_PORT"
check "User root" block_has "    User root"
check "IdentitiesOnly yes" block_has "    IdentitiesOnly yes"
check "dedicated key exists with mode 600" test "$(ls -l "$HOME/.ssh/dropsite_$NAME" | cut -c1-10)" = -rw-------
check "ssh config mode 600" test "$(ls -l "$CFG" | cut -c1-10)" = -rw-------
check "final screen tells ssh <name>" grep -q "ssh $NAME " "$WORK/run1.log"
check "final screen tells the local URL" grep -q "http://localhost:$LOCAL_PORT" "$WORK/run1.log"
check "default output is short (<= 25 lines: $(grep -c . "$WORK/run1.log"))" test "$(grep -c . "$WORK/run1.log")" -le 25
check "default output hides the server steps" sh -c '! grep -qiE "==>|swap|firewall|docker|\\.env|localforward|bootstrap" "$1"' _ "$WORK/run1.log"
INSTALL_LOG="$HOME/.dropsite/install-$NAME.log"
check "install log written with mode 600" test "$(ls -l "$INSTALL_LOG" | cut -c1-10)" = -rw-------
check "install log has the server steps" grep -q '^==> Installing base packages' "$INSTALL_LOG"
check "install log has the stack start" grep -q '^==> dropsite is running' "$INSTALL_LOG"
check "DNS records printed" grep -q "^  A      \\*  " "$WORK/run1.log"

# ssh <name>, exactly as the user types it (plus the temporary known_hosts).
SSH() { ssh -F "$CFG" -o "UserKnownHostsFile=$KNOWN" -o BatchMode=yes "$@"; }
check "ssh $NAME works with the dedicated key, no password" SSH "$NAME" true </dev/null

REMOTE() { SSH "$NAME" "$@" </dev/null; }
remote_out_is() { test "$(REMOTE "$1")" = "$2"; }
remote_out_ge() { test "$(REMOTE "$1")" -ge "$2"; }
remote_grep() { REMOTE "$1" | grep -q -- "$2"; }
check "control is healthy" remote_out_is 'docker inspect -f {{.State.Health.Status}} $(cd /opt/dropsite/deploy && docker compose ps -q control)' healthy
check ".env is 600" test "$(REMOTE stat -c %a /opt/dropsite/deploy/.env)" = 600
check "ADMIN_PASSWORD removed from .env" test "$(REMOTE grep -c ^ADMIN_PASSWORD= /opt/dropsite/deploy/.env || true)" = 0
check "control runs without ADMIN_PASSWORD" test "$(REMOTE 'docker inspect -f "{{range .Config.Env}}{{println .}}{{end}}" $(cd /opt/dropsite/deploy && docker compose ps -q control) | grep -c "^ADMIN_PASSWORD=." || true')" = 0
check "ADMIN_BIND_IP=127.0.0.1" test "$(REMOTE sed -n s/^ADMIN_BIND_IP=//p /opt/dropsite/deploy/.env)" = 127.0.0.1
check "BASE_DOMAIN set" test "$(REMOTE sed -n s/^BASE_DOMAIN=//p /opt/dropsite/deploy/.env)" = "$DOMAIN"
check "UI_URL uses the local port" test "$(REMOTE sed -n s/^UI_URL=//p /opt/dropsite/deploy/.env)" = "http://localhost:$LOCAL_PORT"
check "DROPSITE_VERSION=latest" test "$(REMOTE sed -n s/^DROPSITE_VERSION=//p /opt/dropsite/deploy/.env)" = latest
check "admin UI published on loopback only" remote_out_is 'cd /opt/dropsite/deploy && docker compose port control 8082' 127.0.0.1:8082
for c in dropsite-update dropsite-rollback dropsite-backup dropsite-admin-password dropsite-info; do
	check "$c installed" REMOTE test -x "/usr/local/bin/$c"
done
check "daily backup cron" REMOTE grep -q dropsite-backup /etc/cron.d/dropsite-backup
check "first backup made" remote_out_ge 'ls /var/backups/dropsite | wc -l' 1
check "secrets key copied with mode 600" test "$(REMOTE stat -c %a /root/dropsite-secrets.key)" = 600
check "ufw active" remote_grep 'ufw status' 'Status: active'
check "ufw allows 22" remote_grep 'ufw status' '^22/tcp'
check "ufw allows 80" remote_grep 'ufw status' '^80/tcp'
check "ufw allows 443" remote_grep 'ufw status' '^443/tcp'
check "password not in the install log" remote_out_is "grep -c -F -- '$PASSWORD' /var/log/dropsite-install.log || true" 0
check "password not in the local install log" sh -c '! grep -q -F -- "$1" "$2"' _ "$PASSWORD" "$INSTALL_LOG"
check "root password not in the local install log" sh -c '! grep -q -F -- "$1" "$2"' _ "$ROOT_PASSWORD" "$INSTALL_LOG"

# --- the tunnel and the admin login ------------------------------------------
open_tunnel() {
	SSH -o ExitOnForwardFailure=yes -N "$NAME" </dev/null &
	TUNNEL_PID=$!
	for _ in $(seq 1 20); do
		curl -s -o /dev/null "http://127.0.0.1:$LOCAL_PORT/login" && return 0
		sleep 1
	done
	return 1
}
close_tunnel() {
	kill "$TUNNEL_PID" 2>/dev/null || true
	wait "$TUNNEL_PID" 2>/dev/null || true
	TUNNEL_PID=""
}
# login EMAIL PASSWORD: HTTP status of the sign-in form (303 = signed in).
# The Altcha check is solved by test/install/altchasolve (Go), like the
# widget does in a browser.
login() {
	jar="$WORK/jar"
	rm -f "$jar"
	curl -s -o /dev/null -c "$jar" "http://127.0.0.1:$LOCAL_PORT/login"
	token=$(awk '$6 == "ds_csrf" { print $7 }' "$jar")
	solution=$(cd "$ROOT" && go run ./test/install/altchasolve "http://127.0.0.1:$LOCAL_PORT/login/challenge") || solution=""
	curl -s -o /dev/null -w '%{http_code}' -b "$jar" \
		-H "Origin: http://127.0.0.1:$LOCAL_PORT" \
		--data-urlencode "_csrf=$token" --data-urlencode "email=$1" --data-urlencode "password=$2" \
		--data-urlencode "altcha=$solution" \
		"http://127.0.0.1:$LOCAL_PORT/login"
}

log "tunnel ssh $NAME -> http://localhost:$LOCAL_PORT"
check "tunnel opens the admin UI" open_tunnel
check "login with the generated password" test "$(login "$EMAIL" "$PASSWORD")" = 303
check "wrong password refused" test "$(login "$EMAIL" wrong-password-123)" != 303

log "dropsite-admin-password"
NEWPW=$(REMOTE dropsite-admin-password 2>/dev/null | tail -n 1)
check "new password printed" test "${#NEWPW}" -ge 20
check "old password stops working" test "$(login "$EMAIL" "$PASSWORD")" != 303
check "new password works" test "$(login "$EMAIL" "$NEWPW")" = 303
close_tunnel

# --- run 2: re-run with a key file, idempotent ---------------------------------
log "run 2: re-run with --key and --verbose (idempotent)"
rc=0
installer --host 127.0.0.1 --port "$SSH_PORT" --key "$HOME/.ssh/dropsite_$NAME" --name "$NAME" \
	--domain "$DOMAIN" --email "$EMAIL" --skip-dns --yes --verbose >"$WORK/run2.log" 2>&1 || rc=$?
show "$WORK/run2.log" 25
check "run 2 exits 0" test "$rc" = 0
check "still one block" test "$(grep -c "^# BEGIN dropsite $NAME$" "$CFG")" = 1
check "same local port" test "$(block | awk '$1 == "LocalForward" { print $2 }')" = "$LOCAL_PORT"
check "existing password not reprinted" grep -q 'the one from the first installation' "$WORK/run2.log"
check "--verbose shows the server steps" grep -q '^==> ' "$WORK/run2.log"
check "--verbose final screen tells dropsite-update" grep -q "ssh $NAME dropsite-update" "$WORK/run2.log"
open_tunnel >/dev/null || true
check "reset password still valid after re-run" test "$(login "$EMAIL" "$NEWPW")" = 303
close_tunnel

# --- dropsite-update and dropsite-rollback -----------------------------------
log "dropsite-update with a Caddyfile change"
docker exec "$CTR" sh -c 'cd /srv/dropsite-src && printf "\n# e2e update\n" >>deploy/Caddyfile && git -c user.name=e2e -c user.email=e2e@example.invalid commit -qam "e2e: caddyfile change"'
caddy_started() { REMOTE 'docker inspect -f {{.State.StartedAt}} $(cd /opt/dropsite/deploy && docker compose ps -q caddy)'; }
before=$(caddy_started)
old_commit=$(REMOTE git -C /opt/dropsite rev-parse HEAD)
rc=0
REMOTE dropsite-update >"$WORK/update.log" 2>&1 || rc=$?
tail -n 15 "$WORK/update.log" >&2
check "dropsite-update exits 0" test "$rc" = 0
check "checkout updated" test "$(REMOTE git -C /opt/dropsite rev-parse HEAD)" != "$old_commit"
check "caddy restarted after the Caddyfile change" test "$(caddy_started)" != "$before"
check "rollback record written" REMOTE grep -q "^PREV_COMMIT=$old_commit" /var/lib/dropsite/previous-release

log "dropsite-rollback"
rc=0
REMOTE dropsite-rollback --yes >"$WORK/rollback.log" 2>&1 || rc=$?
tail -n 10 "$WORK/rollback.log" >&2
check "dropsite-rollback exits 0" test "$rc" = 0
check "back on the previous commit" test "$(REMOTE git -C /opt/dropsite rev-parse HEAD)" = "$old_commit"
open_tunnel >/dev/null || true
check "login works after rollback" test "$(login "$EMAIL" "$NEWPW")" = 303
close_tunnel
rc=0
REMOTE dropsite-update >"$WORK/update2.log" 2>&1 || rc=$?
check "dropsite-update after a rollback returns to the branch" test "$rc" = 0 -a "$(REMOTE git -C /opt/dropsite symbolic-ref --short HEAD)" = main

# --- --check-dns mode ---------------------------------------------------------
log "--check-dns (DNS cannot resolve in a test: expect 'not ready', exit 3)"
rc=0
env DROPSITE_LANG=en DROPSITE_NO_TTY=1 DROPSITE_DNS_TIMEOUT=5 \
	sh "$ROOT/scripts/install-server.sh" --check-dns --name "$NAME" </dev/null >"$WORK/dns.log" 2>&1 || rc=$?
show "$WORK/dns.log" 12
check "--check-dns reads the server and exits 3" test "$rc" = 3
check "--check-dns prints the records" grep -q "^  A      @  " "$WORK/dns.log"
check "--check-dns explains how to retry" grep -q -- "--check-dns --name $NAME" "$WORK/dns.log"

if [ "$FAILS" != 0 ]; then
	log "$FAILS check(s) FAILED"
	exit 1
fi
log "all end-to-end checks passed"
