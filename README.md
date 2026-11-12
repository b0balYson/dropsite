# pushpage

[Русская версия](README.ru.md)

Self-hosted publishing of static sites. A marketer builds a landing page with an AI assistant, says "publish it" or drops a zip into the web interface, and gets an HTTPS link on a subdomain of your domain in seconds. No VPS per site, no git, no DevOps.

- One server, one command: `docker compose up -d`.
- Archive in, HTTPS link out. Every site gets a random address such as `k3f9x2m7qa.example.site` and an optional readable one such as `promo-may.example.site`.
- Every publish is an immutable release: switch and roll back instantly, with zero failed requests during the switch.
- Each user sees and changes only their own sites. Admins manage users, all sites, limits and the audit log.
- Publish from the web interface, the `pushpage` CLI, the HTTP API, or an AI assistant (Claude Code, Codex) through the CLI.
- Sites keep working while the control service is stopped or being upgraded.

pushpage serves finished static files only (HTML, CSS, JS, images). It does not build projects or run server code.

## How it works

Two containers. **Caddy** serves files by host name and obtains TLS certificates on demand; its configuration is static and never reloads. **control** (one Go binary) owns the database, accounts, the API and the web interface, and publishes releases by atomically switching symlinks on a shared volume that Caddy reads.

## Install in 5 minutes

Requirements: a Linux server with Docker and the Compose plugin, ports 80 and 443 open, and a domain.

### 1. Choose a dedicated base domain

Use a domain (or a subdomain such as `sites.example.com`) that hosts **nothing else**. Published sites run arbitrary JavaScript on its subdomains; never put company services, logins or cookies on the base domain or share cookies with it. For public installations with untrusted users, consider adding the domain to the [Public Suffix List](https://publicsuffix.org/) so browsers isolate sites from each other.

### 2. Point DNS at the server

| Record | Name | Value |
| --- | --- | --- |
| A (and AAAA if you have IPv6) | `example.site` | server IP |
| A (and AAAA) | `*.example.site` | server IP |

### 3. Configure and start

```bash
mkdir pushpage && cd pushpage
base=https://raw.githubusercontent.com/b0balYson/dropsite/main/deploy
curl -fsSLO "$base/docker-compose.yml" -fsSLO "$base/Caddyfile"
curl -fsSL "$base/.env.example" -o .env
# Fill in BASE_DOMAIN, ACME_EMAIL, ADMIN_EMAIL, ADMIN_PASSWORD
nano .env
docker compose up -d
```

### 4. Open the web interface

The interface listens on `ADMIN_BIND_IP:8082`, by default `127.0.0.1:8082`, and is **never exposed to the internet**. Reach it through an SSH tunnel:

```bash
ssh -L 8082:127.0.0.1:8082 you@server
# then open http://127.0.0.1:8082 and sign in with ADMIN_EMAIL / ADMIN_PASSWORD
```

For a team, set `ADMIN_BIND_IP` to the server address in a private overlay network (Tailscale, WireGuard, NetBird) and set `UI_URL` to the address people open, for example `http://100.64.0.10:8082`. The interface uses plain HTTP and must stay on a private network.

Then: **Users → create user**, send them the invite link (valid 72 hours); they set a password and publish.

## Publishing

- **Web interface:** create a site, drag a zip or tar.gz onto it.
- **CLI:** on the **Connect AI** page issue a token and run the shown `pushpage login` command, then `pushpage deploy ./dist`. The first run creates a site and remembers it in `.pushpage.json`; later runs update it.
- **AI assistants:** the **Connect AI** page has ready instructions for Claude Code (`skills/claude-code/SKILL.md`) and Codex (`skills/codex/AGENTS.md`). The assistant publishes through the CLI.
- **HTTP API:** `https://api.<BASE_DOMAIN>/api/v1`, bearer token, see [docs/api-contract.md](docs/api-contract.md) and `/api/v1/openapi.json`.

Install the CLI (Linux, macOS):

```bash
curl -fsSL https://raw.githubusercontent.com/b0balYson/dropsite/main/scripts/install.sh | sh
```

Windows (PowerShell):

```powershell
irm https://raw.githubusercontent.com/b0balYson/dropsite/main/scripts/install.ps1 | iex
```

### What a site archive may contain

`index.html` at the root (a single top-level folder such as `dist/` is unwrapped automatically). Up to 50 MB unpacked, 20 MB per file, 2000 files by default. Symlinks, absolute paths and `..` are rejected. `.git`, `node_modules`, `__MACOSX`, `.DS_Store`, `Thumbs.db` are skipped.

### Serving rules

| Behaviour | Rule |
| --- | --- |
| Index | `/` and `/dir/` serve `index.html`; `/dir` redirects to `/dir/` |
| Clean URLs | `/about` serves `about.html` |
| Custom 404 | `404.html` at the root is served with status 404 |
| Single-page apps | `200.html` at the root is served with status 200 for any unknown path |
| Hidden files | any path segment starting with a dot is 404, except `/.well-known/` |
| Reserved | `/_form/` and `/_pushpage/` belong to the platform |
| Caching | HTML `no-cache`; other files `public, max-age=3600` |

## TLS certificates and Let's Encrypt limits

By default every address gets its own certificate on first visit (`TLS_MODE=ondemand`). Caddy only requests a certificate for addresses that exist.

Let's Encrypt allows **50 new certificates per registered domain per 7 days** (as of August 2026, see [rate limits](https://letsencrypt.org/docs/rate-limits/)). A site with a slug uses two certificates (random address and slug), so plan for about **25 new sites per week**. Renewals do not count. Caddy falls back to ZeroSSL when Let's Encrypt refuses. If you grow beyond that, switch to a wildcard certificate: see [docs/tls.md](docs/tls.md).

Every issued certificate is published in public Certificate Transparency logs, so site addresses are not secret.

## Configuration

| Variable | Required | Default | Purpose |
| --- | --- | --- | --- |
| `BASE_DOMAIN` | yes | | Base domain for sites |
| `ACME_EMAIL` | yes | | ACME account email |
| `ADMIN_EMAIL` | yes | | First administrator login |
| `ADMIN_PASSWORD` | yes | | Their password; used only on the very first start |
| `ADMIN_BIND_IP` | no | `127.0.0.1` | Address the web interface listens on |
| `UI_URL` | no | `http://<ADMIN_BIND_IP>:8082` | Address used in invite links |
| `API_HOST` | no | `api.<BASE_DOMAIN>` | Host of the public API |
| `TLS_MODE` | no | `ondemand` | Only `ondemand` in this version |
| `MAX_SITE_BYTES`, `MAX_FILE_BYTES`, `MAX_FILES`, `MAX_SITES_PER_USER`, `KEEP_RELEASES` | no | 50 MB, 20 MB, 2000, 10, 5 | Limits; admins can override them in the interface |
| `RESERVED_SLUGS` | no | | Extra forbidden slugs, comma separated |
| `LOG_LEVEL` | no | `info` | `debug`, `info`, `warn`, `error` |

## Operations

- **Backup:** `docker compose exec -T control /control backup - > backup-$(date +%F).tar.gz`. See [docs/backup.md](docs/backup.md) for restore.
- **Upgrade:** back up, then `docker compose pull && docker compose up -d`. Migrations run automatically; downgrades are not supported.
- **Logs:** JSON on stdout of both containers: `docker compose logs -f`.
- **Health:** `docker compose ps` shows the `control` health check.

More: [docs/install.md](docs/install.md), [docs/backup.md](docs/backup.md), [docs/tls.md](docs/tls.md), [docs/production-checklist.md](docs/production-checklist.md).

## Security model

- The web interface is on a separate listener that is not reachable from the internet; its session cookie is bound to the interface host only.
- The API accepts only bearer tokens (never cookies). Tokens are shown once, stored as hashes, can expire and be revoked.
- Another user's site always answers "not found".
- Archives are validated before anything is written: no path traversal, links, devices or zip bombs.
- Caddy mounts the sites volume read-only and has no access to the database or tokens. Both containers run as non-root with read-only root filesystems.

## Development

```bash
make tools        # Go and golangci-lint into .tools/ (nothing system-wide)
make test lint
scripts/dev-control.sh   # run control natively, UI on http://127.0.0.1:18082
make dev-up       # full stack in Docker with a local CA: https://<site>.pushpage.localhost:18443
make e2e
```

## License

Apache-2.0
