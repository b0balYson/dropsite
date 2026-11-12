# Installation details

The [README](../README.md) has the clone, configure and build path. This page covers the choices behind it.

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

1. Sign in with `ADMIN_EMAIL` and `ADMIN_PASSWORD`. The password variable is only read on the first start; change it later by issuing yourself a new invite (Users → reissue invite).
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
