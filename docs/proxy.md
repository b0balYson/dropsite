# API keys for pages (secret proxy)

A static page cannot hide a key: everything in its HTML and JavaScript reaches the visitor. A **proxy endpoint** lets a page of a site call a third-party API (OpenAI, a CRM, a Telegram bot, maps) while the key stays on the Dropsite server. No code of the owner runs on the server: control only forwards the request and adds the key.

```text
page ──fetch──▶ https://<site>/_dropsite/proxy/<name>/<path>
                 │ Caddy: forward_auth for protected sites
                 ▼
               control ──https + key──▶ <upstream_url>/<path>
```

## Endpoint

| Field | Meaning |
| --- | --- |
| `name` | `a-z`, `0-9`, `-`, up to 40 characters, unique within the site |
| `upstream_url` | `https://host[/base]`, no user info, standard port, no key-like query parameters (`key`, `token`, `secret`, `password`, …) |
| `allowed_hosts` | exact host names the request and its redirects may reach (up to 10); the upstream host is always first |
| `methods` | subset of `GET HEAD POST PUT PATCH DELETE`, default `GET POST` |
| `paths` | up to 10 prefixes such as `/chat/completions`, matched on segment boundaries; empty allows any path |
| `auth` | `bearer` (`Authorization: Bearer <key>`), `header` (header `auth_name`), `query` (parameter `auth_name`), `path` (`{secret}` in `upstream_url`, e.g. `https://api.telegram.org/bot{secret}`) |
| `enabled` | off answers 404 |

A site has at most 10 endpoints. Hosts must be public DNS names: IP literals, `localhost`, `*.local`, `*.internal`, single-label names, the base domain and its subdomains, the API and UI hosts are refused.

## The key

- The key is entered **only** on the **API keys** screen of the site in the web interface (`/sites/<id>/proxy`). The HTTP API and both MCP servers have no way to set it; `PUT .../proxy/endpoints/{name}` with a `secret` field answers `400 proxy_secret_ui_only` with the screen's address in `details.secret_url`. This keeps keys out of chat histories: the local MCP server talks to the same API, so a write-only API field would let an assistant pass a key through the chat.
- Responses never contain the key, only `secret_set` and `secret_updated_at`. The screen never shows it again.
- The key is sealed with `state/secrets.key` (AES-256-GCM, the same key as form channel secrets) and bound to the endpoint row, so a copied value does not open for another endpoint. A backup restored without `secrets.key` loses the keys: calls answer `proxy_secret_missing` until they are pasted again.
- Changing `upstream_url`, `allowed_hosts`, `auth` or `auth_name` erases the key (audit `proxy.endpoint.update` with `secret_cleared: true`), so nobody, including an assistant tricked by page content, can redirect a stored key to another server. Methods, paths and `enabled` keep it.

## Forwarding rules

- The path after the name is decoded segment by segment; `.`/`..` segments, encoded `/` or `\`, empty inner segments and control characters answer `403 proxy_path`.
- Visitor query parameters are appended to those of `upstream_url`; a visitor parameter with the name of a `query` key is dropped.
- Not forwarded: `Cookie`, `Authorization`, `Proxy-Authorization`, `Host`, `Origin`, `Referer`, hop-by-hop headers and those named in `Connection`, `Forwarded`, `X-Forwarded-*`, `X-Real-Ip`, `Via`, `Sec-*`, `Cf-*`, `Accept-Encoding`, method and URL override headers (`X-Http-Method-Override`, `X-Http-Method`, `X-Method-Override`, `X-Original-Method`, `X-Original-Url`, `X-Rewrite-Url`), and the header that carries the key.
- Returned from upstream: the status, the body and only `Content-Type`, `Content-Language`, `Content-Disposition`, `ETag`, `Last-Modified`, `Retry-After`. The proxy adds `Cache-Control: no-store`, `X-Content-Type-Options: nosniff`, `X-Robots-Tag: noindex` and `Content-Security-Policy: default-src 'none'; sandbox`, so HTML from an API cannot run on the site's origin.
- Connections use https with certificate checks and go only to public addresses; the address is checked on the socket being connected (DNS rebinding cannot reach internal networks) and the server's own addresses are refused. No environment proxy is used.
- Redirects are followed by control itself, at most 3, only to `https` hosts of `allowed_hosts`, with the key added again; anything else answers `502 proxy_upstream` and the `Location` never reaches the page.
- Limits: request body 1 MiB (also in Caddy; `413 proxy_too_large`), response 10 MiB (a declared larger length answers 502; a longer stream is cut by closing the connection), 60 seconds per call (`504 proxy_timeout`). Responses are streamed and flushed, so `text/event-stream` answers arrive as they are produced.
- `Origin` must match the site address (as for forms) and `Sec-Fetch-Site` must not be `cross-site` or `same-site`: `403 proxy_origin`. No CORS headers are sent. Requests without these headers (scripts, curl) are only limited by rate.
- Rate limits: 60 calls per minute per IP (IPv6 per /64) and site, 1000 per hour per site (`429` with `Retry-After`). Refusals by origin, method and path happen before and do not use the site's quota.
- A disabled or unknown endpoint, or one of a deleted site, answers 404; an endpoint without a key answers `503 proxy_secret_missing` without contacting the upstream.

Errors are JSON in the API format: `{"error": {"code": "proxy_path", "message": "..."}}`.

## Protected sites

Caddy sends `/_dropsite/proxy/*` to control only after `forward_auth`, so a password-protected site serves its proxy only to visitors who signed in. The rest of `/_dropsite/*` (sign-in page, Altcha script) is still proxied before the access check.

## Usage and audit

Each endpoint counts forwarded calls, failed ones (upstream 5xx or unreachable), refused ones (origin, method, path, rate, missing key, size) and the time and status of the last call. Refused calls are counted in memory and written at most once every 10 seconds (and before the owner views the endpoints), so a flood of refused calls does not turn into database writes; a restart may lose the refusals of the last 10 seconds. No IP addresses, paths or bodies are stored. The audit log records `proxy.endpoint.create`, `proxy.endpoint.update`, `proxy.endpoint.delete`, `proxy.secret.set` and `proxy.secret.clear` with the site, the endpoint name and the upstream host, never the key. Visitor calls are not audited.

## Risks the owner accepts

- Anyone who opens the site can make its page call the API within the allowed methods, paths and rate limits. Use a key with minimal rights and a spending limit at the provider, and keep paths narrow.
- The body of the API answer is passed through as is: an API that echoes the key in its body would reveal it.

## API and MCP

- `GET /api/v1/sites/{ref}/proxy/endpoints`, `PUT /api/v1/sites/{ref}/proxy/endpoints/{name}` (201 on creation, 200 on replacement), `DELETE /api/v1/sites/{ref}/proxy/endpoints/{name}`; see `openapi.json`.
- MCP tools on both servers: `list_proxy_endpoints`, `set_proxy_endpoint`, `delete_proxy_endpoint` (with `confirm`). `set_proxy_endpoint` returns `secret_url` and a `next_step` asking the user to paste the key in the web interface. The guide topic `proxy` explains when to use it.
