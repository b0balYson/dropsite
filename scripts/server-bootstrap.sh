#!/usr/bin/env bash
# Prepare a fresh Ubuntu 22.04/24.04 or Debian 12 server for dropsite and
# start it. Runs on the server as root; install-server.sh uploads
# and runs it over ssh, but it also works on its own:
#
#   DROPSITE_DOMAIN=sites.example.com DROPSITE_EMAIL=me@example.com \
#     bash server-bootstrap.sh
#
# Safe to run again: every step checks what is already there. It installs
# Docker Engine with the compose plugin from Docker's apt repository, adds swap
# on small servers, opens only SSH/80/443 with ufw, clones the repository to
# /opt/dropsite, writes deploy/.env with a generated administrator password,
# starts the stack, installs a daily backup and the helper commands
# dropsite-update, dropsite-rollback, dropsite-backup, dropsite-admin-password
# and dropsite-info.
#
# Modes:
#   (none)              full installation
#   --up                only start the stack (used by dropsite-update)
#   --install-helpers   only (re)install the helper commands and the cron job
#   --info              print the result lines of an existing installation
#
# Environment:
#   DROPSITE_DOMAIN       base domain for sites (required for a new .env)
#   DROPSITE_EMAIL        administrator login and ACME e-mail (required for a new .env)
#   DROPSITE_LOCAL_PORT   local port of the admin SSH tunnel, used for UI_URL
#   DROPSITE_REPO_URL     repository to clone (default: the public GitHub repository)
#   DROPSITE_REF          branch to clone (default: main)
#   DROPSITE_DIR          checkout directory (default: /opt/dropsite)
#   DROPSITE_BUILD=1      always build control from source, even when
#                         published images exist
#   DROPSITE_SKIP_SWAP=1      never create swap (test containers)
#   DROPSITE_SKIP_FIREWALL=1  never touch the firewall (test containers)
#
# Output: progress on stderr; on success the last lines on stdout are
# "DROPSITE_RESULT key=value" (domain, api_host, admin_email, admin_password,
# admin_password_new, ipv4, ipv6) for the local installer. The password line
# goes to stdout only and never into the log file.
set -euo pipefail
umask 022

REPO_URL=${DROPSITE_REPO_URL:-https://github.com/b0balYson/dropsite.git}
REF=${DROPSITE_REF:-main}
APP_DIR=${DROPSITE_DIR:-/opt/dropsite}
LOG=/var/log/dropsite-install.log
BIN_DIR=/usr/local/bin
CRON_FILE=/etc/cron.d/dropsite-backup
COMPOSE_MIN=2.24.4
SWAP_TARGET_MB=3072 # RAM + swap of at least 3 GB: enough to compile control
WAIT_TIMEOUT=900

export DEBIAN_FRONTEND=noninteractive
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

DOMAIN_RE='^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+([a-z]{2,63}|xn--[a-z0-9-]{1,59})$'
EMAIL_RE='^[A-Za-z0-9._%+-]{1,64}@([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+([a-z]{2,63}|xn--[a-z0-9-]{1,59})$'

step() { printf '==> %s\n' "$*" >&2; }
note() { printf '    %s\n' "$*" >&2; }
warn() { printf 'warning: %s\n' "$*" >&2; }
die() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

# run CMD...: run quietly, output into the log; on failure show its tail.
run() {
	printf '\n$ %s\n' "$*" >>"$LOG"
	if ! "$@" >>"$LOG" 2>&1 </dev/null; then
		tail -n 40 "$LOG" >&2
		die "command failed: $1 (full log: $LOG)"
	fi
}

apt_get() { run apt-get -o DPkg::Lock::Timeout=600 -y "$@"; }

check_root() { [ "$(id -u)" = 0 ] || die "run this script as root"; }

# check_os sets OS_ID and OS_CODENAME, refusing unsupported systems.
check_os() {
	[ -r /etc/os-release ] || die "unknown operating system: /etc/os-release is missing"
	# shellcheck disable=SC1091
	read -r OS_ID OS_VERSION OS_CODENAME < <(. /etc/os-release && printf '%s %s %s\n' "$ID" "${VERSION_ID:-}" "${VERSION_CODENAME:-}")
	case "$OS_ID $OS_VERSION" in
	"ubuntu 22.04" | "ubuntu 24.04" | "debian 12") ;;
	*) die "unsupported system: $OS_ID $OS_VERSION (supported: Ubuntu 22.04, Ubuntu 24.04, Debian 12)" ;;
	esac
	case "$(uname -m)" in
	x86_64 | aarch64) ;;
	*) die "unsupported architecture: $(uname -m) (supported: x86-64, arm64)" ;;
	esac
}

install_base_packages() {
	step "Installing base packages"
	apt_get update
	pkgs=(ca-certificates curl git gnupg openssl cron iproute2)
	[ "$OS_ID" = ubuntu ] && [ "${DROPSITE_SKIP_FIREWALL:-}" != 1 ] && pkgs+=(ufw)
	apt_get install --no-install-recommends "${pkgs[@]}"
}

# version_ge A B: true when version A >= B (numeric, dot separated).
version_ge() {
	[ "$(printf '%s\n%s\n' "$2" "$1" | sort -t. -k1,1n -k2,2n -k3,3n | head -n1)" = "$2" ]
}

compose_ok() {
	command -v docker >/dev/null 2>&1 || return 1
	v=$(docker compose version --short 2>/dev/null) || return 1
	version_ge "${v#v}" "$COMPOSE_MIN"
}

start_docker() {
	if [ -d /run/systemd/system ]; then
		run systemctl enable --now docker
	elif ! docker info >/dev/null 2>&1; then
		# No systemd (containers): the SysV script, else dockerd directly.
		if ! service docker start >>"$LOG" 2>&1 </dev/null; then
			setsid nohup dockerd </dev/null >>/var/log/dockerd.log 2>&1 &
		fi
	fi
	for _ in $(seq 1 60); do
		docker info >/dev/null 2>&1 && return 0
		sleep 1
	done
	die "Docker did not start (see $LOG and 'journalctl -u docker')"
}

install_docker() {
	if [ -x /snap/bin/docker ]; then
		die "Docker from snap is installed. Remove it ('snap remove docker') and run this script again."
	fi
	if compose_ok; then
		step "Docker is already installed ($(docker --version))"
		start_docker
		return
	fi
	step "Installing Docker Engine and the compose plugin from download.docker.com"
	install -m 0755 -d /etc/apt/keyrings
	run curl -fsSL "https://download.docker.com/linux/$OS_ID/gpg" -o /etc/apt/keyrings/docker.asc
	chmod a+r /etc/apt/keyrings/docker.asc
	printf 'deb [arch=%s signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/%s %s stable\n' \
		"$(dpkg --print-architecture)" "$OS_ID" "$OS_CODENAME" >/etc/apt/sources.list.d/docker.list
	apt_get update
	apt_get install docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
	start_docker
	compose_ok || die "docker compose $COMPOSE_MIN or newer is required, found: $(docker compose version 2>&1 || true)"
}

ensure_swap() {
	if [ "${DROPSITE_SKIP_SWAP:-}" = 1 ]; then
		return
	fi
	mem_mb=$(($(awk '/^MemTotal:/ {print $2}' /proc/meminfo) / 1024))
	if [ "$mem_mb" -ge 2048 ]; then
		return
	fi
	if [ -n "$(swapon --noheadings --show 2>/dev/null)" ]; then
		note "swap is already on"
		return
	fi
	step "Adding swap (the server has ${mem_mb} MB of memory)"
	if [ ! -f /swapfile ]; then
		size=$(((SWAP_TARGET_MB - mem_mb + 511) / 512 * 512))
		[ "$size" -ge 1024 ] || size=1024
		if ! fallocate -l "${size}M" /swapfile 2>>"$LOG"; then
			run dd if=/dev/zero of=/swapfile bs=1M count="$size"
		fi
		chmod 600 /swapfile
		run mkswap /swapfile
	fi
	if ! swapon /swapfile >>"$LOG" 2>&1; then
		warn "could not turn on swap; the first build may run out of memory"
		return
	fi
	grep -q '^/swapfile ' /etc/fstab || printf '/swapfile none swap sw 0 0\n' >>/etc/fstab
}

configure_firewall() {
	if [ "${DROPSITE_SKIP_FIREWALL:-}" = 1 ]; then
		return
	fi
	if ! command -v ufw >/dev/null 2>&1; then
		note "ufw is not installed: make sure your provider's firewall allows only ports 22, 80 and 443"
		return
	fi
	step "Firewall: allowing SSH, HTTP and HTTPS only"
	# The port of this very SSH session goes first, so enabling can never
	# lock us out, even when sshd listens on a non-standard port.
	ssh_port=22
	if [ -n "${SSH_CONNECTION:-}" ]; then
		ssh_port=$(printf '%s\n' "$SSH_CONNECTION" | awk '{print $4}')
	fi
	if ! {
		ufw allow "$ssh_port/tcp" &&
			ufw allow 22/tcp &&
			ufw allow 80/tcp &&
			ufw allow 443/tcp &&
			ufw allow 443/udp &&
			ufw default deny incoming &&
			ufw default allow outgoing &&
			ufw --force enable
	} >>"$LOG" 2>&1; then
		warn "could not configure ufw (see $LOG); make sure only ports 22, 80 and 443 are open"
	fi
}

checkout() {
	if [ -d "$APP_DIR/.git" ]; then
		step "Updating the dropsite source in $APP_DIR"
		if ! git -C "$APP_DIR" pull --ff-only >>"$LOG" 2>&1 </dev/null; then
			warn "could not update $APP_DIR with 'git pull --ff-only'; continuing with the current version"
		fi
	else
		step "Downloading dropsite to $APP_DIR"
		[ ! -e "$APP_DIR" ] || [ -z "$(ls -A "$APP_DIR")" ] ||
			die "$APP_DIR exists and is not a git checkout; move it away and run again"
		run git clone --branch "$REF" "$REPO_URL" "$APP_DIR"
	fi
	# dropsite-update returns to this branch after a rollback.
	mkdir -p /var/lib/dropsite
	git -C "$APP_DIR" symbolic-ref -q --short HEAD >/var/lib/dropsite/branch 2>/dev/null ||
		printf '%s\n' "$REF" >/var/lib/dropsite/branch
}

env_file() { printf '%s/deploy/.env' "$APP_DIR"; }

# get_env KEY: the value of KEY in .env, without surrounding quotes.
get_env() {
	sed -n "s/^$1=//p" "$(env_file)" | tail -n 1 | sed -e 's/^"\(.*\)"$/\1/' -e "s/^'\(.*\)'$/\1/"
}

# set_env KEY VALUE: VALUE must not contain '|', newlines or backslashes;
# callers pass validated values only.
set_env() {
	f=$(env_file)
	if grep -q "^$1=" "$f"; then
		sed -i "s|^$1=.*|$1=$2|" "$f"
	else
		printf '%s=%s\n' "$1" "$2" >>"$f"
	fi
}

generate_password() {
	pw=""
	while [ "${#pw}" -lt 24 ]; do
		pw="$pw$(openssl rand -base64 48 | tr -dc 'A-Za-z0-9')"
	done
	printf '%s' "${pw:0:24}"
}

public_ipv4() {
	ip -4 route get 1.1.1.1 2>/dev/null | sed -n 's/.* src \([0-9.]*\).*/\1/p' | head -n 1
}

public_ipv6() {
	a=$(ip -6 route get 2606:4700:4700::1111 2>/dev/null | sed -n 's/.* src \([0-9a-fA-F:]*\).*/\1/p' | head -n 1)
	case "$a" in
	"" | fe80:* | fc* | fd* | ::1) ;;
	*) printf '%s' "$a" ;;
	esac
}

is_private_ipv4() {
	case "$1" in
	10.* | 127.* | 192.168.* | 169.254.* | 172.1[6-9].* | 172.2[0-9].* | 172.3[01].* | 100.6[4-9].* | 100.[7-9][0-9].* | 100.1[01][0-9].* | 100.12[0-7].*) return 0 ;;
	*) return 1 ;;
	esac
}

ADMIN_PASSWORD_NEW=0

write_env() {
	f=$(env_file)
	domain=${DROPSITE_DOMAIN:-}
	email=${DROPSITE_EMAIL:-}
	if [ ! -f "$f" ]; then
		[ -n "$domain" ] && [ -n "$email" ] || die "DROPSITE_DOMAIN and DROPSITE_EMAIL are required for a new installation"
		step "Writing $f"
		(umask 077 && cp "$APP_DIR/deploy/.env.example" "$f")
		set_env ADMIN_EMAIL "$email"
		set_env ADMIN_PASSWORD "$(generate_password)"
		# Release channel for prebuilt images, once .env.example knows it.
		if grep -q '^DROPSITE_VERSION=' "$f"; then
			set_env DROPSITE_VERSION latest
		fi
		ADMIN_PASSWORD_NEW=1
	else
		step "Keeping the existing $f"
	fi
	chmod 600 "$f"
	[ -z "$domain" ] || set_env BASE_DOMAIN "$domain"
	[ -z "$email" ] || set_env ACME_EMAIL "$email"
	[ -n "$(get_env ADMIN_BIND_IP)" ] || set_env ADMIN_BIND_IP 127.0.0.1
	port=${DROPSITE_LOCAL_PORT:-}
	if [ -z "$(get_env UI_URL)" ] && [ -n "$port" ]; then
		set_env UI_URL "http://localhost:$port"
	fi
	if [ -z "$(get_env SERVER_IPS)" ]; then
		ips=""
		v4=$(public_ipv4)
		if [ -n "$v4" ] && ! is_private_ipv4 "$v4"; then ips=$v4; fi
		v6=$(public_ipv6)
		[ -z "$v6" ] || ips="${ips:+$ips,}$v6"
		[ -z "$ips" ] || set_env SERVER_IPS "$ips"
	fi
	for k in BASE_DOMAIN ACME_EMAIL ADMIN_EMAIL ADMIN_PASSWORD; do
		[ -n "$(get_env "$k")" ] || die "$k is empty in $f"
	done
}

compose() { (cd "$APP_DIR/deploy" && docker compose "$@"); }

# prebuilt_images: true once docker-compose.yml names a published image for
# control (planned release pipeline). Today control is built from source.
prebuilt_images() {
	compose config 2>/dev/null | awk '
		/^  control:/ { c = 1; next }
		/^  [a-z]/ { c = 0 }
		c && /^    image: ghcr\.io\// { f = 1 }
		END { exit !f }'
}

# compose_quiet ARGS...: docker compose into the log.
compose_quiet() {
	printf '\n$ docker compose %s\n' "$*" >>"$LOG"
	compose "$@" >>"$LOG" 2>&1 </dev/null
}

# stack_up is the single "start the stack" step, shared with dropsite-update
# (server-bootstrap.sh --up). With published images it pulls the images of
# DROPSITE_VERSION and falls back to a source build when the pull fails or
# DROPSITE_BUILD=1; without them it builds from the checkout.
stack_up() {
	if [ "${DROPSITE_BUILD:-}" != 1 ] && prebuilt_images; then
		step "Downloading dropsite ${DROPSITE_VERSION:-$(get_env DROPSITE_VERSION)} images"
		if compose_quiet pull && compose_quiet up -d --no-build --wait --wait-timeout "$WAIT_TIMEOUT"; then
			return 0
		fi
		warn "could not start the published images; building from source instead"
	fi
	step "Building and starting dropsite from source (the first build takes several minutes)"
	compose_quiet up -d --build --wait --wait-timeout "$WAIT_TIMEOUT"
}

start_stack() {
	run compose config --quiet
	if ! stack_up; then
		tail -n 60 "$LOG" >&2
		compose ps >&2 || true
		die "dropsite did not start (full log: $LOG)"
	fi
	for _ in $(seq 1 60); do
		id=$(compose ps -q control)
		if [ -n "$id" ] && [ "$(docker inspect -f '{{.State.Health.Status}}' "$id")" = healthy ]; then
			return
		fi
		sleep 2
	done
	compose ps >&2 || true
	die "the control service is not healthy"
}

# check_stack verifies the stack through the server-local ports: the admin UI
# on 127.0.0.1:8082 and Caddy on port 80 for the API host.
check_stack() {
	step "Checking the services on this server"
	api_host=$(get_env API_HOST)
	[ -n "$api_host" ] || api_host="api.$(get_env BASE_DOMAIN)"
	for _ in $(seq 1 30); do
		ui=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 http://127.0.0.1:8082/login || true)
		web=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 -H "Host: $api_host" http://127.0.0.1/ || true)
		if [ "$ui" = 200 ] && [ "$web" = 308 ]; then
			note "admin interface: ok, web server: ok"
			return
		fi
		sleep 2
	done
	die "services do not answer (admin interface: HTTP $ui, web server: HTTP $web)"
}

# write_file PATH MODE: install stdin atomically, so a running copy of a
# helper (dropsite-update refreshing itself) keeps its old inode.
write_file() {
	tmp=$(mktemp "$1.XXXXXX")
	cat >"$tmp"
	chmod "$2" "$tmp"
	mv -f "$tmp" "$1"
}

install_helpers() {
	step "Installing the commands dropsite-update, dropsite-rollback, dropsite-backup, dropsite-admin-password, dropsite-info"
	header=$(printf '#!/usr/bin/env bash\n# Installed by dropsite server-bootstrap.sh; reinstalled by dropsite-update.\nset -euo pipefail\nAPP_DIR=%q\n' "$APP_DIR")

	{
		printf '%s\n' "$header"
		cat <<'EOF'
# Back up dropsite into /var/backups/dropsite, keeping the newest 7 archives.
# The first run also copies the form secrets key to /root/dropsite-secrets.key.
DIR=/var/backups/dropsite
KEEP=${DROPSITE_KEEP_BACKUPS:-7}
umask 077
mkdir -p "$DIR"
chmod 700 "$DIR"
cd "$APP_DIR/deploy"
out="$DIR/dropsite-$(date +%Y-%m-%d-%H%M%S).tar.gz"
trap 'rm -f "$out.part" /root/dropsite-secrets.key.part' EXIT
docker compose exec -T control /control backup - >"$out.part"
mv "$out.part" "$out"
ls -1t "$DIR"/dropsite-*.tar.gz | tail -n +$((KEEP + 1)) | xargs -r rm -f --
if [ ! -s /root/dropsite-secrets.key ]; then
	docker compose cp control:/state/secrets.key /root/dropsite-secrets.key.part >&2
	chmod 600 /root/dropsite-secrets.key.part
	mv /root/dropsite-secrets.key.part /root/dropsite-secrets.key
fi
echo "$out"
EOF
	} | write_file "$BIN_DIR/dropsite-backup" 0755

	{
		printf '%s\n' "$header"
		cat <<'EOF'
# Update dropsite: back up, remember what runs now (for dropsite-rollback),
# download the new version, start it and restart Caddy if its config changed.
STATE_DIR=/var/lib/dropsite
cd "$APP_DIR"
echo "==> Backing up"
backup=$(dropsite-backup)
echo "    $backup"
control=$(cd deploy && docker compose ps -q control)
image=""
[ -z "$control" ] || image=$(docker inspect -f '{{.Image}}' "$control")
version=$(sed -n 's/^DROPSITE_VERSION=//p' deploy/.env | tail -n 1)
mkdir -p "$STATE_DIR"
{
	printf 'PREV_COMMIT=%q\n' "$(git rev-parse HEAD)"
	printf 'PREV_IMAGE=%q\n' "$image"
	printf 'PREV_VERSION=%q\n' "$version"
	printf 'PREV_BACKUP=%q\n' "$backup"
	printf 'PREV_DATE=%q\n' "$(date -u +%FT%TZ)"
} >"$STATE_DIR/previous-release.tmp"
mv -f "$STATE_DIR/previous-release.tmp" "$STATE_DIR/previous-release"
before=$(sha256sum deploy/Caddyfile | cut -d' ' -f1)
echo "==> Downloading the new version"
if ! git symbolic-ref -q HEAD >/dev/null; then
	# After dropsite-rollback the checkout is detached: back to the branch.
	git checkout -q "$(cat "$STATE_DIR/branch" 2>/dev/null || echo main)"
fi
git pull --ff-only
echo "==> Starting the new version (a few minutes)"
bash "$APP_DIR/scripts/server-bootstrap.sh" --up
cd deploy
if [ "$(sha256sum Caddyfile | cut -d' ' -f1)" != "$before" ]; then
	echo "==> The Caddyfile changed: restarting the web server"
	docker compose restart caddy
fi
bash "$APP_DIR/scripts/server-bootstrap.sh" --install-helpers
docker compose ps
echo "==> dropsite is up to date. To go back: dropsite-rollback"
EOF
	} | write_file "$BIN_DIR/dropsite-update" 0755

	{
		printf '%s\n' "$header"
		cat <<'EOF'
# Go back to the version that ran before the last dropsite-update: check out
# that commit, rebuild it, restore the backup taken right before the update
# (the database of a newer version cannot be used by an older one) and start.
# Data written since that update is lost.
STATE=/var/lib/dropsite/previous-release
[ -f "$STATE" ] || { echo "nothing to roll back: run dropsite-update first" >&2; exit 1; }
# shellcheck disable=SC1090
. "$STATE"
[ -s "$PREV_BACKUP" ] || { echo "the backup $PREV_BACKUP is missing" >&2; exit 1; }
if [ "${1:-}" != --yes ]; then
	echo "This returns dropsite to commit $PREV_COMMIT (version ${PREV_VERSION:-dev}, as of $PREV_DATE)"
	echo "and restores the backup $PREV_BACKUP: changes made since then are lost."
	read -r -p "Type yes to continue: " answer
	[ "$answer" = yes ] || exit 1
fi
cd "$APP_DIR"
before=$(sha256sum deploy/Caddyfile | cut -d' ' -f1)
git checkout -q --detach "$PREV_COMMIT"
cd deploy
echo "==> Building the previous version"
docker compose build control
echo "==> Restoring the backup"
docker compose stop control
docker compose run --rm -T control restore - --force <"$PREV_BACKUP"
echo "==> Starting"
docker compose up -d --wait --wait-timeout 900
if [ "$(sha256sum Caddyfile | cut -d' ' -f1)" != "$before" ]; then
	docker compose restart caddy
fi
mv -f "$STATE" "$STATE.done"
docker compose ps
echo "==> Rolled back. dropsite-update brings the new version again."
EOF
	} | write_file "$BIN_DIR/dropsite-rollback" 0755

	{
		printf '%s\n' "$header"
		cat <<'EOF'
# Set a new random password for the administrator (or for the e-mail given)
# and print it. Every session of that user ends.
cd "$APP_DIR/deploy"
exec docker compose exec -T control /control reset-password "$@"
EOF
	} | write_file "$BIN_DIR/dropsite-admin-password" 0755

	{
		printf '%s\n' "$header"
		cat <<'EOF'
# Print the installation facts as DROPSITE_RESULT lines (no secrets).
exec bash "$APP_DIR/scripts/server-bootstrap.sh" --info
EOF
	} | write_file "$BIN_DIR/dropsite-info" 0755

	printf '%s\n' \
		'# dropsite: daily backup at 03:15 server time (see dropsite-backup).' \
		'SHELL=/bin/bash' \
		'PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin' \
		'15 3 * * * root /usr/local/bin/dropsite-backup >>/var/log/dropsite-backup.log 2>&1' |
		write_file "$CRON_FILE" 0644
}

first_backup() {
	step "Making the first backup"
	if ! "$BIN_DIR/dropsite-backup" >>"$LOG" 2>&1 </dev/null; then
		warn "the first backup failed (see $LOG); the daily backup will retry"
	fi
}

print_info() {
	f=$(env_file)
	[ -f "$f" ] || die "dropsite is not installed in $APP_DIR"
	domain=$(get_env BASE_DOMAIN)
	api_host=$(get_env API_HOST)
	printf 'DROPSITE_RESULT domain=%s\n' "$domain"
	printf 'DROPSITE_RESULT api_host=%s\n' "${api_host:-api.$domain}"
	printf 'DROPSITE_RESULT admin_email=%s\n' "$(get_env ADMIN_EMAIL)"
	printf 'DROPSITE_RESULT ipv4=%s\n' "$(public_ipv4)"
	printf 'DROPSITE_RESULT ipv6=%s\n' "$(public_ipv6)"
}

validate_inputs() {
	if [ -n "${DROPSITE_DOMAIN:-}" ] && ! printf '%s' "$DROPSITE_DOMAIN" | grep -Eq "$DOMAIN_RE"; then
		die "invalid domain: $DROPSITE_DOMAIN"
	fi
	if [ -n "${DROPSITE_EMAIL:-}" ] && ! printf '%s' "$DROPSITE_EMAIL" | grep -Eq "$EMAIL_RE"; then
		die "invalid e-mail: $DROPSITE_EMAIL"
	fi
	case "${DROPSITE_LOCAL_PORT:-}" in
	"" | [1-9] | [1-9][0-9] | [1-9][0-9][0-9] | [1-9][0-9][0-9][0-9] | [1-9][0-9][0-9][0-9][0-9]) ;;
	*) die "invalid DROPSITE_LOCAL_PORT: $DROPSITE_LOCAL_PORT" ;;
	esac
}

main() {
	case "${1:-}" in
	--info)
		print_info
		return
		;;
	--up)
		check_root
		touch "$LOG"
		chmod 600 "$LOG"
		start_stack
		check_stack
		return
		;;
	--install-helpers)
		check_root
		install_helpers
		return
		;;
	"") ;;
	*) die "unknown argument: $1" ;;
	esac
	check_root
	check_os
	validate_inputs
	touch "$LOG"
	chmod 600 "$LOG"
	printf '\n===== %s dropsite bootstrap =====\n' "$(date -u +%FT%TZ)" >>"$LOG"
	install_base_packages
	ensure_swap
	configure_firewall
	install_docker
	checkout
	write_env
	start_stack
	check_stack
	install_helpers
	first_backup
	step "dropsite is running"
	print_info
	printf 'DROPSITE_RESULT admin_password=%s\n' "$(get_env ADMIN_PASSWORD)"
	printf 'DROPSITE_RESULT admin_password_new=%s\n' "$ADMIN_PASSWORD_NEW"
}

main "$@"
