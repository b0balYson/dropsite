# Installation details

## One-command installer

The easiest way: one command on **your own computer**. It connects to a freshly rented server, installs everything there and prepares your computer to open the admin interface.

### What you need

- A rented server (VPS) with **Ubuntu 22.04 or newer** or **Debian 12 or newer**, 1 GB of memory or more. Your hosting provider sends you its **IP address** and the **root password** (or lets you upload an SSH key).
- A **domain** for your sites, for example `sites.example.com`. It must not host anything else (see the security note in the README).
- Your **e-mail**: it becomes the administrator login and is used for the HTTPS certificates.

### Run it

**macOS or Linux:** open the Terminal app and paste:

```bash
curl -fsSL https://raw.githubusercontent.com/b0balYson/dropsite/main/scripts/install-server.sh | sh -s -- --lang en
```

**Windows:** run the same command in WSL (Ubuntu from the Microsoft Store) or Git Bash.

`--lang` switches the language of the installer: `en`, `ru` or `zh`. Without it the language follows your system.

Answer four questions: the server IP, how you sign in (root password or a key file), the domain and your e-mail. A fifth, short yes/no question asks whether to also open the admin interface from the internet at `https://admin.<domain>`; the default is **No** (press Enter). Opening it is more convenient but a potential threat: anyone can find the sign-in page ([security.md](security.md#admin-interface-from-the-internet)). With yes, the final block also shows that address; the SSH tunnel is configured either way. You can change the choice later in Settings. The short name of the server comes from the domain (`sites.example.com` gives `sites`); the installer asks for it only when another server in `~/.ssh/config` already uses that name. When ssh asks for the password, type the root password: it stays invisible while you type. The password is used **once** to add a dedicated key and is never saved anywhere. Installing takes 10-20 minutes.

The output stays short: a check mark for each step (connected to the server, installed Dropsite, the domain works, HTTPS is on), the two DNS records to create, and at the end the command to run, the address to open, the login and the generated password:

```text
Installing Dropsite. It takes 10-20 minutes, keep this window open.
  ✓ Connected to the server
  ✓ Installed Dropsite
  …
Done!

1. Run: ssh sites   (keep that window open)
2. Open: http://localhost:8082
   Login: me@example.com   Password: …   (save the password)
```

Every detail goes to the log file `~/.dropsite/install-<name>.log` on your computer (mode 600, readable by you only): each step, the whole output of the server and the commands it ran. The log never contains the root password, your keys or the generated administrator password. To see all of it on the screen as well, add `--verbose` (or set `DROPSITE_VERBOSE=1`): every server step, the summary before the start, the full DNS explanation with `AAAA` records and the commands installed on the server.

If a step fails, the installer says what failed in one sentence, shows the last 15 lines of the log and where the log is. Run the same command again: finished steps are skipped.

What the installer does:

1. Creates a key just for this server, `~/.ssh/dropsite_<name>`, and adds it to the server. From then on only this key is used.
2. Checks the server system. Other systems are refused with a clear message.
3. On the server: installs Docker from Docker's official repository, adds swap on servers with less than 2 GB of memory, opens only ports 22, 80 and 443 in the firewall (ufw), downloads Dropsite to `/opt/dropsite` and switches it to the newest release, writes the settings with a **generated administrator password**, downloads the prebuilt images of that release (nothing is compiled on the server), starts everything and checks that it answers. It also sets up a daily backup (the last 7 copies in `/var/backups/dropsite`; the form secrets key is copied once to `/root/dropsite-secrets.key`) and the commands below.
4. On your computer: adds a short entry `<name>` to `~/.ssh/config`, so `ssh <name>` connects to the server and opens a tunnel to the admin interface. The local port is the first free one starting from 8082 (another server may already use 8082).
5. Shows the **DNS records** to create where you bought the domain: `A` for `@` and for `*` with the server IPv4 address (`--verbose` also lists `AAAA` when the server has IPv6; the **Getting started** page shows them too). Then it waits (up to 30 minutes, checking every 10 seconds) until the domain points to the server and HTTPS works.

Ctrl+C while waiting for DNS changes nothing: the server is installed. Check later with:

```bash
curl -fsSL https://raw.githubusercontent.com/b0balYson/dropsite/main/scripts/install-server.sh | sh -s -- --check-dns --name <name>
```

If anything fails, run the same command again: finished steps are skipped. The log on your computer is `~/.dropsite/install-<name>.log`; the server keeps its own in `/var/log/dropsite-install.log`.

### Afterwards

| Task | How |
| --- | --- |
| Open the admin interface | run `ssh <name>`, keep the window open, open `http://localhost:<port>` (the port is shown at the end of the installation); or `https://admin.<domain>` if you opened it to the internet |
| Open or close the admin interface to the internet | Settings → "Access to the admin interface from the internet", or `ssh <name> 'cd /opt/dropsite/deploy && docker compose exec -T control /control public-admin on'` (`off`, `status`) |
| Sign in | the e-mail you entered and the generated password shown once at the end; change it on the Account page |
| Finish the setup | the **Getting started** page (first item of the menu): it checks that the domain points to the server and that HTTPS works, shows the exact DNS records with copy buttons and lists the remaining steps |
| Connect ChatGPT or Claude | the **Connect AI** page of the admin interface |
| Update | the **Update** button in Settings → Updates when a new release is out, or `ssh <name> dropsite-update` (backup, switch to the newest release, download its images, restart); see [Updating](#updating) |
| Undo the last update | `ssh <name> dropsite-rollback` (previous release and the backup taken right before the update) |
| Stay on one release | set `DROPSITE_VERSION=X.Y.Z` in `/opt/dropsite/deploy/.env`, then `dropsite-update`; `latest` follows new releases again ([releasing.md](releasing.md#on-a-server)) |
| Forgot the admin password | `ssh <name> dropsite-admin-password` prints a new one |
| Back up now | `ssh <name> dropsite-backup` |

### Automation

Every answer can also be passed as an option or an environment variable, for example `curl ... | sh -s -- --host 203.0.113.7 --name sites --domain sites.example.com --email me@example.com --yes`. Options: `--host`, `--port`, `--key` (a private key file instead of the password), `--name`, `--domain`, `--email`, `--public-admin` / `--no-public-admin`, `--skip-dns`, `--check-dns`, `--yes`, `--verbose`, `--lang`; the matching variables are `DROPSITE_HOST`, `DROPSITE_SSH_PORT`, `DROPSITE_SSH_KEY`, `DROPSITE_NAME`, `DROPSITE_DOMAIN`, `DROPSITE_EMAIL`, `DROPSITE_PUBLIC_ADMIN=1|0`, `DROPSITE_SKIP_DNS=1`, `DROPSITE_CHECK_DNS=1`, `DROPSITE_YES=1`, `DROPSITE_VERBOSE=1`, `DROPSITE_LANG`. Without a terminal or with `--yes` the public-admin question is not asked: unless `--public-admin` is given, a new server keeps the interface behind the SSH tunnel and an existing one keeps its current choice. The language comes from `--lang`, then `DROPSITE_LANG`, then the system locale. Without a terminal the server name comes from the domain, or `dropsite-<name>` when that one is taken. The server part, [`scripts/server-bootstrap.sh`](../scripts/server-bootstrap.sh), can also be run by hand as root on the server.

Limits: the installer signs in as `root` (providers that give a sudo user instead are not supported yet); only `~/.ssh/config` itself is read when choosing the name and the port, not files it includes.

For developers: `make install-test` checks the installer logic and the `~/.ssh/config` editing against a temporary home directory; `make install-e2e` runs the whole installer against a disposable Ubuntu 24.04 container on the local Docker engine. `make lint` runs shellcheck on the shell scripts when a `shellcheck` binary is in `.tools/bin` or on `PATH`.

## Manual installation

The [README](../README.md) has the short path. The sections below cover the choices behind it.

## Server

- Linux, x86-64 or arm64, Git, Docker Engine 24+ with Compose 2.24.4+ (the YAML overrides require a recent Compose).
- Sizing target: 2 vCPU and 4 GB RAM serve up to 10 000 sites and 1000 users. The `control` service idles below 100 MB.
- Open ports 80 and 443 (TCP) and 443/UDP for HTTP/3. Port 8082 must stay closed to the internet.

## Domain and DNS

1. Pick a dedicated base domain (see the security note in the README).
2. Create `A`/`AAAA` records for the domain and for `*.<domain>` pointing at the server.
3. The API lives on `api.<domain>` by default (`API_HOST`). The wildcard covers this name unless an existing record prevents wildcard matching; in that case, add an explicit `A`/`AAAA` record for the API host. The slug `api` and other system names are reserved automatically.
4. With Cloudflare, check that the zone is **Active** and that the domain registrar uses the nameservers assigned by Cloudflare.
5. Check DNS from outside the server before connecting AI assistants: `dig +short <domain>` and `dig +short api.<domain>` must return the server IP. If the zone is hosted by Cloudflare, use **DNS only** while setting up Caddy.

Certificates are issued on first visit; see [tls.md](tls.md) for limits.

## Release checkout

Every release is a Git tag `vX.Y.Z` with prebuilt images for linux/amd64 and linux/arm64 on ghcr.io (`ghcr.io/b0balyson/dropsite-control`, and `ghcr.io/b0balyson/dropsite-caddy`, Caddy with the plugins every TLS mode needs). Check out the tag of the release you run, so the compose file and the Caddyfile belong to the same release as the images:

```bash
git clone https://github.com/b0balYson/dropsite.git
cd dropsite
git checkout "$(git tag -l 'v*' --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -n 1)"
cd deploy
cp .env.example .env
chmod 600 .env
# Set BASE_DOMAIN, ACME_EMAIL, ADMIN_EMAIL and ADMIN_PASSWORD.
# DROPSITE_VERSION=latest runs the newest release; set the version of the
# tag you checked out (X.Y.Z) to pin it.
nano .env
docker compose config --quiet
docker compose pull
docker compose up -d --wait
docker compose ps
```

`docker compose pull` downloads the images; `up` starts them as they are and never builds unless asked. The images are public: no registry login is needed. To check that an image was built by this repository's release workflow: `gh attestation verify oci://ghcr.io/b0balyson/dropsite-control:X.Y.Z --repo b0balYson/dropsite`.

Run all commands below from `dropsite/deploy`. Your `.env` is ignored by Git. Keep the checkout directory and Compose project name unchanged during updates so Docker reuses the data volumes.

## Building from source

Without a published release, for a modified checkout, or on a platform without images, build the images from the checkout instead:

```bash
docker compose up -d --build --wait
```

Keep the entire repository. The build context for `control` is its root, one directory above `deploy`. Go runs inside Docker; no host Go installation is needed. Docker still downloads upstream base images and Go dependencies. The first build can take several minutes; later builds reuse the compiler cache. The built images get the tag of `DROPSITE_VERSION` locally; set `DROPSITE_BUILD_VERSION="$(sh ../scripts/version.sh)"` in the environment to make `control` report `dev+<commit>` instead of `dev`. The one-command installer builds from source with `DROPSITE_BUILD=1`, and falls back to it on its own when the images of a release cannot be downloaded.

## Build memory

This applies only to building from source. For a server with 1 CPU and 1 GB RAM, we recommend 2 GB swap for a source build. This is guidance, not a measured minimum for every architecture. Runtime memory and compiler memory are different: compiling the SQLite dependency can use much more memory than running the server.

The Dockerfiles compile packages one at a time, limit concurrent Go execution to one CPU, and run garbage collection more often. `GOMEMLIMIT=256MiB` is a soft Go memory target **per process**, not a hard limit on the whole build. The build can exceed it. These settings reduce memory use at the cost of build time.

If the log ends with `modernc.org/sqlite/lib: ... compile: signal: killed`, the compiler was terminated. Lack of memory is a likely cause; confirm it on the Linux server:

```bash
free -h
swapon --show
sudo journalctl -k --since '10 minutes ago' | tail -100
```

Look for `Out of memory` or `Killed process`. If confirmed, add RAM or swap using your server provider's instructions, or increase the Docker/VM memory limit. Then repeat `docker compose up -d --build --wait`; the completed build steps remain cached. If the kernel log has no memory error, investigate the host or container limits before assuming an out-of-memory failure.

## Files

| File | Purpose |
| --- | --- |
| `docker-compose.yml` | Services `caddy-init`, `caddy`, `control` (release image of `DROPSITE_VERSION`, or a source build with `--build`); volumes `sites`, `state`, `caddy_data`, `logs` |
| `Caddyfile` | Static Caddy configuration; do not edit unless you know why each rule is there |
| `.env` | Your settings, from `.env.example` |

Volumes:

| Volume | Mounted in | Contents |
| --- | --- | --- |
| `sites` | control (rw), caddy (ro) | releases and host-name symlinks |
| `state` | control | SQLite database, temporary uploads |
| `caddy_data` | caddy | certificates and ACME account |
| `logs` | caddy (rw), control (ro) | access log of published sites for statistics, rotated by Caddy (see [statistics.md](statistics.md)) |

## Web interface access

The interface is served over plain HTTP on `ADMIN_BIND_IP:8082` and must only be reachable from a private network:

- **Single admin:** keep the default `127.0.0.1` and use `ssh -L 8082:127.0.0.1:8082 server`.
- **Team:** join the server to an overlay network (Tailscale, WireGuard, NetBird), set `ADMIN_BIND_IP` to its overlay address and `UI_URL=http://<overlay address>:8082`, then `docker compose up -d --wait`.

Invite links use `UI_URL`; make sure it is the address your colleagues can open.

`control` checks this at start: on a public `ADMIN_BIND_IP` (or `0.0.0.0`) it refuses to start unless `ALLOW_PUBLIC_ADMIN=1` is set, and on a private address it logs a warning that the interface uses plain HTTP. See [security.md](security.md).

- **From the internet (opt-in):** Settings → "Access to the admin interface from the internet" serves the interface at `https://admin.<BASE_DOMAIN>` through Caddy (HTTPS, a certificate on demand or the wildcard one). Nothing else needs configuring: `admin` is covered by the `*` DNS record, the Caddy block is always there and `control` serves it only while the switch is on. It is a potential threat; read [security.md](security.md#admin-interface-from-the-internet) first.

## First steps

1. Sign in with `ADMIN_EMAIL` and `ADMIN_PASSWORD`. The password variable is only read on the first start: once you have signed in, remove `ADMIN_PASSWORD` from `.env` and run `docker compose up -d control` (the server installer does this itself), otherwise it stays visible through `docker inspect` and `control` logs a warning. Change the password later by issuing yourself a new invite (Users → reissue invite). Locked out? On the server run `docker compose exec -T control /control reset-password [email]` (default: `ADMIN_EMAIL`): it prints a new random password and ends that user's sessions.
2. Create users and send them invite links.
3. Optionally bind a site to the base domain root (All sites → bind root).
4. Review limits in Settings.

## CLI from source

The web interface can publish sites without a CLI. The CLI installers download a binary of a GitHub Release and check it against `SHA256SUMS` (`DROPSITE_VERSION=X.Y.Z` picks a release, default the newest); the CLI prints a one-line hint when it and the server are a minor version or more apart. Without a release, or to run a modified checkout, build the CLI on the computer where you use your AI assistants. Install the Go version declared in `go.mod`, then run from the repository root:

```bash
go build -o ./bin/dropsite ./cmd/dropsite
./bin/dropsite login --server https://api.example.com --code CODE_FROM_CONNECT_PAGE
./bin/dropsite setup
./bin/dropsite deploy ./path/to/site
```

Replace the server URL, connect code and site path. Use `./bin/dropsite.exe` as the output and command name on Windows. Copy the binary to a directory on `PATH` if you want to run it as `dropsite`. `setup` records its absolute path for Claude Desktop; keep the binary at that path.

## Updating

**The notice.** About twice a day (and shortly after a start) `control` asks the GitHub API for the newest stable Dropsite release. When it is newer than the running version, administrators see a calm notice in the sidebar: "Version X.Y.Z is available · What's new →", linking to the release page with its notes. Other users see nothing. Development builds (`dev+<commit>`) are never compared.

**Settings → Updates** shows the running version, the latest release, when it was last checked (with **Check now**, limited to 5 checks per 10 minutes), the what's-new link and the **Update to X.Y.Z** button. The button asks for confirmation: the server takes a backup first, the panel restarts for about a minute, the sites keep working, and `dropsite-rollback` on the server undoes the update. While the update runs the section shows its progress; when the panel is restarting the page says "Updating… the page reloads by itself" and reloads once the panel answers. Afterwards it shows "Updated to X.Y.Z", or the error with the last lines of the log (the previous version keeps running then).

**How the button works.** `control` has no Docker access and cannot update itself. It writes a small request file (`update/request` in its state volume: the target version and the administrator's ID) and nothing else. `dropsite-update-watch`, a root job the installer sets up (cron every minute, plus a systemd path unit where systemd runs), checks the request strictly, runs `dropsite-update --to X.Y.Z` and writes the progress back to `update/status`. It also writes a heartbeat every minute; without one (a manual installation, or an older server that has not run `dropsite-update` since) the section shows the command instead of the button: `ssh <server> dropsite-update`. The request and its checks are described in [security.md](security.md#updates); the job logs to `/var/log/dropsite-update.log`.

**Older servers** get the helper with their next `dropsite-update`: it reinstalls the helper commands of the release it switches to (`server-bootstrap.sh --install-helpers`), so one manual update turns the button on.

**Turning the check off.** `UPDATE_CHECK=0` in `/opt/dropsite/deploy/.env` (then `dropsite-update`, or `docker compose up -d`) stops the outgoing request; the section then only shows the running version, and you update with `ssh <server> dropsite-update`.

**By hand.** `ssh <server> dropsite-update` works as before, and `dropsite-update --to X.Y.Z` installs exactly that release. One update runs at a time.

See also [backup and upgrade](backup.md#upgrade) and [releasing.md](releasing.md#on-a-server). An update checks out the tag of the new release and downloads its images; a source build stays available as a fallback.
