#!/usr/bin/env bash
# Prepare a fresh Ubuntu (22.04 or newer) or Debian (12 or newer) server for Dropsite and
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
# starts the stack, removes that password from .env once the administrator
# exists (it is shown once; dropsite-admin-password resets it), installs a daily backup and the helper commands
# dropsite-update, dropsite-rollback, dropsite-backup, dropsite-admin-password
# and dropsite-info, and dropsite-update-watch: the root job that runs an
# update an administrator requested with the button in the web interface.
#
# Releases: DROPSITE_VERSION in deploy/.env selects a published release
# ("latest", the default, or an exact version such as 1.4.2). The checkout is
# switched to its tag vX.Y.Z, so the compose file and the Caddyfile match the
# prebuilt images, which are then downloaded from ghcr.io instead of being
# compiled on the server. Before the first release, or with DROPSITE_BUILD=1,
# control is built from the checked-out branch.
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
#   DROPSITE_PUBLIC_ADMIN 1: also open the admin interface from the internet at
#                         https://admin.<domain>; 0: SSH tunnel only. Applied
#                         once with "control public-admin on|off" (the choice
#                         lives in the database, not in .env); unset keeps the
#                         current choice (off on a new server)
#   DROPSITE_REPO_URL     repository to clone (default: the public GitHub repository)
#   DROPSITE_REF          branch to clone (default: main)
#   DROPSITE_DIR          checkout directory (default: /opt/dropsite)
#   DROPSITE_VERSION      release to run (default: DROPSITE_VERSION of .env,
#                         else latest)
#   DROPSITE_BUILD=1      build the source of the branch instead of
#                         downloading the images of a release
#   DROPSITE_SKIP_SWAP=1      never create swap (test containers)
#   DROPSITE_SKIP_FIREWALL=1  never touch the firewall (test containers)
#
# Output: progress on stderr; on success the last lines on stdout are
# "DROPSITE_RESULT key=value" (domain, api_host, admin_email, admin_password,
# admin_password_new, ipv4, ipv6, public_admin_url) for the local installer. admin_password is
# printed only by the run that generated it; the password line goes to stdout
# only and never into the log file.
set -euo pipefail
umask 022

REPO_URL=${DROPSITE_REPO_URL:-https://github.com/b0balYson/dropsite.git}
REF=${DROPSITE_REF:-main}
APP_DIR=${DROPSITE_DIR:-/opt/dropsite}
LOG=/var/log/dropsite-install.log
BIN_DIR=/usr/local/bin
CRON_FILE=/etc/cron.d/dropsite-backup
UPDATE_CRON_FILE=/etc/cron.d/dropsite-update
SYSTEMD_DIR=/etc/systemd/system
COMPOSE_MIN=2.24.4
SWAP_TARGET_MB=3072 # RAM + swap of at least 3 GB: enough to compile control
STATE_DIR=/var/lib/dropsite
# A release version: X.Y.Z with an optional pre-release suffix.
RELEASE_RE='^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'
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

# os_supported ID VERSION_ID: Ubuntu 22.04 or newer, Debian 12 or newer.
# Newer releases are accepted as long as Docker publishes packages for them
# (checked on the server before Docker is installed).
os_supported() {
	v=${2:-}
	case "${1:-}" in
	ubuntu)
		major=${v%%.*}
		minor=${v#*.}
		[ "$minor" != "$v" ] || minor=0
		case "$major" in '' | *[!0-9]*) return 1 ;; esac
		case "$minor" in '' | *[!0-9]*) return 1 ;; esac
		[ $((major * 100 + ${minor#0})) -ge 2204 ]
		;;
	debian)
		case "$v" in '' | *[!0-9]*) return 1 ;; esac
		[ "$v" -ge 12 ]
		;;
	*) return 1 ;;
	esac
}

# check_os sets OS_ID and OS_CODENAME, refusing unsupported systems.
check_os() {
	[ -r /etc/os-release ] || die "unknown operating system: /etc/os-release is missing"
	# shellcheck disable=SC1091
	read -r OS_ID OS_VERSION OS_CODENAME < <(. /etc/os-release && printf '%s %s %s\n' "$ID" "${VERSION_ID:-}" "${VERSION_CODENAME:-}")
	os_supported "$OS_ID" "$OS_VERSION" ||
		die "unsupported system: $OS_ID $OS_VERSION (supported: Ubuntu 22.04 or newer, Debian 12 or newer)"
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
	# A release newer than the ones tested is fine once Docker ships packages for it.
	curl -fsSIL --max-time 20 -o /dev/null "https://download.docker.com/linux/$OS_ID/dists/$OS_CODENAME/Release" ||
		die "Docker does not publish packages for $OS_ID $OS_VERSION ($OS_CODENAME) yet: reinstall the server with an earlier release (Ubuntu 24.04 or Debian 12) and run the installer again"
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
		step "Updating the Dropsite source in $APP_DIR"
		# A release checkout is detached at its tag: back to the branch first.
		if ! git -C "$APP_DIR" symbolic-ref -q HEAD >/dev/null; then
			git -C "$APP_DIR" checkout -q "$(cat "$STATE_DIR/branch" 2>/dev/null || echo "$REF")" >>"$LOG" 2>&1 </dev/null || true
		fi
		if ! git -C "$APP_DIR" pull --ff-only >>"$LOG" 2>&1 </dev/null; then
			warn "could not update $APP_DIR with 'git pull --ff-only'; continuing with the current version"
		fi
	else
		step "Downloading Dropsite to $APP_DIR"
		[ ! -e "$APP_DIR" ] || [ -z "$(ls -A "$APP_DIR")" ] ||
			die "$APP_DIR exists and is not a git checkout; move it away and run again"
		run git clone --branch "$REF" "$REPO_URL" "$APP_DIR"
	fi
	# dropsite-update returns to this branch before selecting a release.
	mkdir -p "$STATE_DIR"
	git -C "$APP_DIR" symbolic-ref -q --short HEAD >"$STATE_DIR/branch" 2>/dev/null ||
		printf '%s\n' "$REF" >"$STATE_DIR/branch"
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

# unset_env KEY: remove every KEY= line from .env, keeping its mode 600.
unset_env() {
	f=$(env_file)
	[ -f "$f" ] || return 0
	grep -q "^$1=" "$f" || return 0
	tmp=$(mktemp "$f.XXXXXX")
	grep -v "^$1=" "$f" >"$tmp" || true
	chmod 600 "$tmp"
	mv -f "$tmp" "$f"
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
		if [ -z "$domain" ] || [ -z "$email" ]; then
			die "DROPSITE_DOMAIN and DROPSITE_EMAIL are required for a new installation"
		fi
		step "Writing $f"
		(umask 077 && cp "$APP_DIR/deploy/.env.example" "$f")
		set_env ADMIN_EMAIL "$email"
		set_env ADMIN_PASSWORD "$(generate_password)"
		# The newest release unless the caller pins one.
		v=${DROPSITE_VERSION:-latest}
		set_env DROPSITE_VERSION "${v#v}"
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
	# ADMIN_PASSWORD is only needed until the first administrator exists;
	# forget_admin_password removes it after the first healthy start.
	for k in BASE_DOMAIN ACME_EMAIL ADMIN_EMAIL; do
		[ -n "$(get_env "$k")" ] || die "$k is empty in $f"
	done
}

# forget_admin_password removes ADMIN_PASSWORD from .env once control runs
# healthy, which means the first administrator exists, and recreates control
# without it: the value would otherwise stay visible through docker inspect
# and become valid again if the database were ever recreated empty. The
# password is reset with dropsite-admin-password.
forget_admin_password() {
	grep -q '^ADMIN_PASSWORD=' "$(env_file)" || return 0
	step "Removing the first administrator password from $(env_file) (reset it with dropsite-admin-password)"
	unset_env ADMIN_PASSWORD
	unset ADMIN_PASSWORD
	if ! compose_quiet up -d --no-build --wait --wait-timeout "$WAIT_TIMEOUT" control; then
		tail -n 60 "$LOG" >&2
		die "control did not restart without ADMIN_PASSWORD (full log: $LOG)"
	fi
}

compose() { (cd "$APP_DIR/deploy" && docker compose "$@"); }

# published_images: the ghcr.io images the resolved compose file names.
published_images() {
	compose config --images 2>/dev/null | grep '^ghcr\.io/' || true
}

# images_present: every published image is in the local image store. It is
# checked separately because "docker compose pull" exits 0 even when the
# image of a service that has a build section cannot be downloaded.
images_present() {
	imgs=$(published_images)
	[ -n "$imgs" ] || return 1
	for i in $imgs; do
		docker image inspect "$i" >/dev/null 2>&1 || return 1
	done
}

# release_tag WANT: the Git tag of release WANT ("latest": the newest tag
# vX.Y.Z without a pre-release suffix); empty when there is none.
release_tag() {
	if [ "$1" = latest ]; then
		git -C "$APP_DIR" tag -l 'v*' --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -n 1 || true
	elif git -C "$APP_DIR" rev-parse -q --verify "refs/tags/v${1#v}^{commit}" >/dev/null; then
		printf 'v%s\n' "${1#v}"
	fi
}

# select_release checks out the tag of the release DROPSITE_VERSION names
# and exports its exact version for compose, so the compose file, the
# Caddyfile and the images all belong to one release. It fails, meaning
# "build the current checkout", with DROPSITE_BUILD=1, before the first
# release exists, or when that release's compose file names no published image.
select_release() {
	[ "${DROPSITE_BUILD:-}" != 1 ] || return 1
	want=${DROPSITE_VERSION:-$(get_env DROPSITE_VERSION)}
	want=${want:-latest}
	if [ "$want" != latest ] && ! printf '%s\n' "${want#v}" | grep -Eq "$RELEASE_RE"; then
		die "DROPSITE_VERSION must be latest or a release such as 1.4.2, got: $want"
	fi
	if ! git -C "$APP_DIR" fetch -q --tags --force origin >>"$LOG" 2>&1 </dev/null; then
		warn "could not fetch release tags; using the tags already known"
	fi
	tag=$(release_tag "$want")
	if [ -z "$tag" ]; then
		[ "$want" = latest ] || die "release $want does not exist (no tag v${want#v} in $REPO_URL)"
		note "no published release yet: building from source"
		return 1
	fi
	run git -C "$APP_DIR" -c advice.detachedHead=false checkout -q --detach "$tag"
	export DROPSITE_VERSION="${tag#v}"
	[ -n "$(published_images)" ]
}

# tag_latest: on the default channel, also tag the downloaded images :latest,
# so a manual "docker compose up" with .env's latest starts the same images.
tag_latest() {
	case "$(get_env DROPSITE_VERSION)" in
	"" | latest) ;;
	*) return 0 ;;
	esac
	for i in $(published_images); do
		docker tag "$i" "${i%:*}:latest" >>"$LOG" 2>&1 || true
	done
}

# compose_quiet ARGS...: docker compose into the log.
compose_quiet() {
	printf '\n$ docker compose %s\n' "$*" >>"$LOG"
	compose "$@" >>"$LOG" 2>&1 </dev/null
}

# stack_up is the single "start the stack" step, shared with dropsite-update
# (server-bootstrap.sh --up). It runs the prebuilt images of the selected
# release; when they cannot be downloaded it builds that same release from
# source. Without a release (DROPSITE_BUILD=1 or no tag yet) it builds the
# checked-out branch, with images tagged "dev".
stack_up() {
	if select_release; then
		step "Downloading the Dropsite $DROPSITE_VERSION images"
		if compose_quiet pull && images_present &&
			compose_quiet up -d --no-build --wait --wait-timeout "$WAIT_TIMEOUT"; then
			tag_latest
			return 0
		fi
		warn "could not start the published images of $DROPSITE_VERSION; building that release from source instead"
	else
		export DROPSITE_VERSION=dev
	fi
	DROPSITE_BUILD_VERSION=$(sh "$APP_DIR/scripts/version.sh" 2>/dev/null || echo dev)
	export DROPSITE_BUILD_VERSION
	step "Building and starting Dropsite $DROPSITE_BUILD_VERSION from source (the first build takes several minutes)"
	compose_quiet up -d --build --wait --wait-timeout "$WAIT_TIMEOUT"
}

start_stack() {
	run compose config --quiet
	if ! stack_up; then
		tail -n 60 "$LOG" >&2
		compose ps >&2 || true
		die "Dropsite did not start (full log: $LOG)"
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
	step "Installing the commands dropsite-update, dropsite-rollback, dropsite-backup, dropsite-admin-password, dropsite-info, dropsite-update-watch"
	header=$(printf '#!/usr/bin/env bash\n# Installed by Dropsite server-bootstrap.sh; reinstalled by dropsite-update.\nset -euo pipefail\nAPP_DIR=%q\n' "$APP_DIR")

	{
		printf '%s\n' "$header"
		cat <<'EOF'
# Back up Dropsite into /var/backups/dropsite, keeping the newest 7 archives.
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
# Update Dropsite: back up, remember what runs now (for dropsite-rollback),
# download the new version, start it and restart Caddy if its config changed.
#   dropsite-update            the release DROPSITE_VERSION in deploy/.env names
#                              (latest by default)
#   dropsite-update --to X.Y.Z exactly that release (the update button in the
#                              web interface passes the version it showed)
STATE_DIR=/var/lib/dropsite
TO=""
usage() { echo "usage: dropsite-update [--to X.Y.Z]" >&2; exit 2; }
while [ $# -gt 0 ]; do
	case $1 in
	--to)
		[ $# -ge 2 ] || usage
		TO=$2
		shift 2
		;;
	--to=*)
		TO=${1#--to=}
		shift
		;;
	*) usage ;;
	esac
done
if [ -n "$TO" ] && ! [[ $TO =~ ^[0-9]{1,6}\.[0-9]{1,6}\.[0-9]{1,6}$ ]]; then
	echo "dropsite-update: --to needs a release such as 1.4.2" >&2
	exit 2
fi
# One update at a time, whoever starts it.
exec 8>>/run/dropsite-update.lock
flock -n 8 || { echo "dropsite-update: another update is running" >&2; exit 1; }
cd "$APP_DIR"
echo "==> Backing up"
backup=$(dropsite-backup)
echo "    $backup"
control=$(cd deploy && docker compose ps -q control)
image=""
version=""
if [ -n "$control" ]; then
	image=$(docker inspect -f '{{.Image}}' "$control")
	# The version the running binary reports: X.Y.Z or dev+<commit>.
	version=$(docker exec "$control" /control version 2>/dev/null | awk '{print $NF}') || version=""
fi
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
git pull -q --ff-only
if [ -n "$TO" ]; then
	echo "==> Selecting release $TO"
	export DROPSITE_VERSION="$TO"
	# A pinned version follows the explicit choice; "latest" stays latest.
	case "$(sed -n 's/^DROPSITE_VERSION=//p' deploy/.env | tail -n 1)" in
	"" | latest) ;;
	*) sed -i "s/^DROPSITE_VERSION=.*/DROPSITE_VERSION=$TO/" deploy/.env ;;
	esac
fi
echo "==> Starting the new version (a few minutes)"
bash "$APP_DIR/scripts/server-bootstrap.sh" --up
cd deploy
if [ "$(sha256sum Caddyfile | cut -d' ' -f1)" != "$before" ]; then
	echo "==> The Caddyfile changed: restarting the web server"
	docker compose restart caddy
fi
bash "$APP_DIR/scripts/server-bootstrap.sh" --install-helpers
docker compose ps --format "    {{.Service}}: {{.Status}}"
echo "==> Dropsite is up to date. To go back: dropsite-rollback"
EOF
	} | write_file "$BIN_DIR/dropsite-update" 0755

	{
		printf '%s\n' "$header"
		cat <<'EOF'
# Go back to the version that ran before the last dropsite-update: check out
# that commit, download that release's images (or rebuild it when it was a
# source build or the images are gone), restore the backup taken right before
# the update (the database of a newer version cannot be used by an older one)
# and start. Data written since that update is lost.
STATE=/var/lib/dropsite/previous-release
[ -f "$STATE" ] || { echo "nothing to roll back: run dropsite-update first" >&2; exit 1; }
# shellcheck disable=SC1090
. "$STATE"
[ -s "$PREV_BACKUP" ] || { echo "the backup $PREV_BACKUP is missing" >&2; exit 1; }
if [ "${1:-}" != --yes ]; then
	echo "This returns Dropsite to commit $PREV_COMMIT (version ${PREV_VERSION:-dev}, as of $PREV_DATE)"
	echo "and restores the backup $PREV_BACKUP: changes made since then are lost."
	read -r -p "Type yes to continue: " answer
	[ "$answer" = yes ] || exit 1
fi
cd "$APP_DIR"
before=$(sha256sum deploy/Caddyfile | cut -d' ' -f1)
git checkout -q --detach "$PREV_COMMIT"
cd deploy
published() { docker compose config --images | grep '^ghcr\.io/' || true; }
need_build=1
if [ "${DROPSITE_BUILD:-}" != 1 ] && [ -n "$(published)" ] &&
	printf '%s\n' "${PREV_VERSION:-}" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'; then
	export DROPSITE_VERSION="$PREV_VERSION"
	echo "==> Downloading the images of $PREV_VERSION"
	docker compose pull -q || true
	need_build=0
	for i in $(published); do
		docker image inspect "$i" >/dev/null 2>&1 || need_build=1
	done
else
	export DROPSITE_VERSION=dev
fi
if [ "$need_build" = 1 ]; then
	echo "==> Building the previous version"
	DROPSITE_BUILD_VERSION=$(sh ../scripts/version.sh 2>/dev/null || echo dev)
	export DROPSITE_BUILD_VERSION
	docker compose build
fi
echo "==> Restoring the backup"
docker compose stop control
docker compose run --rm -T control restore - --force <"$PREV_BACKUP"
echo "==> Starting"
docker compose up -d --no-build --wait --wait-timeout 900
if [ "$(sha256sum Caddyfile | cut -d' ' -f1)" != "$before" ]; then
	docker compose restart caddy
fi
mv -f "$STATE" "$STATE.done"
docker compose ps --format "    {{.Service}}: {{.Status}}"
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

	{
		printf '%s\n' "$header"
		cat <<'EOF'
# Run an update an administrator requested with the button in the web
# interface. control cannot update itself (no Docker access): it writes
# update/request into its state volume, and this root job, started every
# minute by cron (and at once by a systemd path unit where systemd runs):
#   - writes update/heartbeat, so the web interface knows it is installed;
#   - reads the request and removes it; the request is two lines,
#     "version=X.Y.Z" and "admin=<user id>", and anything else is refused;
#   - accepts only a release tag vX.Y.Z that exists in the repository and is
#     newer than the running version; nothing from the file is executed;
#   - runs dropsite-update --to X.Y.Z and writes update/status (JSON: state
#     running, done or failed, versions, times, the last log lines).
# Every file operation inside the volume runs as control's own user
# (setpriv), so a link a compromised control planted there cannot make root
# read or overwrite anything. Log: /var/log/dropsite-update.log.
VOLUME=${DROPSITE_STATE_VOLUME:-dropsite_state}
UPDATE_BIN=${DROPSITE_UPDATE_BIN:-/usr/local/bin/dropsite-update}
LOG=${DROPSITE_UPDATE_LOG:-/var/log/dropsite-update.log}
LOCK=${DROPSITE_UPDATE_LOCK:-/run/dropsite-update-watch.lock}
CONTROL_UID=65532
VERSION_RE='^[0-9]{1,6}\.[0-9]{1,6}\.[0-9]{1,6}$'
ADMIN_RE='^[1-9][0-9]{0,17}$'
MAX_REQUEST=256
LOG_LINES=20

as_control() {
	setpriv --reuid="$CONTROL_UID" --regid="$CONTROL_UID" --clear-groups --no-new-privs -- "$@"
}

data=$(docker volume inspect -f '{{.Mountpoint}}' "$VOLUME" 2>/dev/null) || exit 0
[ -n "$data" ] && [ -d "$data" ] || exit 0
# Root enters the volume (control cannot replace that directory: its parent
# belongs to Docker), and control's user then works with relative paths: it
# may not traverse /var/lib/docker itself.
cd "$data" || exit 0
dir=update
as_control mkdir -p -m 700 -- "$dir" 2>/dev/null || exit 0

# put NAME: stdin becomes $dir/NAME, replaced atomically, as control.
put() {
	tmp="$dir/.$1.$$"
	as_control tee -- "$tmp" >/dev/null && as_control mv -f -- "$tmp" "$dir/$1"
}

note() { printf '%s %s\n' "$(date -u +%FT%TZ)" "$*" >>"$LOG"; }

# json STRING: a JSON string without control characters, at most 300 bytes.
json() {
	s=$(printf '%s' "$1" | LC_ALL=C tr -d '\000-\037\177' | head -c 300)
	s=${s//\\/\\\\}
	s=${s//\"/\\\"}
	printf '"%s"' "$s"
}

# status STATE MESSAGE [LOGFILE]
status() {
	{
		printf '{"state":%s,"from":%s,"to":%s,"admin":%s,"started_at":%s,"finished_at":%s,"message":%s,"log":[' \
			"$(json "$1")" "$(json "$current")" "$(json "$version")" "$(json "$admin")" \
			"$(json "$started")" "$(json "${finished:-}")" "$(json "$2")"
		if [ -n "${3:-}" ]; then
			sep=""
			while IFS= read -r line; do
				printf '%s%s' "$sep" "$(json "$line")"
				sep=,
			done < <(tail -n "$LOG_LINES" -- "$3")
		fi
		printf ']}\n'
	} | put status
}

# newer A B: release A is newer than release B (both X.Y.Z, validated).
newer() {
	IFS=. read -r a1 a2 a3 <<<"$1"
	IFS=. read -r b1 b2 b3 <<<"$2"
	for p in "$((10#$a1 - 10#$b1))" "$((10#$a2 - 10#$b2))" "$((10#$a3 - 10#$b3))"; do
		[ "$p" -gt 0 ] && return 0
		[ "$p" -lt 0 ] && return 1
	done
	return 1
}

reject() {
	finished=$(date -u +%FT%TZ)
	note "update request refused: $1"
	status failed "request refused: $1"
	exit 0
}

date +%s | put heartbeat || exit 0

exec 9>>"$LOCK"
flock -n 9 || exit 0

req=$(as_control timeout 5 head -c "$MAX_REQUEST" -- "$dir/request" 2>/dev/null) || exit 0
as_control rm -f -- "$dir/request"
started=$(date -u +%FT%TZ)
version=""
admin=""
current=""
ctr=$(cd "$APP_DIR/deploy" && docker compose ps -q control 2>/dev/null) || ctr=""
if [ -n "$ctr" ]; then
	current=$(docker exec "$ctr" /control version 2>/dev/null | awk '{print $NF}') || current=""
fi
[[ $current =~ ^[0-9A-Za-z.+-]{1,64}$ ]] || current=""

v=""
a=""
n=0
while IFS= read -r line || [ -n "$line" ]; do
	n=$((n + 1))
	[ "$n" -le 2 ] || reject "unexpected content"
	case $line in
	version=*)
		[ -z "$v" ] || reject "version given twice"
		v=${line#version=}
		;;
	admin=*)
		[ -z "$a" ] || reject "administrator given twice"
		a=${line#admin=}
		;;
	*) reject "unexpected content" ;;
	esac
done <<<"$req"
[[ $v =~ $VERSION_RE ]] || reject "the version is not X.Y.Z"
[[ $a =~ $ADMIN_RE ]] || reject "no administrator"
version=$v
admin=$a
git -C "$APP_DIR" fetch -q --tags --force origin >/dev/null 2>&1 </dev/null || true
git -C "$APP_DIR" rev-parse -q --verify "refs/tags/v$version^{commit}" >/dev/null ||
	reject "release $version does not exist"
running=${current%%[-+]*}
if [[ $running =~ $VERSION_RE ]] && ! newer "$version" "$running"; then
	reject "release $version is not newer than $current"
fi

note "update to $version requested by administrator $admin (running ${current:-unknown})"
status running ""
out=$(mktemp)
trap 'rm -f "$out"' EXIT
rc=0
# The lock stays with this job only, never with what the update starts.
"$UPDATE_BIN" --to "$version" >"$out" 2>&1 </dev/null 9>&- || rc=$?
cat "$out" >>"$LOG"
finished=$(date -u +%FT%TZ)
if [ "$rc" = 0 ]; then
	note "update to $version done"
	status done "" "$out"
else
	note "update to $version failed (exit $rc)"
	status failed "dropsite-update exited with $rc" "$out"
fi
EOF
	} | write_file "$BIN_DIR/dropsite-update-watch" 0755

	printf '%s\n' \
		'# Dropsite: run an update requested in the web interface (see dropsite-update-watch).' \
		'SHELL=/bin/bash' \
		'PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin' \
		'* * * * * root /usr/local/bin/dropsite-update-watch >/dev/null 2>&1' |
		write_file "$UPDATE_CRON_FILE" 0644
	install_update_unit

	printf '%s\n' \
		'# Dropsite: daily backup at 03:15 server time (see dropsite-backup).' \
		'SHELL=/bin/bash' \
		'PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin' \
		'15 3 * * * root /usr/local/bin/dropsite-backup >>/var/log/dropsite-backup.log 2>&1' |
		write_file "$CRON_FILE" 0644
}

# install_update_unit starts dropsite-update-watch as soon as a request
# appears, where systemd runs; cron picks it up within a minute anyway.
install_update_unit() {
	[ -d /run/systemd/system ] || return 0
	data=$(docker volume inspect -f '{{.Mountpoint}}' dropsite_state 2>/dev/null) || return 0
	[ -n "$data" ] || return 0
	printf '%s\n' '[Unit]' 'Description=Run a Dropsite update requested in the web interface' '' \
		'[Service]' 'Type=oneshot' 'ExecStart=/usr/local/bin/dropsite-update-watch' |
		write_file "$SYSTEMD_DIR/dropsite-update-watch.service" 0644
	printf '%s\n' '[Unit]' 'Description=Watch for Dropsite update requests' '' \
		'[Path]' "PathChanged=$data/update/request" '' '[Install]' 'WantedBy=multi-user.target' |
		write_file "$SYSTEMD_DIR/dropsite-update-watch.path" 0644
	if ! { systemctl daemon-reload && systemctl enable --now dropsite-update-watch.path; } >>"$LOG" 2>&1 </dev/null; then
		warn "could not start dropsite-update-watch.path; cron runs requested updates within a minute"
	fi
}

# apply_public_admin passes the owner's answer to control once. The setting
# lives in the database (Settings in the web interface can change it later);
# nothing is written to .env, so it never becomes a permanent override.
apply_public_admin() {
	case "${DROPSITE_PUBLIC_ADMIN:-}" in
	1) state=on ;;
	0) state=off ;;
	*) return 0 ;;
	esac
	step "Admin interface from the internet: $state"
	if ! compose exec -T control /control public-admin "$state" >>"$LOG" 2>&1 </dev/null; then
		warn "could not turn $state the admin interface from the internet (see $LOG); change it in Settings"
	fi
}

# public_admin_url prints https://admin.<domain> while public access is on.
public_admin_url() {
	state=$(compose exec -T control /control public-admin status 2>/dev/null </dev/null) || return 0
	[ "$state" = on ] || return 0
	printf 'https://admin.%s' "$(get_env BASE_DOMAIN)"
}

first_backup() {
	step "Making the first backup"
	if ! "$BIN_DIR/dropsite-backup" >>"$LOG" 2>&1 </dev/null; then
		warn "the first backup failed (see $LOG); the daily backup will retry"
	fi
}

print_info() {
	f=$(env_file)
	[ -f "$f" ] || die "Dropsite is not installed in $APP_DIR"
	domain=$(get_env BASE_DOMAIN)
	api_host=$(get_env API_HOST)
	printf 'DROPSITE_RESULT domain=%s\n' "$domain"
	printf 'DROPSITE_RESULT api_host=%s\n' "${api_host:-api.$domain}"
	printf 'DROPSITE_RESULT admin_email=%s\n' "$(get_env ADMIN_EMAIL)"
	printf 'DROPSITE_RESULT ipv4=%s\n' "$(public_ipv4)"
	printf 'DROPSITE_RESULT ipv6=%s\n' "$(public_ipv6)"
	printf 'DROPSITE_RESULT public_admin_url=%s\n' "$(public_admin_url)"
}

validate_inputs() {
	if [ -n "${DROPSITE_VERSION:-}" ] && [ "$DROPSITE_VERSION" != latest ] &&
		! printf '%s\n' "${DROPSITE_VERSION#v}" | grep -Eq "$RELEASE_RE"; then
		die "invalid DROPSITE_VERSION: $DROPSITE_VERSION (latest or a release such as 1.4.2)"
	fi
	if [ -n "${DROPSITE_DOMAIN:-}" ] && ! printf '%s' "$DROPSITE_DOMAIN" | grep -Eq "$DOMAIN_RE"; then
		die "invalid domain: $DROPSITE_DOMAIN"
	fi
	if [ -n "${DROPSITE_EMAIL:-}" ] && ! printf '%s' "$DROPSITE_EMAIL" | grep -Eq "$EMAIL_RE"; then
		die "invalid e-mail: $DROPSITE_EMAIL"
	fi
	case "${DROPSITE_PUBLIC_ADMIN:-}" in
	"" | 0 | 1) ;;
	*) die "invalid DROPSITE_PUBLIC_ADMIN: $DROPSITE_PUBLIC_ADMIN (1 or 0)" ;;
	esac
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
		forget_admin_password
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
	printf '\n===== %s Dropsite bootstrap =====\n' "$(date -u +%FT%TZ)" >>"$LOG"
	install_base_packages
	ensure_swap
	configure_firewall
	install_docker
	checkout
	write_env
	# Read before it leaves .env: shown once at the end, never logged.
	admin_password=$(get_env ADMIN_PASSWORD)
	start_stack
	forget_admin_password
	check_stack
	apply_public_admin
	install_helpers
	first_backup
	step "Dropsite is running"
	print_info
	if [ "$ADMIN_PASSWORD_NEW" = 1 ]; then
		printf 'DROPSITE_RESULT admin_password=%s\n' "$admin_password"
	else
		note "the administrator password is not stored on the server; reset it with dropsite-admin-password"
	fi
	printf 'DROPSITE_RESULT admin_password_new=%s\n' "$ADMIN_PASSWORD_NEW"
}

# One line: bash has parsed it completely before main runs, so a checkout
# that replaces this file meanwhile (select_release) cannot be read on.
# DROPSITE_BOOTSTRAP_LIB=1 only defines the functions (test/install/unit.sh).
[ -n "${DROPSITE_BOOTSTRAP_LIB:-}" ] || { main "$@"; exit; }
