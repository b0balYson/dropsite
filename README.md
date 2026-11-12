# dropsite

[Русская версия](README.ru.md)

Self-hosted publishing of static sites. A marketer asks ChatGPT or Claude "make a coffee shop site and publish it", or drops a zip into the web interface, and gets an HTTPS link on a subdomain of your domain in seconds; later changes, visit statistics, form requests and passwords are plain requests in the same chat. No VPS per site, no git, no DevOps.

- One server. Build from source and start: `docker compose up -d --build --wait`.
- Archive in, HTTPS link out. Every site gets a random address such as `k3f9x2m7qa.example.site` and an optional readable one such as `promo-may.example.site`.
- Every publish is an immutable release: switch and roll back instantly, with zero failed requests during the switch.
- Each user sees and changes only their own sites. Admins manage users, all sites, limits and the audit log.
- Manage sites from a chat (ChatGPT, claude.ai, Claude Desktop, Claude Code) connected once, or from the web interface, the `dropsite` CLI and the HTTP API.
- Sites keep working while the control service is stopped or being upgraded.

dropsite serves finished static files only (HTML, CSS, JS, images). It does not build projects or run server code.

## How it works

Two containers. **Caddy** serves files by host name and obtains TLS certificates on demand; its configuration is static and never reloads. **control** (one Go binary) owns the database, accounts, the API and the web interface, and publishes releases by atomically switching symlinks on a shared volume that Caddy reads.

## Install from the repository

Requirements: a Linux server (amd64 or arm64), Git, Docker Engine 24+ and Compose 2.24.4+, ports 80 and 443 open, and a domain. For a server with 1 CPU and 1 GB RAM, provide 2 GB swap for building; see [build memory](docs/install.md#build-memory). Go is not needed on the host: Docker downloads the toolchain and builds the application inside a container. Internet access is needed for base images and dependencies. The first build can take several minutes.

### 1. Choose a dedicated base domain

Use a domain (or a subdomain such as `sites.example.com`) that hosts **nothing else**. Published sites run arbitrary JavaScript on its subdomains; never put company services, logins or cookies on the base domain or share cookies with it. For public installations with untrusted users, consider adding the domain to the [Public Suffix List](https://publicsuffix.org/) so browsers isolate sites from each other.

### 2. Point DNS at the server

| Record | Name | Value |
| --- | --- | --- |
| A (and AAAA if you have IPv6) | `example.site` | server IP |
| A (and AAAA) | `*.example.site` | server IP |

The `*` record also covers `api.example.site` unless a separate record at `api` overrides it. With Cloudflare, the zone must be **Active** and the registrar must use the nameservers assigned by Cloudflare.

Check `dig +short example.site` and `dig +short api.example.site`: both must return the server IP. `NXDOMAIN` means the DNS record does not exist. Until DNS works, Caddy cannot obtain a certificate and ChatGPT cannot discover OAuth settings. Use **DNS only** in Cloudflare during setup.

### 3. Configure and start

```bash
git clone https://github.com/b0balYson/dropsite.git
cd dropsite/deploy
cp .env.example .env
chmod 600 .env
# Fill in BASE_DOMAIN, ACME_EMAIL, ADMIN_EMAIL, ADMIN_PASSWORD
nano .env
docker compose config --quiet
docker compose up -d --build --wait
docker compose ps
```

Run `docker compose` commands from `dropsite/deploy`. Docker builds `control` from this repository. No prebuilt dropsite image is required. Keep the full checkout for future builds. In `docker compose ps`, `control` must be `healthy` and `caddy` must be `running`. If startup fails, run `docker compose logs --tail=100 control caddy`.

### 4. Open the web interface

The interface listens on `ADMIN_BIND_IP:8082`, by default `127.0.0.1:8082`, and is **never exposed to the internet**. Reach it through an SSH tunnel:

```bash
ssh -L 8082:127.0.0.1:8082 you@server
# then open http://127.0.0.1:8082 and sign in with ADMIN_EMAIL / ADMIN_PASSWORD
```

For a team, set `ADMIN_BIND_IP` to the server address in a private overlay network (Tailscale, WireGuard, NetBird) and set `UI_URL` to the address people open, for example `http://100.64.0.10:8082`. The interface uses plain HTTP and must stay on a private network.

Then: **Users → create user**, send them the invite link (valid 72 hours); they set a password and publish.

## Sites from a chat

The main way to use dropsite is a chat. Connect it once to ChatGPT, claude.ai, Claude Desktop or Claude Code, then ask in plain words; the assistant builds or changes the site, publishes it and answers with a working link. Nobody needs to know file formats, commands or server details.

1. **Connect once.** On the **Connect AI** page of the web interface copy the address `https://api.<BASE_DOMAIN>/mcp` and follow the short steps for your chat (ChatGPT: developer mode → create an app; claude.ai and Claude Desktop: Settings → Connectors → Add custom connector; Claude Code: `claude mcp add --transport http dropsite <address>`). When the dropsite page opens, press **Connect** on the Connect AI page and enter the one-time code; the password is never asked for.
2. **Ask.** For example:

| English | Русский |
| --- | --- |
| "Make a coffee shop site and publish it" | «Сделай сайт кофейни и опубликуй» |
| "Change the photos and prices on my site" | «Измени фотографии и цены на моём сайте» |
| "Show visits for the week" | «Покажи посещаемость за неделю» |
| "Show the latest requests" | «Покажи последние заявки» |
| "Send requests to Telegram" | «Присылай заявки в Telegram» |
| "Close the site with a password" | «Закрой сайт паролем» |
| "Bring back the previous version" | «Верни предыдущую версию» |
| Administrator: "Put this placeholder on the root domain" | Администратор: «Поставь эту заглушку на корневой домен» |

A change from a new chat works too: the assistant reads the current files of the site from the server, changes what was asked and publishes a new version (the previous one stays available for rollback). Sites it builds carry their own images and fonts (no CDNs or hotlinked photos), are sized for phones first and load fast. Before deleting or replacing anything it asks for confirmation.

What stays with people:

- connecting a chat the first time (the Connect AI page);
- DNS records of an own domain, which the user creates at their domain provider with the values the assistant gives, and the administrator's approval of that domain;
- confirming a delivery channel: pressing Start in Telegram or reading the code from the e-mail;
- installation settings (Telegram bot, mail server, users, limits), which an administrator sets in the web interface. Through a chat even an administrator manages only their own sites; the only installation-level action is the root domain.

A site can also start from one of five built-in templates, be copied into a new site, go live at a given time (a prepared version waits, then the site switches), and, when the optional renderer is enabled, be checked by the assistant with screenshots and a before/after comparison: [docs/screenshots.md](docs/screenshots.md).

Give the assistant a link or a picture of a site you like and it builds an original site in a similar style, never a copy; for a link the server returns a design brief (palette, fonts, spacing, layout) without the page's texts or images: [docs/references.md](docs/references.md).

How the assistant learns the rules (server instructions, the `get_guide` tool, tool descriptions) and what each chat supports: [docs/ai-clients.md](docs/ai-clients.md).

## Publishing

- **Chat (main path):** see [Sites from a chat](#sites-from-a-chat). Each call of the chat tools carries up to 16 MB; bigger sites go up in an upload session that ends in one release.
- **Web interface:** create a site, drag a zip or tar.gz onto it.
- **AI assistants on a computer (one command, optional):** on the **Connect AI** page press **Connect**. It shows a one-time code (10 minutes) inside a ready command for macOS/Linux and for Windows. The command installs the CLI, signs in with the code and runs `dropsite setup`, which connects every assistant it finds: the publish skill for **Claude Code**, **Codex** and **Cursor**, and a local MCP server for **Claude Desktop** with the same chat tools as the connector. Then say "publish this site".
- **Chat connector details:** the connector address is `https://api.<BASE_DOMAIN>/mcp`. When the dropsite page opens, enter a connect code from the **Connect AI** page; the password is never asked for. Custom MCP connectors in ChatGPT need developer mode and are not available on every plan; check the current terms at [help.openai.com](https://help.openai.com/). Connected apps are listed on the **Connect AI** page and can be disconnected there. Details: [docs/ai-clients.md](docs/ai-clients.md).
- **Managing the account from chat:** besides publishing, a connected chat can rename sites and change their addresses, protect a site with a password, hide it from search engines or set its last day, attach own domains (it explains the DNS records to create), show visit statistics, set analytics counters and list or disconnect connected apps; an administrator can also put a site on the root domain. Destructive actions ask for confirmation, and the chat never creates tokens or connect codes. Tool list: [docs/ai-clients.md](docs/ai-clients.md#chats-chatgpt-claudeai-claude-desktop-claude-code).
- **Changing a site from chat:** in any new chat say "change the prices on my coffee shop site": the assistant reads the published files, changes only what is needed and publishes a new version (the previous one stays available for rollback). Sites larger than 16 MB are sent in parts, and the server itself downloads images and fonts by link so they ship with the site. Details: [docs/ai-clients.md](docs/ai-clients.md#changing-a-site-from-a-new-chat).
- **CLI by hand:** `dropsite login --server https://api.<BASE_DOMAIN> --code <code>` (or `--token` with a key from the collapsed "Manual access keys" section), then `dropsite deploy ./dist`. The first run creates a site and remembers it in `.dropsite.json`; later runs update it. A folder with secret-looking files (`.env*`, keys, credentials) is refused with their list; `--skip-sensitive` leaves them out, `--include-sensitive` publishes them.
- **HTTP API and MCP:** `https://api.<BASE_DOMAIN>/api/v1` with a bearer token ([docs/api-contract.md](docs/api-contract.md), `/api/v1/openapi.json`); the MCP endpoint `https://api.<BASE_DOMAIN>/mcp` accepts the same API tokens or OAuth tokens.

The one command looks like this (the page fills in the server and the code):

```bash
curl -fsSL https://raw.githubusercontent.com/b0balYson/dropsite/main/scripts/install.sh | DROPSITE_SERVER=https://api.example.com DROPSITE_CODE=ABCD-EFGH-JKMNP sh
```

```powershell
$env:DROPSITE_SERVER='https://api.example.com'; $env:DROPSITE_CODE='ABCD-EFGH-JKMNP'; irm https://raw.githubusercontent.com/b0balYson/dropsite/main/scripts/install.ps1 | iex
```

Without `--server` and `--code` the installers only install the CLI. `dropsite setup --remove` takes the integrations out again.

The CLI installers download a binary from GitHub Releases. If no release is available, [build the CLI from source](docs/install.md#cli-from-source) on your computer. The server and web publishing do not need the CLI.

### Own domains

A site can also open on the owner's domain, such as `promo.example.com`: add it in the **Own domains** block of the site page, with `dropsite domains add <site> <domain>` or `POST /sites/{id}/hostnames`. Point it at the server (a CNAME to the site's random address, or A/AAAA records for a root domain). The domain gets a certificate and serves the site only after an administrator approves it on the **Domains** screen, so nobody can make the server request certificates for arbitrary names. Details: [docs/external-domains.md](docs/external-domains.md).

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
| Reserved | `/_form/` and `/_dropsite/` belong to the platform (`/_dropsite/login` is the password page of protected sites, `/_dropsite/data/` serves site data) |
| Caching | HTML `no-cache`; other files `public, max-age=3600` |

### Access: search engines, password, lifetime

The **Access** block of a site (also `PATCH /api/v1/sites/{ref}`) controls who sees it and for how long. Changes apply at once, without restarting anything.

- **Hide from search engines** (`noindex`): every response of the site, including errors, carries `X-Robots-Tag: noindex, nofollow`.
- **Password** (`protected`): visitors see a neutral sign-in page with the site title, enter the password and stay signed in on that address for 7 days. Changing the password or removing protection signs everybody out. The password is stored as an argon2id hash. Every sign-in also passes a self-hosted Altcha proof of work that the page solves on its own (about a second on a desktop) and that gets much harder after failed attempts; wrong passwords are limited to 10 per 15 minutes per IP, and a burst of failures on one site makes the check harder for everyone instead of locking the site. Details: [docs/site-access.md](docs/site-access.md). Open sites never contact `control`; protected ones are checked on every request, and while `control` is down they show a "temporarily unavailable" page instead of the content.
- **Lifetime** (`expires_at`, CLI `--expires 7d`): after the last day the site stops opening within a minute and is deleted 7 days later, like a deleted site. The interface warns 3 days ahead.

Details: [docs/site-access.md](docs/site-access.md).
## Forms and submissions

Any published site can receive form submissions without a third-party service: point a form at `/_form/<name>` on the same site.

```html
<form action="/_form/lead" method="post">
  <input name="name" required> <input name="phone" type="tel" required>
  <input name="_hp" tabindex="-1" autocomplete="off" aria-hidden="true" style="position:absolute;left:-10000px">
  <input type="hidden" name="_next" value="/thanks.html">
  <altcha-widget challenge="/_form/challenge"></altcha-widget>
  <button>Send</button>
</form>
<script async defer src="/_dropsite/altcha.js" type="module"></script>
```

Every submission is stored first and then delivered in the background, with retries, to the channels the site owner adds on the site screen: a Telegram chat (one bot per installation, its token is set by an administrator), an e-mail address (SMTP set by an administrator) or an HTTPS webhook signed with HMAC-SHA256. Spam protection: the `_hp` honeypot, an optional fill-time check, rate limits per IP and per site, 64 KB body limit and, when the owner turns it on, a self-hosted [Altcha](https://altcha.org) proof of work whose difficulty grows for repeated attempts from one IP. Submissions are kept 90 days by default, can be browsed on the site screen and exported to CSV. Secrets (bot token, SMTP password, webhook secrets) are stored encrypted with `state/secrets.key`. Details: [docs/forms.md](docs/forms.md).

### API keys for pages

A page can call a third-party API with a key (OpenAI, a CRM, a Telegram bot) through `/_dropsite/proxy/<name>/<path>` on its own address: the server adds the key and forwards the request, the key never reaches the page. The owner pastes the key on the **API keys** screen of the site; the API and the chat never accept or show it. Allowed hosts, methods and paths, size and rate limits and private-network blocking apply; protected sites keep the proxy behind their password. Details: [docs/proxy.md](docs/proxy.md).

From ChatGPT or claude.ai the owner manages the same in plain words ("show the latest submissions", "send submissions to Telegram", "export this week's submissions"): the MCP servers (the remote one and the local `dropsite mcp` of Claude Desktop) have tools for form settings, submissions with filters, a capped CSV export and delivery channels; confirmation codes and the Telegram link still go through the user.

## Site data

A site can keep small JSON data in collections (a menu with prices, a catalog, a schedule, reviews) that its pages load with `fetch('/_dropsite/data/<name>')`. The owner changes documents from the chat ("change the cappuccino price to 250"), the web interface (site screen, "Data") or the API, and the change is live at once: no new release, and rollbacks do not touch the data. A `public` collection is written only by the owner; a `moderated` one accepts visitor proposals with `POST /_dropsite/data/<name>` (JSON checked against the collection's JSON Schema, with the same protection as forms: Origin, rate limits, honeypot, Altcha by the site flag), and only documents the owner approved are shown. On a password-protected site the data is served only after sign-in. Limits: 20 collections per site, 5000 documents per collection, 16 KB per document. No server code runs: the pages render the data themselves and keep their HTML as fallback when it cannot be loaded.

## Visit statistics and analytics counters

The site screen shows page views per day for the last 30 days, totals, top pages and top referrers; administrators also see views over 7 days in "All sites". The numbers come from Caddy's access log, so sites need no scripts: robots and asset requests are not counted, and a visitor is recognised by IP address and browser within one UTC day without storing either. The same data is available at `GET /api/v1/sites/{ref}/stats?days=30`.

Site owners may enter a Yandex Metrica counter number and/or a GA4 Measurement ID (administrators also global ones for every site). dropsite then inserts the standard tracking code before `</head>` in every HTML page of each new release; changing a site's IDs creates a new release with the updated code. Only IDs are accepted, never HTML. Details: [docs/statistics.md](docs/statistics.md).

## TLS certificates and Let's Encrypt limits

By default every address gets its own certificate on first visit (`TLS_MODE=ondemand`). Caddy only requests a certificate for addresses that exist.

Let's Encrypt allows **50 new certificates per registered domain per 7 days** (as of August 2026, see [rate limits](https://letsencrypt.org/docs/rate-limits/)). A site with a slug uses two certificates (random address and slug), so plan for about **25 new sites per week**. Renewals do not count. Caddy falls back to ZeroSSL when Let's Encrypt refuses. If you grow beyond that, switch to wildcard mode.

Every issued certificate is published in public Certificate Transparency logs, so site addresses are not secret.

**Wildcard mode** (`TLS_MODE=wildcard`) uses one certificate for `*.<BASE_DOMAIN>` and the root, obtained through the DNS-01 challenge, so new sites cost no certificates. It needs DNS hosted at Cloudflare (other providers can be added) and an API token with `Zone.DNS:Edit`:

```sh
# .env: CF_API_TOKEN=<token>
docker compose -f docker-compose.yml -f compose.wildcard.yml up -d --build --wait
```

In this mode every release also gets its own preview address `https://p-<release>-<site>.<BASE_DOMAIN>/` (not indexed by search engines), shown in the release history. Step-by-step switch and rollback: [docs/tls.md](docs/tls.md).

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
| `TLS_MODE` | no | `ondemand` | `ondemand` or `wildcard`; wildcard is set by `compose.wildcard.yml` |
| `CF_API_TOKEN` | wildcard only | — | Cloudflare token with `Zone.DNS:Edit`, passed to Caddy only |
| `MAX_SITE_BYTES`, `MAX_FILE_BYTES`, `MAX_FILES`, `MAX_SITES_PER_USER`, `KEEP_RELEASES` | no | 50 MB, 20 MB, 2000, 10, 5 | Limits; admins can override them globally (Settings) and per user (Users → Limits): user → admin setting → environment → default |
| `RESERVED_SLUGS` | no | | Extra forbidden slugs, comma separated |
| `MAX_DOMAINS_PER_SITE` | no | `5` | Own domains per site, 1–100 |
| `SERVER_IPS` | no | addresses of `BASE_DOMAIN` | Public IPs of the server, comma separated: DNS target shown to owners and used by the domain DNS check |
| `LOG_LEVEL` | no | `info` | `debug`, `info`, `warn`, `error` |
| `STATS_INTERVAL` | no | `1m` | How often control reads the sites access log (1s to 1h) |
| `STATS_LOG_FILE` | no | `/logs/sites-access.log` | Sites access log inside control; `off` disables statistics |
| `TELEGRAM_API_URL` | no | `https://api.telegram.org` | Bot API address for form delivery (proxy or tests) |
| `PUBLISH_OPTIMIZE` | no | `on` | Publish-time optimization: WebP copies of images and fallback `robots.txt`, `sitemap.xml`, `favicon.ico`; site files are never changed (docs/publishing.md) |
| `FORM_WEBHOOK_ALLOW_CIDRS` | no | | Private networks form webhooks may reach — for every user (test stacks only; the control host itself is always refused) |

## Operations

- **Backup:** `docker compose exec -T control /control backup - > backup-$(date +%F).tar.gz`. See [docs/backup.md](docs/backup.md) for restore.
- **Upgrade:** from `dropsite/deploy`, back up, run `git pull --ff-only`, then `docker compose up -d --build --wait`. Your `.env` and data in Docker volumes are preserved. Migrations run automatically; downgrades are not supported. See [upgrade steps](docs/backup.md#upgrade).
- **Logs:** JSON on stdout of both containers: `docker compose logs -f`. Caddy also writes the sites access log to the `logs` volume for statistics (rotated at 50 MB, 5 old files kept).
- **Health:** `docker compose ps` shows the `control` health check.

More: [docs/install.md](docs/install.md), [docs/backup.md](docs/backup.md), [docs/tls.md](docs/tls.md), [docs/external-domains.md](docs/external-domains.md), [docs/production-checklist.md](docs/production-checklist.md), [docs/statistics.md](docs/statistics.md), [docs/forms.md](docs/forms.md).

## Security model

- The web interface is on a separate listener that is not reachable from the internet; its session cookie is bound to the interface host only.
- The API accepts only bearer tokens (never cookies). Tokens are shown once, stored as hashes, can expire and be revoked.
- Another user's site always answers "not found".
- Passwords are typed only on the private interface. Chat clients are authorised on the public API host with OAuth 2.1 (PKCE, short-lived access tokens, rotating refresh tokens with reuse detection); the user confirms with a one-time connect code issued in the interface, not with a password.
- Archives are validated before anything is written: no path traversal, links, devices or zip bombs.
- Caddy mounts the sites volume read-only and has no access to the database or tokens. Both containers run as non-root with read-only root filesystems.

## Development

```bash
make tools        # Go and golangci-lint into .tools/ (nothing system-wide)
make test lint
scripts/dev-control.sh   # run control natively, UI on http://127.0.0.1:18082
make dev-up       # full stack in Docker with a local CA: https://<site>.dropsite.localhost:18443
make e2e
```

## License

Apache-2.0
