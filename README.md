<h1 align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset=".github/assets/logo-dark.svg">
    <source media="(prefers-color-scheme: light)" srcset=".github/assets/logo.svg">
    <img src=".github/assets/logo.svg" alt="dropsite" width="300">
  </picture>
</h1>

**English** | [Русский](README.ru.md) | [简体中文](README.zh-CN.md)

dropsite publishes websites on your own server. You ask ChatGPT or Claude "make a site for my coffee shop and publish it", and a minute later the site is live at an HTTPS address on your own domain. Changes, form requests, visit numbers and passwords are just more requests in the same chat.

dropsite serves finished static sites (HTML, CSS, JavaScript, images). It does not build projects or run server code.

## What you need

- **A rented server (VPS)** with Ubuntu 22.04 or newer, or Debian 12 or newer, and 1 GB of memory or more. The hosting provider gives you its IP address and the root password (or lets you add an SSH key).
- **A domain** that hosts nothing else, for example `sites.example.com`.
- **ChatGPT or Claude** to make sites by asking. Without a chat you can still upload sites in the web interface.

## Install in one command

Run this on **your own computer**, not on the server. On macOS and Linux open the Terminal app; on Windows use WSL (Ubuntu from the Microsoft Store) or Git Bash.

```bash
curl -fsSL https://raw.githubusercontent.com/b0balYson/dropsite/main/scripts/install-server.sh | sh
```

The installer asks for the server IP, the root password (or a key file), a short name for the server, your domain and your e-mail, in English, Russian or Chinese depending on your system language. Then it:

1. creates a key just for this server, so the password is used once and never saved;
2. installs Docker and dropsite on the server, opens only ports 22, 80 and 443, turns on a daily backup and starts everything (10–20 minutes);
3. adds the server to `~/.ssh/config` on your computer, so `ssh <name>` connects and opens a private tunnel to the admin interface;
4. tells you which DNS records to create where you bought the domain: an `A` record for `@` and for `*` with the server IP (plus `AAAA` when the server has IPv6), and waits until the domain works;
5. prints the admin login and a generated password, and how to connect ChatGPT or Claude.

If something fails, run the same command again: finished steps are skipped. Details and options: [docs/install.md](docs/install.md#one-command-installer).

## First steps

1. Run `ssh <name>`, keep that window open, and open `http://localhost:<port>` in the browser (the installer shows the port). Sign in with your e-mail and the generated password.
2. The **Getting started** page is a checklist that checks the server for you: the domain points here, HTTPS works, a chat is connected, the first site exists. It shows the exact DNS records with copy buttons and what to fix if a step fails. Optional steps: invite a teammate and set up Telegram or e-mail for form requests.
3. Open **Connect AI**, pick your chat and follow the numbered steps. You add the address `https://api.<your domain>/mcp` as a connector, then press **Connect** and type the one-time connect code into the dropsite page that opens. Your password is never asked for in a chat.
4. Ask the chat: "Make a site for my coffee shop and publish it".

## What you can do

### Make and change sites by asking

- **Publish from a chat.** ChatGPT, claude.ai, Claude Desktop and Claude Code build the site, publish it and answer with a working link. Sites are self-contained (their own images and fonts, no third-party CDNs), made for phones first and quick to load.
- **Change a site from any new chat.** The assistant reads the published files from the server, changes only what you asked for and publishes a new version.
- **Or publish without a chat.** Drag a zip or tar.gz onto a site in the web interface, run `dropsite deploy ./folder`, or call the HTTP API.
- **Show a site you like.** Give a link or a picture, and you get an original site in a similar style: palette, fonts, spacing and layout, never the other site's texts, images or logo ([docs/references.md](docs/references.md)).
- **Start from a template when you ask for one.** Five starter sites (cafe, services, event, portfolio, links) in English or Russian, each with a working request form; otherwise the assistant designs each site from scratch ([docs/screenshots.md](docs/screenshots.md#templates)).
- **Copy a site** into a new one to try a different version without touching the original.

### Publish safely

- **Drafts with preview.** Save a new version as a draft and check it at the site's private preview address; visitors keep seeing the current version until you press **Publish**. On a live site the assistant makes a draft and asks before publishing.
- **Scheduled publishing.** Prepare a version now and let it go live at a given time, up to a year ahead.
- **Versions and rollback.** Every publication is a separate version. Going back to a previous one is instant, and visitors never see a half-updated site.
- **Quality report.** Every publication gets a score with broken links, missing files, files loaded from other servers, images without `alt`, pages without a title or description and the heaviest pages ([docs/quality.md](docs/quality.md)).
- **Automatic optimization.** WebP copies of JPEG and PNG images for browsers that support them, plus `sitemap.xml`, `robots.txt` and a favicon when the site has none. Your own files are never changed ([docs/publishing.md](docs/publishing.md)).
- **Screenshots and before/after comparison.** With the optional renderer the assistant can look at its own work on desktop and phone and compare two versions ([docs/screenshots.md](docs/screenshots.md)).

### Collect requests and keep content live

- **Forms without a third-party service.** Requests are stored on your server and delivered to Telegram, e-mail or a signed HTTPS webhook, with retries. Anti-bot protection: a hidden honeypot field, rate limits and an optional self-hosted Altcha check. Browse requests on the site page and export them to CSV ([docs/forms.md](docs/forms.md)).
- **Site data you can edit from a chat.** Menus, prices, catalogs, schedules or reviews live in data collections that pages load. "Change the cappuccino price to 250" takes effect at once, without a new version. A collection is either public (only you write) or moderated (visitors propose entries, you approve them). Ready collection templates: menu, catalog, services, events, team, FAQ, reviews.
- **Google Sheets.** Send form requests to a spreadsheet, or fill a data collection from one, manually or on a schedule ([docs/sheets.md](docs/sheets.md)).
- **API keys stay secret.** A page can call a third-party API (OpenAI, a CRM, a Telegram bot) through your server, which adds the key. You paste the key only in the web interface; the chat and the API never accept or show it ([docs/proxy.md](docs/proxy.md)).

### Control who sees what

- **Password-protected sites**, hidden from search engines, or with a last day after which the site closes ([docs/site-access.md](docs/site-access.md)).
- **Your own domains.** Open a site at `promo.example.com` in addition to its address on the base domain; the assistant tells you which DNS records to create, and an administrator approves the domain ([docs/external-domains.md](docs/external-domains.md)).
- **Shared workspaces for teams.** Create a workspace, add colleagues, and everyone in it can manage its sites. Every user otherwise sees only their own sites.
- **Statistics without scripts.** Page views and visitors per day, top pages and sources, counted from the server log. Add a Yandex Metrica or Google Analytics 4 counter by ID if you want more ([docs/statistics.md](docs/statistics.md)).

### Run it with confidence

- **Audit log.** Administrators see sign-ins, publications, rollbacks, user, domain, form and settings changes.
- **Three interface languages:** English, Russian and Simplified Chinese.
- **Prebuilt images and versioned releases** for amd64 and arm64 with signed build provenance; nothing is compiled on the server ([docs/releasing.md](docs/releasing.md)).
- **Backups and one-command update and rollback:** a daily backup, `dropsite-update`, `dropsite-rollback`.
- **Sites keep working** while the management service is stopped or being updated.

## Details

### Requirements

- Linux server, amd64 or arm64; Git, Docker Engine 24+ and Compose 2.24.4+ (the one-command installer sets these up).
- Ports 80 and 443 (TCP) open, and 443/UDP for HTTP/3. The admin port 8082 must stay closed to the internet.
- 2 vCPU and 4 GB RAM serve up to 10 000 sites and 1000 users; 1 GB RAM is enough to run from prebuilt images. Building from source on 1 CPU and 1 GB needs 2 GB swap ([build memory](docs/install.md#build-memory)).
- A dedicated base domain. Published sites run arbitrary JavaScript on its subdomains: never put company services, logins or cookies on it. For public installations with untrusted users consider adding it to the [Public Suffix List](https://publicsuffix.org/).

How it works: two containers. **Caddy** serves the sites by host name and obtains HTTPS certificates; its configuration is static. **control** (one Go binary) holds the database, accounts, the API, the MCP server and the web interface, and publishes a version by atomically switching a link that Caddy reads.

### Installation options

**One-command installer** (above). Every answer can be passed as an option for automation (`--host`, `--name`, `--domain`, `--email`, `--key`, `--yes`, …), and `--check-dns --name <name>` checks DNS later: [docs/install.md](docs/install.md#one-command-installer). The installer signs in as `root`; providers that give only a sudo user are not supported yet.

**Manual installation with Docker Compose.** Create DNS records `A`/`AAAA` for `example.site` and `*.example.site` pointing at the server (the `*` record also covers `api.example.site`), then:

```bash
git clone https://github.com/b0balYson/dropsite.git
cd dropsite
# The newest release tag: compose file and Caddyfile of the same release as the images.
git checkout "$(git tag -l 'v*' --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -n 1)"
cd deploy
cp .env.example .env
chmod 600 .env
# Fill in BASE_DOMAIN, ACME_EMAIL, ADMIN_EMAIL, ADMIN_PASSWORD
nano .env
docker compose config --quiet
docker compose pull
docker compose up -d --wait
docker compose ps
```

`control` must be `healthy` and `caddy` `running`; on failure see `docker compose logs --tail=100 control caddy`. The web interface listens on `127.0.0.1:8082` only: open it through `ssh -L 8082:127.0.0.1:8082 you@server`. For a team, bind it to the server's address in a private overlay network (Tailscale, WireGuard, NetBird) with `ADMIN_BIND_IP` and `UI_URL`. More: [docs/install.md](docs/install.md), [docs/production-checklist.md](docs/production-checklist.md).

**Versions.** Every release is a Git tag `vX.Y.Z` with images `ghcr.io/b0balyson/dropsite-control` and `ghcr.io/b0balyson/dropsite-caddy`. `DROPSITE_VERSION=latest` in `.env` (the default) follows new releases; set `X.Y.Z` to stay on one. Without a release, or to run your own changes, build from the checkout: `docker compose up -d --build --wait` ([building from source](docs/install.md#building-from-source), [docs/releasing.md](docs/releasing.md)).

**HTTPS certificates.** By default (`TLS_MODE=ondemand`) every address gets its own Let's Encrypt certificate on first visit. Let's Encrypt issues at most **50 new certificates per registered domain per 7 days**; a site with a random address and a readable one uses two, so plan for about **25 new sites a week**. A site's draft preview address costs one more, once. Renewals do not count. Beyond that, switch to **wildcard mode** (`TLS_MODE=wildcard`, `compose.wildcard.yml`): one certificate for `*.<BASE_DOMAIN>` through DNS-01, so new sites cost nothing; it needs DNS at Cloudflare and an API token with `Zone.DNS:Edit`, and gives every version its own preview address. Step by step: [docs/tls.md](docs/tls.md). All issued certificates appear in public Certificate Transparency logs, so site addresses are not secret.

### Connecting AI clients

| Client | How |
| --- | --- |
| ChatGPT | Settings → Apps → Advanced settings → Developer mode, then create an app with `https://api.<BASE_DOMAIN>/mcp` and OAuth. Custom apps are not available on every plan; check [help.openai.com](https://help.openai.com/). |
| claude.ai, Claude Desktop | Settings → Connectors → Add custom connector, paste the address, press Connect. |
| Claude Code | `claude mcp add --transport http dropsite https://api.<BASE_DOMAIN>/mcp`, then `/mcp` to sign in. |
| Claude Code, Codex, Cursor, Claude Desktop on your computer | **Connect** on the Connect AI page shows a one-line command that installs the CLI, signs in and runs `dropsite setup` (publish skill, or a local MCP server for Claude Desktop). |

A connect code is valid for 10 minutes and works once. Through a chat even an administrator manages only their own sites; installation settings stay in the web interface, and the chat never creates tokens or connect codes. Connected apps are listed and disconnected on the Connect AI page. Tools, rules and limits: [docs/ai-clients.md](docs/ai-clients.md), [docs/mcp-tools.md](docs/mcp-tools.md).

### CLI

The installers download a binary from GitHub Releases and check its SHA-256 (`DROPSITE_VERSION=X.Y.Z` picks a release):

```bash
curl -fsSL https://raw.githubusercontent.com/b0balYson/dropsite/main/scripts/install.sh | DROPSITE_SERVER=https://api.example.com DROPSITE_CODE=ABCD-EFGH-JKMNP sh
```

```powershell
$env:DROPSITE_SERVER='https://api.example.com'; $env:DROPSITE_CODE='ABCD-EFGH-JKMNP'; irm https://raw.githubusercontent.com/b0balYson/dropsite/main/scripts/install.ps1 | iex
```

Commands: `login`, `setup` (`--remove` undoes it), `deploy` (`--draft`, `--expires 7d`), `sites`, `deploys`, `rollback`, `draft publish|discard`, `clone`, `domains`, `workspaces`, `delete`, `mcp`, `version`. The first `dropsite deploy ./dist` creates a site and remembers it in `.dropsite.json`; later runs update it. A folder with secret-looking files (`.env*`, keys) is refused unless you pass `--skip-sensitive` or `--include-sensitive`. The CLI prints a hint when it and the server are a minor version or more apart. Without a release, [build it from source](docs/install.md#cli-from-source).

### HTTP API

`https://api.<BASE_DOMAIN>/api/v1` with a bearer token created in the web interface; the OpenAPI document is at `/api/v1/openapi.json`. Contract: [docs/api-contract.md](docs/api-contract.md).

A site archive needs `index.html` at the root (a single top folder such as `dist/` is unwrapped); by default up to 50 MB unpacked, 20 MB per file, 2000 files. `/about` serves `about.html`, `404.html` is the not-found page and `200.html` turns on single-page-app mode. `/_form/` and `/_dropsite/` belong to the platform.

### Configuration

Settings live in `deploy/.env` ([.env.example](deploy/.env.example)).

| Variable | Default | Purpose |
| --- | --- | --- |
| `BASE_DOMAIN` | required | Base domain for sites |
| `ACME_EMAIL` | required | E-mail for the certificate account |
| `ADMIN_EMAIL`, `ADMIN_PASSWORD` | required | First administrator; the password is read only on the first start |
| `DROPSITE_VERSION` | `latest` | Release images to run, or `X.Y.Z` |
| `ADMIN_BIND_IP`, `UI_URL` | `127.0.0.1`, `http://<ADMIN_BIND_IP>:8082` | Where the web interface listens, and the address in invite links |
| `API_HOST` | `api.<BASE_DOMAIN>` | Host of the API and MCP endpoint |
| `TLS_MODE`, `CF_API_TOKEN` | `ondemand` | Certificate mode; the token only for wildcard |
| `MAX_SITE_BYTES`, `MAX_FILE_BYTES`, `MAX_FILES`, `MAX_SITES_PER_USER`, `KEEP_RELEASES` | 50 MB, 20 MB, 2000, 10, 5 | Limits; administrators override them in Settings and per user |
| `MAX_DOMAINS_PER_SITE` | `5` | Own domains per site |
| `SERVER_IPS` | addresses of `BASE_DOMAIN` | Public IPs shown as the DNS target |
| `PUBLISH_OPTIMIZE` | `on` | Publish-time optimization |
| `COMPOSE_PROFILES`, `RENDERER_URL` | off | Optional screenshot renderer ([docs/screenshots.md](docs/screenshots.md)) |

Further variables: `RESERVED_SLUGS` (extra forbidden short addresses), `LOG_LEVEL`, `STATS_INTERVAL` and `STATS_LOG_FILE` (`off` disables statistics), `TELEGRAM_API_URL`, `FORM_WEBHOOK_ALLOW_CIDRS` (test stacks only). The Telegram bot, mail server, Google service account and limits are set by an administrator in **Settings** of the web interface.

### Operations

| Task | Installed with the installer | By hand, from `dropsite/deploy` |
| --- | --- | --- |
| Update | `ssh <name> dropsite-update` | back up, check out the new tag, `docker compose pull && docker compose up -d --wait` |
| Undo the last update | `ssh <name> dropsite-rollback` | previous tag plus the backup taken before the update |
| Back up | daily automatically, or `ssh <name> dropsite-backup` | `docker compose exec -T control /control backup - > backup.tar.gz` |
| Reset the admin password | `ssh <name> dropsite-admin-password` | `docker compose exec -T control /control reset-password [email]` |
| Logs | `docker compose logs -f` | same |

Migrations run automatically; downgrades are not supported, so roll back with the backup. The form secrets key `state/secrets.key` is not part of a backup: keep a copy. Never run `docker compose down -v`, it deletes the data. Details: [docs/backup.md](docs/backup.md), [docs/releasing.md](docs/releasing.md#on-a-server).

### Security notes

- The web interface is on a separate listener that is not reachable from the internet; passwords are typed only there.
- Chat clients connect with OAuth 2.1 (PKCE, short-lived tokens, refresh rotation) confirmed by a one-time connect code. The API accepts only bearer tokens, stored as hashes.
- Another user's site always answers "not found". Archives are checked before anything is written, and publications with secret keys in their files are refused.
- Caddy reads the sites read-only and has no access to the database. Both containers run as non-root with read-only root filesystems.
- Use a dedicated base domain (see Requirements).
- The admin interface refuses to start on a public address unless you allow it explicitly; the first administrator password is not kept on the server after installation.
- Threat model, CDN setup and abuse handling: [docs/security.md](docs/security.md).

### Development

```bash
make tools        # Go and golangci-lint into .tools/ (nothing system-wide)
make test lint
scripts/dev-control.sh   # run control natively, UI on http://127.0.0.1:18082
make dev-up       # full stack in Docker with a local CA: https://<site>.dropsite.localhost:18443
make e2e
make release-check
```

Changes are listed in [CHANGELOG.md](CHANGELOG.md).

### License

[Apache-2.0](LICENSE)
