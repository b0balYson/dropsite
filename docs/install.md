# Installation details

## One-command installer

The easiest way: one command on **your own computer**. It connects to a freshly rented server, installs everything there and prepares your computer to open the admin interface.

### What you need

- A rented server (VPS) with **Ubuntu 22.04**, **Ubuntu 24.04** or **Debian 12**, 1 GB of memory or more. Your hosting provider sends you its **IP address** and the **root password** (or lets you upload an SSH key).
- A **domain** for your sites, for example `sites.example.com`. It must not host anything else (see the security note in the README).
- Your **e-mail**: it becomes the administrator login and is used for the HTTPS certificates.

### Run it

**macOS or Linux:** open the Terminal app and paste:

```bash
curl -fsSL https://raw.githubusercontent.com/b0balYson/dropsite/main/scripts/install-server.sh | sh
```

**Windows:** run the same command in WSL (Ubuntu from the Microsoft Store) or Git Bash.

Answer the questions. The questions are in English, Russian or Chinese depending on your system language. When ssh asks for the password, type the root password: it stays invisible while you type. The password is used **once** to add a dedicated key and is never saved anywhere. Installing takes 10-20 minutes.

What the installer does:

1. Creates a key just for this server, `~/.ssh/dropsite_<name>`, and adds it to the server. From then on only this key is used.
2. Checks the server system. Other systems are refused with a clear message.
3. On the server: installs Docker from Docker's official repository, adds swap on servers with less than 2 GB of memory, opens only ports 22, 80 and 443 in the firewall (ufw), downloads dropsite to `/opt/dropsite`, writes the settings with a **generated administrator password**, starts everything and checks that it answers. It also sets up a daily backup (the last 7 copies in `/var/backups/dropsite`; the form secrets key is copied once to `/root/dropsite-secrets.key`) and the commands below.
4. On your computer: adds a short entry `<name>` to `~/.ssh/config`, so `ssh <name>` connects to the server and opens a tunnel to the admin interface. The local port is the first free one starting from 8082 (another server may already use 8082).
5. Shows the **DNS records** to create where you bought the domain: `A` for `@` and for `*` with the server IPv4 address, and `AAAA` too when the server has IPv6. Then it can wait (up to 30 minutes, checking every 10 seconds) until the domain points to the server and HTTPS works.

Ctrl+C while waiting for DNS changes nothing: the server is installed. Check later with:

```bash
curl -fsSL https://raw.githubusercontent.com/b0balYson/dropsite/main/scripts/install-server.sh | sh -s -- --check-dns --name <name>
```

If anything fails, run the same command again: finished steps are skipped. The server log is `/var/log/dropsite-install.log`.

### Afterwards

| Task | How |
| --- | --- |
| Open the admin interface | run `ssh <name>`, keep the window open, open `http://localhost:<port>` (the port is shown at the end of the installation) |
| Sign in | the e-mail you entered and the generated password shown once at the end; change it on the Account page |
| Finish the setup | the **Getting started** page (first item of the menu): it checks that the domain points to the server and that HTTPS works, shows the exact DNS records with copy buttons and lists the remaining steps |
| Connect ChatGPT or Claude | the **Connect AI** page of the admin interface |
| Update | `ssh <name> dropsite-update` (backup, download, restart) |
| Undo the last update | `ssh <name> dropsite-rollback` (restores the backup taken right before the update) |
| Forgot the admin password | `ssh <name> dropsite-admin-password` prints a new one |
| Back up now | `ssh <name> dropsite-backup` |

### Automation

Every answer can also be passed as an option or an environment variable, for example `curl ... | sh -s -- --host 203.0.113.7 --name sites --domain sites.example.com --email me@example.com --yes`. Options: `--host`, `--port`, `--key` (a private key file instead of the password), `--name`, `--domain`, `--email`, `--skip-dns`, `--check-dns`, `--yes`; the matching variables are `DROPSITE_HOST`, `DROPSITE_SSH_PORT`, `DROPSITE_SSH_KEY`, `DROPSITE_NAME`, `DROPSITE_DOMAIN`, `DROPSITE_EMAIL`, `DROPSITE_SKIP_DNS=1`, `DROPSITE_CHECK_DNS=1`, `DROPSITE_YES=1`. `DROPSITE_LANG` (`en`, `ru`, `zh`) overrides the language. The server part, [`scripts/server-bootstrap.sh`](../scripts/server-bootstrap.sh), can also be run by hand as root on the server.

Limits: the installer signs in as `root` (providers that give a sudo user instead are not supported yet); only `~/.ssh/config` itself is read when choosing the name and the port, not files it includes.

For developers: `make install-test` checks the installer logic and the `~/.ssh/config` editing against a temporary home directory; `make install-e2e` runs the whole installer against a disposable Ubuntu 24.04 container on the local Docker engine. `make lint` runs shellcheck on the shell scripts when a `shellcheck` binary is in `.tools/bin` or on `PATH`.

## Manual installation

The [README](../README.md) has the clone, configure and build path. The sections below cover the choices behind it.

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

## Source checkout

```bash
git clone https://github.com/b0balYson/dropsite.git
cd dropsite/deploy
cp .env.example .env
chmod 600 .env
# Set BASE_DOMAIN, ACME_EMAIL, ADMIN_EMAIL and ADMIN_PASSWORD.
nano .env
docker compose config --quiet
docker compose up -d --build --wait
docker compose ps
```

Keep the entire repository. The build context for `control` is its root, one directory above `deploy`. Go runs inside Docker; no host Go installation or dropsite registry login is needed. Docker still downloads upstream base images and Go dependencies. The first build can take several minutes; later builds reuse the compiler cache.

Run all commands below from `dropsite/deploy`. Your `.env` is ignored by Git. Keep the checkout directory and Compose project name unchanged during updates so Docker reuses the data volumes.

## Build memory

For a server with 1 CPU and 1 GB RAM, we recommend 2 GB swap for a source build. This is guidance, not a measured minimum for every architecture. Runtime memory and compiler memory are different: compiling the SQLite dependency can use much more memory than running the server.

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
| `docker-compose.yml` | Local source build for `control`; services `caddy-init`, `caddy`, `control`; volumes `sites`, `state`, `caddy_data`, `logs` |
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
- **Team:** join the server to an overlay network (Tailscale, WireGuard, NetBird), set `ADMIN_BIND_IP` to its overlay address and `UI_URL=http://<overlay address>:8082`, then `docker compose up -d --build --wait`.

Invite links use `UI_URL`; make sure it is the address your colleagues can open.

## First steps

1. Sign in with `ADMIN_EMAIL` and `ADMIN_PASSWORD`. The password variable is only read on the first start; change it later by issuing yourself a new invite (Users → reissue invite). Locked out? On the server run `docker compose exec -T control /control reset-password [email]` (default: `ADMIN_EMAIL`): it prints a new random password and ends that user's sessions.
2. Create users and send them invite links.
3. Optionally bind a site to the base domain root (All sites → bind root).
4. Review limits in Settings.

## CLI from source

The web interface can publish sites without a CLI. The CLI installer is separate: it needs a published GitHub Release. Until a release exists, build the CLI on the computer where you use your AI assistants. Install the Go version declared in `go.mod`, then run from the repository root:

```bash
go build -o ./bin/dropsite ./cmd/dropsite
./bin/dropsite login --server https://api.example.com --code CODE_FROM_CONNECT_PAGE
./bin/dropsite setup
./bin/dropsite deploy ./path/to/site
```

Replace the server URL, connect code and site path. Use `./bin/dropsite.exe` as the output and command name on Windows. Copy the binary to a directory on `PATH` if you want to run it as `dropsite`. `setup` records its absolute path for Claude Desktop; keep the binary at that path.

## Updates

See [backup and upgrade](backup.md#upgrade). Updates use `git pull --ff-only` and a local rebuild, not a pull of a dropsite image.
