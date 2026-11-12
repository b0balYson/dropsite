# Connecting AI assistants

dropsite reaches AI assistants in three ways. All of them start on the **Connect AI** page of the web interface.

| Assistant | How it publishes | How it is connected |
| --- | --- | --- |
| Claude Code, Codex, Cursor | `dropsite-publish` skill → `dropsite deploy <folder>` | one command (`dropsite setup`) |
| Claude Desktop | local MCP server `dropsite mcp` (tool `deploy_folder`) | one command (`dropsite setup`) |
| ChatGPT, claude.ai | remote MCP `https://api.<BASE_DOMAIN>/mcp` (tool `deploy_files`, up to 1 MB) | custom connector + OAuth with a connect code |

## Connect codes

**Connect** on the page issues a one-time code such as `ABCD-EFGH-JKMNP`: 13 symbols (65 bits) from an alphabet without look-alike characters, case, spaces and dashes are ignored. A code is valid for 10 minutes, works once and is stored only as a hash. It can be exchanged either for an API token (CLI) or for an OAuth grant (chat clients); on its own it opens nothing. Ten wrong codes from one IP within 15 minutes block further attempts until the window passes. Issuing and using codes is recorded in the audit log, without the code.

## One command on a computer

The page shows a command for macOS/Linux and one for Windows with the server and the code filled in:

```bash
curl -fsSL https://raw.githubusercontent.com/b0balYson/dropsite/main/scripts/install.sh | sh -s -- --server https://api.example.com --code ABCD-EFGH-JKMNP
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

`dropsite mcp` speaks MCP over stdio and calls the API with the saved login: `list_sites`, `create_site`, `get_site`, `set_slug`, `list_deploys`, `rollback`, `delete_site` and `deploy_folder` (an absolute folder path; packed exactly like `dropsite deploy`, including `.dropsite.json`). Without a login every tool answers with instructions to press **Connect**.

Because the assistant can be steered by content it reads, `deploy_folder` is guarded: the home folder, folders directly inside it and file system roots are refused (symbolic links are resolved), and a folder with secret-looking files — `.env*`, `.npmrc`, `.netrc`, `.git-credentials`, `.pypirc`, `.ssh/`, `.aws/`, `.gnupg/`, `*.pem`, `*.key`, `id_rsa*`, `id_dsa*`, `id_ecdsa*`, `id_ed25519*` — is refused with the list of those files. With `skip_sensitive_files: true` the folder is published without them; the tool has no way to publish them. Other dotfiles such as `.well-known/` are published as usual. The tool description tells the assistant to pass only the build output folder.

`dropsite deploy` applies the same secret-file check (not the folder check): it stops and lists the files unless `--skip-sensitive` (leave them out) or `--include-sensitive` (publish them, when they really are public) is given.

## ChatGPT and claude.ai

Both connect to the remote MCP endpoint `https://api.<BASE_DOMAIN>/mcp`:

- **ChatGPT:** Settings → Apps (Connectors) → Advanced → developer mode; create an app with the server address and OAuth authentication. Custom MCP connectors are not available on every ChatGPT plan and workspace admins may have to allow them; check the current terms at [help.openai.com](https://help.openai.com/).
- **claude.ai:** Settings → Connectors → Add custom connector, paste the address, press Connect.

When the dropsite page opens, the user enters a connect code from the **Connect AI** page and presses **Allow**. The page shows the application name, the domain it returns to and the permissions; it never asks for a password (passwords are typed only on the private interface).

Tools: the same as the local server, with `deploy_files` instead of `deploy_folder`: `files` is an array of `{path, content, encoding}` (`utf8` or `base64`), at most 1 MB decoded in total; omit `site` to create a new site. Larger sites get an error that recommends the CLI or Claude Desktop. Releases published this way have the source `mcp`.

Whatever token reaches `/mcp` (an OAuth token or an API token), the assistant works only with the user's own sites: an administrator's assistant gets no administrator powers and cannot see, publish to, roll back or delete other users' sites. Administration stays in the web interface, the API and the CLI.

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
