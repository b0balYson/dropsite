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
| `class` | `ai`, `send`, `read` or `transparent` (below); a new endpoint without a class is `ai` |
| `template` | the declarative request of `ai` and `send` (below) |
| `limits` | `per_minute`, `daily_budget`, `session_quota`; 0 keeps the class default |

A site has at most 10 endpoints.

## Classes

| Class | What the page may do | Session | Default limits |
| --- | --- | --- | --- |
| `ai` | POST the declared fields; the server sends only the template | yes | 20/min per client, 50 per session, 1000/day |
| `send` | the same, with the recipient fixed in the template | yes, plus a fresh proof of work per call | 5/min, 50 per session, 100/day |
| `read` | `GET` and `HEAD` within `paths` | no | 60/min, 10 000/day |
| `transparent` | any allowed method and path (the original proxy) | no | 60/min, no daily budget |

Per-minute limits count per endpoint and client (IPv6 per /64); the site limit of 1000 calls per hour applies to all classes. Endpoints created before classes existed became `transparent` on upgrade; the API keys screen and the MCP output show a warning for this class, which suits only password-protected sites.

### Request templates (`ai`, `send`)

```json
{
  "path": "/chat/completions",
  "body": {"model": "gpt-4o-mini", "max_tokens": 400,
           "messages": [{"role": "system", "content": "Answer about the bakery."},
                        {"role": "user", "content": "{{question}}"}]},
  "fields": {"question": {"type": "string", "max_length": 1000, "required": true}},
  "response": "choices.0.message.content",
  "stream": false
}
```

- The visitor POSTs a JSON object to `/_dropsite/proxy/<name>` (no further path). Only the declared `fields` are accepted: an unknown field, a wrong type, a missing required field or a value over `max_length` / `max_items` answers `400 proxy_field` before anything is sent.
- Types: `string`, `number`, `integer`, `boolean`, `messages` (a list of `{role, content}` with role `user` or `assistant` only).
- In `body`, a string that is exactly `"{{field}}"` becomes the typed value (an omitted optional field becomes `null`); `{{field}}` inside longer text is replaced by the value as text. There are no expressions or conditions, and nothing of the visitor's request except these values reaches the upstream: no headers, no query, no path. The request goes to `upstream_url` + `path` with `method` (`POST` by default, `PUT`, `PATCH`) and `Content-Type: application/json`.
- `response` is a dotted path (`choices.0.message.content`): the page gets `{"result": <value>}` (`null` if the path is missing). An upstream answer of 400 or more becomes `502 proxy_upstream_status` with only the status, since such bodies may describe the owner's account. Without `response` the answer passes through as for `transparent`.
- `stream: true` asks for `text/event-stream`; with `response` each `data:` event is reduced to the value at the path as JSON (`data: "Hel"`), `data: [DONE]` passes through, and other lines are dropped.
- The whole template is at most 16 KB, with up to 20 fields.

### Visitor session

Classes `ai` and `send` accept calls only with a session token in `X-Dropsite-Session`. The page includes `<script src="/_dropsite/session.js"></script>` (about 3 KB, no widget) and calls `dropsite.fetch(url, options)` instead of `fetch`:

1. On page load the script fetches `GET /_dropsite/session/challenge` (an Altcha challenge with plain SHA-256, bound to the host) and solves it in a Web Worker started from the same file, falling back to the main thread. The base difficulty is 50 000 (well under a second); networks flagged by the bot detector get 10 and 100 times more (docs/security.md).
2. `POST /_dropsite/session` with `{"payload": "<solution>"}` returns `{"token", "expires_at", "quota"}`. Each solution is accepted once. The token is an HMAC-signed value bound to the site and the client network (an IPv4 address or an IPv6 /64), valid 30 minutes.
3. `dropsite.fetch` adds the token as a header (never a cookie, so it does not travel with other requests) and, on `401 proxy_session` (expired, quota used, other network), gets a new one and retries; on `401 proxy_pow` it solves a `send` challenge (`?purpose=send`) and sends it in `X-Dropsite-Pow` for every call of that endpoint.

Quotas are counted per token and endpoint in memory; a restart of control starts them over. Everything is served from the site's own origin, so a Content Security Policy with `script-src 'self'` (and `worker-src` falling back to it) is enough. The session paths are proxied before the access check of protected sites, like the Altcha script; the proxy itself stays behind it.

### Daily budget

Each call of an endpoint with a daily budget is counted before it is forwarded, per UTC day. When the budget is used up, calls answer `429 proxy_budget` with `Retry-After` up to midnight UTC, and further refusals of the day are answered from memory without database writes. The first refusal of the day writes the audit entry `proxy.budget.exhausted`, pauses the card on the API keys screen ("Daily budget used up") and, if the site has enabled and confirmed Telegram or e-mail form channels, sends one message there with a link to raise the budget. Counters are kept for 7 days. Hosts must be public DNS names: IP literals, `localhost`, `*.local`, `*.internal`, single-label names, the base domain and its subdomains, the API and UI hosts are refused.

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
- Rate limits: the class limit per minute per IP (IPv6 per /64) and endpoint, 1000 per hour per site (`429` with `Retry-After`). Refusals by origin, method, path and session happen before and do not use the site's quota. Banned networks get `429` before anything else, and Caddy blocks cloud and AI scraper ranges on `/_dropsite/proxy/*` unless the administrator turned that off (docs/security.md).
- The session headers `X-Dropsite-Session` and `X-Dropsite-Pow` are never forwarded.
- A disabled or unknown endpoint, or one of a deleted site, answers 404; an endpoint without a key answers `503 proxy_secret_missing` without contacting the upstream.

Errors are JSON in the API format: `{"error": {"code": "proxy_path", "message": "..."}}`.

## Protected sites

Caddy sends `/_dropsite/proxy/*` to control only after `forward_auth`, so a password-protected site serves its proxy only to visitors who signed in. The rest of `/_dropsite/*` (sign-in page, Altcha script) is still proxied before the access check.

## Usage and audit

Each endpoint counts forwarded calls, failed ones (upstream 5xx or unreachable), refused ones (origin, method, path, rate, missing key, size) and the time and status of the last call. Refused calls are counted in memory and written at most once every 10 seconds (and before the owner views the endpoints), so a flood of refused calls does not turn into database writes; a restart may lose the refusals of the last 10 seconds. No IP addresses, paths or bodies are stored. The audit log records `proxy.endpoint.create`, `proxy.endpoint.update`, `proxy.endpoint.delete`, `proxy.secret.set` and `proxy.secret.clear` with the site, the endpoint name and the upstream host, never the key. Visitor calls are not audited.

## Risks the owner accepts

- With `transparent` and `read`, anyone who opens the site can make its page call the API within the allowed methods, paths and rate limits. With `ai` and `send` a script can still solve sessions and send the declared fields, only slower and within the budget. Use a key with minimal rights and a spending limit at the provider, and keep paths narrow.
- The body of the API answer is passed through as is: an API that echoes the key in its body would reveal it.

## API and MCP

- `GET /api/v1/sites/{ref}/proxy/endpoints`, `PUT /api/v1/sites/{ref}/proxy/endpoints/{name}` (201 on creation, 200 on replacement), `DELETE /api/v1/sites/{ref}/proxy/endpoints/{name}`; see `openapi.json`.
- MCP tools on both servers: `list_proxy_endpoints`, `set_proxy_endpoint` (with `class`, `template`, `per_minute`, `daily_budget`, `session_quota`), `delete_proxy_endpoint` (with `confirm`). `set_proxy_endpoint` returns `secret_url` and a `next_step` asking the user to paste the key in the web interface and showing the `dropsite.fetch` call; a `transparent` endpoint carries a `warning`. The guide topic `proxy` explains when to use which class.
- Visitor paths on site addresses (not API v1): `GET /_dropsite/session.js`, `GET /_dropsite/session/challenge[?purpose=send]`, `POST /_dropsite/session`.
