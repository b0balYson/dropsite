# Connecting AI assistants

dropsite is meant to be used from a chat: the user connects it once and then asks in plain words ("make a coffee shop site and publish it", "show the latest requests", "close the site with a password"). Everything starts on the **Connect AI** page of the web interface.

| Assistant | How it works with sites | How it is connected |
| --- | --- | --- |
| ChatGPT, claude.ai, Claude Desktop, Claude Code (main path) | remote MCP `https://api.<BASE_DOMAIN>/mcp`: every chat tool (publish, change files, forms, statistics, access, domains, rollback) | connector + OAuth with a connect code |
| Claude Code, Codex, Cursor on a computer (optional) | `dropsite-publish` skill → `dropsite deploy <folder>` | one command (`dropsite setup`) |
| Claude Desktop on a computer (optional) | local MCP server `dropsite mcp` (tool `deploy_folder`) | one command (`dropsite setup`) |

## Chats: ChatGPT, claude.ai, Claude Desktop, Claude Code

All of them connect to the remote MCP endpoint `https://api.<BASE_DOMAIN>/mcp` (Streamable HTTP, OAuth):

- **ChatGPT:** Settings → Apps → Advanced settings → developer mode; create an app with the server address and OAuth authentication. Custom MCP apps are not available on every ChatGPT plan and workspace admins may have to allow them; the official sources differ on plans, so check the current terms at [help.openai.com](https://help.openai.com/).
- **claude.ai and Claude Desktop:** Settings → Connectors → Add custom connector, paste the address, press Add and Connect. The connector appears in Claude Desktop too. On a Team or Enterprise plan an owner adds it for the organisation first.
- **Claude Code:** `claude mcp add --transport http dropsite https://api.<BASE_DOMAIN>/mcp`, then `/mcp` in Claude Code to sign in.

When the dropsite page opens, the user presses **Connect** on the Connect AI page, enters the one-time code and presses **Allow**. The page shows the application name, the domain it returns to and the permissions; it never asks for a password (passwords are typed only on the private interface).

Example requests (the Connect AI page shows them in the interface language):

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

What people do themselves, and the assistant says so: the first connection; DNS records of an own domain at the domain provider (the assistant gives the exact values) and the administrator's approval of the domain; confirming a delivery channel (Start in Telegram, the code from the e-mail); installation settings (Telegram bot, mail server, users, limits) in the web interface.

### How the assistant learns the rules

Chats differ in which MCP features reach the model (checked November 2026):

| Feature | ChatGPT | claude.ai, Claude Desktop | Claude Code |
| --- | --- | --- | --- |
| server `instructions` | used; the first 512 characters matter most ([Apps SDK](https://developers.openai.com/apps-sdk/build/mcp-server)) | not documented; reports of instructions being dropped for remote servers | used, up to 4096 characters ([docs](https://code.claude.com/docs/en/mcp)) |
| tool descriptions and annotations | used; annotations drive write confirmations | used; read-only tools run without a prompt, destructive ones always ask ([review criteria](https://claude.com/docs/connectors/building/review-criteria.md)) | used |
| prompts, resources | not used for the model | user-invoked only | user-invoked only |

So dropsite delivers its rules three ways, from one source:

1. **Server instructions** sent on `initialize` by both servers: the essentials in the first paragraph (plain language, give the link, confirm before deleting, call `get_guide`), then short rules for every task.
2. **`get_guide`**, a read-only tool that works even before the user has connected. `topic` is one of `overview`, `create`, `update`, `large_sites`, `assets`, `forms`, `stats`, `addresses`, `protection`, `rollback`, `account`, `errors`, or `all` (default).
3. **Tool descriptions** say when to call each tool and what to tell the user, and point to `get_guide`.

The guide lives in `skills/guide/*.md` and is embedded into both binaries. Its sections on self-contained sites and on forms are also part of the `dropsite-publish` skill and the Codex snippet: `skills/dropsite-publish/SKILL.md` and `skills/codex/AGENTS.md` are generated from `skills/parts/*.md` and those sections. After editing any of them run `go test ./skills -update`; the tests fail while the files are out of date.

## Connect codes

**Connect** on the page issues a one-time code such as `ABCD-EFGH-JKMNP`: 13 symbols (65 bits) from an alphabet without look-alike characters, case, spaces and dashes are ignored. A code is valid for 10 minutes, works once and is stored only as a hash. It can be exchanged either for an API token (CLI) or for an OAuth grant (chat clients); on its own it opens nothing. Ten wrong codes from one IP within 15 minutes block further attempts until the window passes. Issuing and using codes is recorded in the audit log, without the code.

## One command on a computer

The page shows a command for macOS/Linux and one for Windows with the server and the code filled in:

```bash
curl -fsSL https://raw.githubusercontent.com/b0balYson/dropsite/main/scripts/install.sh | DROPSITE_SERVER=https://api.example.com DROPSITE_CODE=ABCD-EFGH-JKMNP sh
```

```powershell
$env:DROPSITE_SERVER='https://api.example.com'; $env:DROPSITE_CODE='ABCD-EFGH-JKMNP'; irm https://raw.githubusercontent.com/b0balYson/dropsite/main/scripts/install.ps1 | iex
```

It installs the CLI (checksum-verified, no admin rights), runs `dropsite login --server …` with the code in the `DROPSITE_CODE` environment variable, so it never appears in the process list (a new token named after the computer is saved to the CLI config with owner-only permissions) and then `dropsite setup`. The code in the shell history is useless once spent.

### `dropsite setup`

An assistant counts as installed when its settings directory exists. Setup writes only its own files and keys, and running it again changes nothing.

| Assistant | Detected by | What setup writes |
| --- | --- | --- |
| Claude Code | `$CLAUDE_CONFIG_DIR` or `~/.claude` (`%USERPROFILE%\.claude`) | `<that dir>/skills/dropsite-publish/SKILL.md` |
| Codex | `$CODEX_HOME` or `~/.codex` | `~/.agents/skills/dropsite-publish/SKILL.md` (the user skills directory of current Codex; the global `AGENTS.md` is not touched) |
| Cursor | `~/.cursor` | `~/.cursor/skills/dropsite-publish/SKILL.md` |
| Claude Desktop | `~/Library/Application Support/Claude` (macOS), `%APPDATA%\Claude` (Windows); not available on Linux | `mcpServers.dropsite = {"command": "<absolute path of dropsite>", "args": ["mcp"]}` in `claude_desktop_config.json` |

The Claude Desktop config is rewritten atomically with its permissions and every other key kept; a file with invalid JSON is left alone and reported. `--remove` deletes the skill files and the `mcpServers.dropsite` entry only; `--json` prints a machine-readable result. Restart the assistants that setup lists (Claude Code and Codex pick up skills in a new session).

### Local MCP server

`dropsite mcp` speaks MCP over stdio and calls the API with the saved login. It sends the same instructions and offers `get_guide` (also without a login), plus `list_sites`, `create_site`, `get_site`, `set_slug`, `list_deploys`, `rollback`, `delete_site` and `deploy_folder` (an absolute folder path; packed exactly like `dropsite deploy`, including `.dropsite.json`). Without a login every tool answers with instructions to press **Connect**.

Because the assistant can be steered by content it reads, `deploy_folder` is guarded: the home folder, folders directly inside it and file system roots are refused (symbolic links are resolved), and a folder with secret-looking files — `.env*`, `.npmrc`, `.netrc`, `.git-credentials`, `.pypirc`, `.ssh/`, `.aws/`, `.gnupg/`, `*.pem`, `*.key`, `id_rsa*`, `id_dsa*`, `id_ecdsa*`, `id_ed25519*` — is refused with the list of those files. With `skip_sensitive_files: true` the folder is published without them; the tool has no way to publish them. Other dotfiles such as `.well-known/` are published as usual. The tool description tells the assistant to pass only the build output folder.

`dropsite deploy` applies the same secret-file check (not the folder check): it stops and lists the files unless `--skip-sensitive` (leave them out) or `--include-sensitive` (publish them, when they really are public) is given.

## Remote MCP: tools and rights

The remote server offers every chat tool. A whole small site is published in one call with `deploy_files` (instead of the local `deploy_folder`): `files` is an array of `{path, content, encoding}` (`utf8` or `base64`), at most 16 MB decoded in total; omit `site` to create a new site. Larger sites go up in an upload session where the server offers one; otherwise the error recommends the web interface, the CLI or Claude Desktop. Releases published this way have the source `mcp`.

Form tools (remote server only; details in [forms.md](forms.md#from-a-chat-assistant)): `get_form_settings`, `set_form_settings`, `list_submissions`, `export_submissions`, `list_channels`, `add_channel`, `verify_channel`, `set_channel_enabled`, `delete_channel`, `rotate_channel_secret`.

The remote server also covers the account and site settings of the web interface, so a user can manage everything by asking in plain words:

| Tool | What it does |
| --- | --- |
| `get_account` | email, role, sites used of the limit, size limits, web interface address; for administrators the site on the root domain |
| `update_site` | rename a site and/or change its slug (`set_slug` still works) |
| `set_site_access` | hide from search engines, set/change/remove the visitor password, set (`expires_on`, last day, UTC) or remove the expiry; with only `site` it reports the current state |
| `list_domains`, `add_domain`, `remove_domain` | own domains with their status (pending, active, rejected with the reason) and the DNS records the user creates at the domain provider; `remove_domain` takes the domain again in `confirm` |
| `get_site_stats` | views and visitors per day, totals, busiest day, top pages and sources (default 7 days, up to 400) |
| `get_counters`, `set_counters` | Yandex Metrica and Google Analytics 4 IDs; only the passed ones change, a published site gets a new release |
| `list_connections`, `revoke_connection` | connected apps and active API tokens without secrets, the current one marked; revoking takes the name (or ID) again in `confirm` and works on the next call, including for the assistant's own connection |
| `set_root_site`, `clear_root_site` | administrators only: put any site on the root domain or take it off, with the site's slug or ID in `confirm` |

### Changing a site from a new chat

The assistant does not need the original files: it reads the published ones.

| Tool | What it does |
| --- | --- |
| `list_site_files` | files of the served release (or `deploy_id`): path, size, sha256, type; up to 1000 per call, `prefix` narrows the list. Returns `deploy_id`. |
| `read_site_files` | up to 2000 paths or patterns (`css/*.css`): text as is, binaries up to 4 MB as base64, larger files only described; at most 8 MB of content per call. The analytics block dropsite inserts into HTML is not shown. |
| `update_site_files` | `base_deploy_id` (required) plus `files` to write, `delete` (files or folders) and `fetch` (`{url, path}` downloaded by the server). Builds a new release from the base with the changes; everything else is kept, the previous release stays available for rollback. |
| `begin_upload`, `upload_files`, `commit_upload` | sites larger than 16 MB: a session in `replace` mode (new content, a new site when `site` is omitted) or `patch` mode (on top of `base_deploy_id`), up to 16 MB per `upload_files` call. |

If the site changed after the assistant read it (another publication, a rollback, a parallel edit), `update_site_files` and a `patch` commit fail with `deploy_conflict` and the current release ID; the assistant reads the files again and repeats the change. Of two simultaneous edits of the same release exactly one is published.

Every new release passes the same checks as an uploaded archive (paths, `index.html`, the owner's `MAX_SITE_BYTES`, `MAX_FILE_BYTES`, `MAX_FILES`, 30 publications per hour). Paths that look like secrets (`.env*`, keys, credentials) and service files (`.git/`, `node_modules/`) cannot be written from chat.

Upload sessions: at most 50 open per user, closed after 24 idle hours (every `upload_files` call extends a session) and on a restart of `control`; their files live in the upload directory of the state volume and are removed by housekeeping.

### Images and fonts downloaded by the server

`fetch` entries let the assistant put images, fonts, CSS and JS inside the site instead of linking to other servers. The server downloads them only over `https` on port 443, without a proxy, and only from public addresses: the address actually connected to is checked after every DNS lookup and on every redirect (at most 3), so private, loopback, link-local, cloud metadata and other reserved networks are never reached. Allowed: images (PNG, JPEG, GIF, WebP, AVIF, SVG, ICO), fonts (WOFF, WOFF2, TTF, OTF, EOT), `.css`, `.js`, `.mjs` — by the response type and the target extension. Limits: 500 URLs and 256 MB per call, 50 MB per file (and at most the owner's `MAX_FILE_BYTES`), 10 minutes per call, 100000 URLs per user per hour. JPEG and PNG wider than 2000 px are scaled down to 2000 px when that makes the file smaller. One failed URL cancels the whole call and the error names it.

`list_sites` and `get_site` also return `hidden_from_search`, `password_protected`, `expires_at`, `root_domain` and `own_domains`. Answers carry ready links and a short `summary` the assistant can repeat. These tools exist only on the remote server; the local `dropsite mcp` keeps the tools listed above.

No tool issues credentials: API tokens and connect codes are created only in the web interface.

Whatever token reaches `/mcp` (an OAuth token or an API token), the assistant works only with the user's own sites: an administrator's assistant gets no administrator powers and cannot see, publish to, roll back or delete other users' sites. Administration stays in the web interface, the API and the CLI; the only installation-level action available to an administrator's chat is the root domain, with explicit confirmation.

Connected applications are listed on the **Connect AI** page; **Disconnect** revokes every token of that application at once.

## OAuth server reference

The endpoint follows the MCP authorization specification (2025-11-25) and the requirements of ChatGPT apps.

| Path | Purpose |
| --- | --- |
| `GET /.well-known/oauth-protected-resource` and `/.well-known/oauth-protected-resource/mcp` | RFC 9728 metadata: `resource = https://<API_HOST>/mcp`, `authorization_servers = [https://<API_HOST>]`, scope `sites` |
| `GET /.well-known/oauth-authorization-server` | RFC 8414 metadata: `code_challenge_methods_supported: ["S256"]`, `token_endpoint_auth_methods_supported: ["none"]`, `client_id_metadata_document_supported: true`, `authorization_response_iss_parameter_supported: true` |
| `GET/POST /oauth/authorize` | consent page with the connect code |
| `POST /oauth/token` | `authorization_code` (PKCE S256 required) and `refresh_token` |
| `POST /oauth/register` | dynamic client registration (RFC 7591), public clients only |
| `POST /oauth/revoke` | RFC 7009 revocation, revokes the whole grant |

- `401` from `/mcp` carries `WWW-Authenticate: Bearer resource_metadata="…/.well-known/oauth-protected-resource/mcp", scope="sites"`.
- Clients are identified by a client ID metadata document (CIMD, preferred by ChatGPT) or registered dynamically. Metadata documents are fetched only over https on port 443, only from public addresses (checked after DNS resolution and pinned for the connection; IPv6 only within 2000::/3), without redirects, within 5 seconds and 64 KB, and cached for up to an hour; a failed fetch is cached for a minute, and one IP (or IPv6 /64) can trigger at most 10 fetches per 10 minutes (then the consent page answers 429). Dynamic registration accepts https or loopback redirect URIs whose host is a DNS name or an IP literal, and is limited to 10 per hour per IP (IPv6 addresses count per /64 network, as do all per-IP limits) and 1000 clients in total. A registration that never led to a connection is removed after 24 hours, one whose connections were all revoked after 30 days; at the 1000-client cap the oldest registration without an active connection (and older than 15 minutes) is evicted to make room. Client names from registrations and metadata documents are stripped of control and bidirectional-override characters and cut to 100 characters.
- Failed connect codes count against a shared per-IP limit (10 per 15 minutes); the audit log gets at most one `connect.code_failed` row per IP (or IPv6 /64) per 15 minutes.
- Redirect URIs are compared exactly; an unknown client or redirect URI shows an error page instead of redirecting. Authorization responses include `iss`.
- `resource` must be the MCP resource (the API origin is accepted as a synonym); tokens carry it as their audience and `/mcp` rejects any other.
- Access tokens live 1 hour, refresh tokens 30 days. Each refresh returns a new pair; presenting a used refresh token (or a used authorization code) revokes the whole connection; redeeming a code and issuing its tokens is one transaction, so even a replay racing the first exchange revokes them. Only hashes are stored.
- OAuth tokens work only on `/mcp`, never on `/api/v1`.
- Blocking a user stops their OAuth tokens immediately.

Caddy proxies the whole `API_HOST` to `control`, so no proxy changes are needed. The endpoint is stateless (every POST is authorised on its own; no server-sent event streams).

## OAuth discovery troubleshooting

If ChatGPT reports that it could not determine OAuth settings, check the public API host before entering settings manually:

```bash
dig +short api.example.com
curl -i https://api.example.com/.well-known/oauth-protected-resource/mcp
curl -i https://api.example.com/.well-known/oauth-authorization-server
curl -i https://api.example.com/mcp
```

Replace `api.example.com` with your API host. DNS must return the server IP. Both metadata URLs must return HTTP 200 and JSON, with HTTPS validated without `-k`. The unauthenticated `/mcp` request must return HTTP 401 and a `WWW-Authenticate` header containing `resource_metadata`. The metadata `issuer` and `authorization_servers` must refer to your public API host.

`NXDOMAIN` means a DNS record is missing. Add an `A` record for the API host pointing at the server (and an `AAAA` record only if working IPv6 is available). Check the base domain and wildcard records too. Caddy cannot obtain a public certificate before these names resolve. Inspect `docker compose logs --tail=100 caddy` from `dropsite/deploy` for DNS and certificate errors. Manual OAuth settings do not fix DNS or HTTPS errors. After fixing DNS and HTTPS, retry creating the ChatGPT connection.
