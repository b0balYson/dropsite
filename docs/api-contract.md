# HTTP API v1: wire contract

Base URL: `https://<API_HOST>/api/v1`. Authentication: `Authorization: Bearer <token>` on every endpoint except `GET /openapi.json` and `POST /connect/exchange`. Only API tokens (`ds_…`) are accepted here; OAuth tokens work only on the MCP endpoint (see [ai-clients.md](ai-clients.md)). Bodies are JSON (`Content-Type: application/json`), except the deploy upload. Times are RFC 3339 in UTC. The machine-readable description is `GET /api/v1/openapi.json`.

## Errors

```json
{"error": {"code": "slug_taken", "message": "this address is already taken", "details": {"slug": "promo"}}}
```

`details` is optional. Codes are stable (see `internal/domain/errors.go`). Status mapping:

| Kind | Status |
| --- | --- |
| not found, including other users' resources and admin endpoints for non-admins | 404 |
| invalid input, archive validation | 400 |
| conflict (`email_taken`, `slug_taken`, `domain_taken`) | 409 |
| limit (`site_limit_reached`, `domain_limit_reached`) | 403 |
| too large (`archive_too_large`, `archive_file_too_large`, `archive_too_many_files`) | 413 |
| wrong method (`method_not_allowed`) | 405 with `Allow` |
| rate limited | 429 with `Retry-After` (seconds) |
| missing or invalid token | 401 |
| unexpected | 500, `{"code": "internal"}`, no details |

## Objects

```text
User     {id, email, role, status, created_at}
Limits   {max_site_bytes, max_file_bytes, max_files, max_sites_per_user, keep_releases}
Hostname {hostname, kind, verified, status, reason|null, created_at, reviewed_at|null}
                                                kind: default | slug | apex | external
                                                status: active | pending | rejected
Domain   Hostname + {site_id, site_title, owner_id, owner_email}
DNSCheck {hostname, verdict, addresses[], cname|null, expected[]}
                                                verdict: points_here | points_elsewhere | no_records | failed | unknown
Deploy   {id, site_id, source, size_bytes, file_count, status, created_at, created_by, current, preview_url|null}
Site     {id, title, url, urls[], slug|null, hostnames[], owner_id, created_at, updated_at, current_deploy: Deploy|null,
          noindex, protected, expires_at|null}
SiteItem {id, title, url, urls[], slug|null, owner_id, owner_email, last_deploy_at|null, size_bytes, created_at, updated_at,
          noindex, protected, expires_at|null}
LimitOverrides {max_site_bytes|null, max_file_bytes|null, max_files|null, max_sites_per_user|null, keep_releases|null}
Token    {id, name, prefix, created_at, last_used_at|null, expires_at|null, revoked_at|null}
Audit    {id, actor_id|null, actor_email, action, target, meta{}, ip, created_at}
List     {items[], total}
Stats    {site_id, from, to, days[{date, views, visitors}], totals{views, visitors}, top_pages[{name, views}], top_referrers[{name, views}]}
Channel  {id, kind, target, status, enabled, created_at, updated_at}
                                                kind: telegram | email | webhook; status: pending | active
Submission {id, form, host, created_at, fields[{name, value}], deliveries[{channel_id, kind, target, status, attempts, last_error, next_attempt_at|null, sent_at|null}]}
                                                delivery status: pending | sent | failed
FormSettings {telegram{bot, token_set}, smtp{host, port, username, password_set, from, security}, retention_days, altcha_max_number, secrets_unreadable}
```

IDs of users, tokens and audit entries are JSON strings (`"id": "2"`); `transfer` accepts `user_id` as a string or a number. Every response carries `X-Request-Id`, which also appears in server logs.

`url` is the primary address (slug if set, otherwise the random default). `urls` lists every active address, primary first; external domains appear there only after an administrator approves them, while `hostnames` lists every address in any status.

## Endpoints

| Method and path | Request | Response |
| --- | --- | --- |
| `POST /connect/exchange` | `{code, name?}`, no token | `201 {token: Token, value, user: User}`; `value` is shown once |
| `GET /me` | | `{user: User, limits: Limits, site_count}` |
| `GET /sites` | `?limit&offset` | `List<SiteItem>` (own sites) |
| `POST /sites` | `{title, slug?}` | `201 Site` |
| `GET /sites/{ref}` | | `Site` |
| `PATCH /sites/{ref}` | `{title?, slug?, noindex?, protected?, password?, expires_at?}`; `slug: ""` or `null` removes it | `Site` |
| `DELETE /sites/{ref}` | | `204` |
| `POST /sites/{ref}/deploys` | `multipart/form-data`, field `archive` (zip or tar.gz) | `201 {deploy_id, url, urls[], deploy: Deploy, site: Site}` |
| `GET /sites/{ref}/deploys` | | `List<Deploy>` (newest first) |
| `POST /sites/{ref}/rollback` | `{deploy_id?}`; empty means the previous release | `{deploy_id, url, urls[], deploy: Deploy, site: Site}` |
| `GET /sites/{ref}/hostnames` | | `List<Hostname>` (every address, any status) |
| `POST /sites/{ref}/hostnames` | `{hostname}` | `201 Hostname` (external, `pending`) |
| `DELETE /sites/{ref}/hostnames/{hostname}` | | `204`; only external domains, any status |
| `GET /sites/{ref}/stats` | `?days` (1–400, default 30) | `Stats` |
| `GET /sites/{ref}/forms/channels` | | `List<Channel>` (no secrets) |
| `POST /sites/{ref}/forms/channels` | `{kind, target?}` | `201 {channel: Channel, secret?, link?}` |
| `PATCH /sites/{ref}/forms/channels/{id}` | `{enabled}` | `Channel` |
| `DELETE /sites/{ref}/forms/channels/{id}` | | `204` |
| `POST /sites/{ref}/forms/channels/{id}/verify` | `{code}` | `Channel` |
| `POST /sites/{ref}/forms/channels/{id}/secret` | | `{channel: Channel, secret}` (webhooks only) |
| `GET /sites/{ref}/forms/settings` | | `{require_altcha}` |
| `PATCH /sites/{ref}/forms/settings` | `{require_altcha}` | `{require_altcha}` |
| `GET /sites/{ref}/forms/submissions` | `?form&limit&offset` | `List<Submission>` (newest first) |
| `GET /sites/{ref}/forms/submissions.csv` | `?form` | `text/csv`, up to 10 000 newest |
| `GET /tokens` | | `List<Token>` |
| `POST /tokens` | `{name, expires_in_days?}` | `201 {token: Token, value}`; `value` is shown once |
| `DELETE /tokens/{id}` | | `204` |
| `GET /admin/users` | `?q&status&limit&offset` | `List<User + site_count>` |
| `POST /admin/users` | `{email, role?}` | `201 {user: User, invite_url, invite_expires_at}` |
| `GET /admin/users/{id}` | | `{user: User, limits: Limits, limit_overrides: LimitOverrides}` |
| `PATCH /admin/users/{id}` | `{blocked: bool}`, `{reissue_invite: true}` or `{limits: LimitOverrides}` | `{user: User, invite_url?, invite_expires_at?, limits?, limit_overrides?}` |
| `GET /admin/sites` | `?q&limit&offset` | `List<SiteItem>` (all sites) |
| `POST /admin/sites/{ref}/apex` | | `Site` |
| `DELETE /admin/sites/{ref}/apex` | | `Site` |
| `POST /admin/sites/{ref}/transfer` | `{user_id}` | `Site` |
| `GET /admin/audit` | `?actor_id&action&from&to&limit&offset` | `List<Audit>` |
| `GET /admin/domains` | `?status&q&limit&offset` | `List<Domain>` (oldest first) |
| `POST /admin/domains/{hostname}/approve` | | `Domain` |
| `POST /admin/domains/{hostname}/reject` | `{reason}` | `Domain` |
| `POST /admin/domains/{hostname}/dns-check` | | `DNSCheck` |
| `GET /admin/forms/settings` | | `FormSettings` |
| `PATCH /admin/forms/settings` | `{telegram_token?, smtp?{host?, port?, username?, password?, from?, security?}, retention_days?, altcha_max_number?}` | `FormSettings` |
| `GET /openapi.json` | no auth | OpenAPI 3.1 document |

`{ref}` is a site ID or its slug. `limit` defaults to 50; values outside 1–200 are rejected with 400. JSON request bodies are limited to 64 KiB.

`expires_in_days` accepts 1–3650; omitted means the token never expires. In `GET /admin/audit`, `from` and `to` accept RFC 3339 or `YYYY-MM-DD`; `from` is inclusive, an RFC 3339 `to` is exclusive and a date `to` includes the whole day. `PATCH /admin/users/{id}` with `blocked` returns only `{user}`; the invite fields appear only with `reissue_invite`.

The deploy source is `cli` when the request carries `X-Dropsite-Client: cli/<version>`, otherwise `api`. The upload may be at most `max_site_bytes + max_files KiB + 1 MiB`; larger bodies get 413 without being read fully.

External domains (see `docs/external-domains.md`): `hostname` is normalised (lower case, punycode, no trailing dot) and refused with `domain_invalid` (not a name of two or more labels, an IP, a scheme, path or port) or `domain_forbidden` (the base domain or below it, the API host or below it, the UI host or one of its parents). A site holds at most `MAX_DOMAINS_PER_SITE` external domains in any status (`domain_limit_reached`, `details.limit`). `reject` requires a reason of 1–200 characters (`domain_reason_invalid`); rejecting an approved domain withdraws the approval. In `GET /admin/domains`, `status` is `pending`, `active`, `rejected` or `all` (default). `dns-check` takes at most 5 seconds and never fails because of DNS: problems are reported in `verdict`.
`GET /sites/{ref}/stats` counts page views of HTML pages from the sites access log (bots and assets excluded), by UTC day; `days` lists every day of the period ending today, oldest first, including days without views. `visitors` is the approximate number of unique visitors of a day (hash of IP and User-Agent with a daily salt); the total is the sum of daily values. In `top_referrers` an empty `name` is a direct visit; in both tops `*` stands for everything beyond the rows stored per day (500 pages, 200 referrers). Statistics lag behind by up to one collection interval (`STATS_INTERVAL`, default one minute).

`preview_url` is the address that serves exactly this release, `https://p-<deploy id>-<site id>.<BASE_DOMAIN>/` (with `X-Robots-Tag: noindex`). It is set only when the installation runs with `TLS_MODE=wildcard` and the release is `ready`; otherwise it is `null` (see `docs/tls.md`).

## Site access and limits

- `noindex: true` makes every response of the site carry `X-Robots-Tag: noindex, nofollow`.
- `password` (10–256 bytes) protects the site or changes its password; it is stored as an argon2id hash and never returned. `protected: true` without `password` is accepted only when the site already has one (otherwise `400 site_password_required`); `protected: false` removes protection and must not come with `password`. Changing or removing the password signs every visitor out.
- `expires_at` is an RFC 3339 time in the future, at most 10 years ahead (`400 expiry_invalid` otherwise); `null` removes it. Within a minute after it passes the site is soft-deleted (`site.expire` in the audit log) and purged 7 days later.
- `limits` in `PATCH /admin/users/{id}` replaces the user's personal overrides; a missing or `null` field inherits the global value, `{}` resets all. Effective limits follow user override → admin setting → environment → default; `GET /me` returns them. Deploy size limits and release pruning use the limits of the site's owner.

`POST /connect/exchange` trades a one-time connect code from the Connect AI page for a new API token of its owner. Unknown, used and expired codes all answer `400 connect_code_invalid`; ten failures per IP within 15 minutes give `429`. Without `name` the token is called `Connected <date>`.

Outside `/api/v1` the API host also serves `/mcp`, `/.well-known/oauth-protected-resource[/mcp]`, `/.well-known/oauth-authorization-server` and `/oauth/*` (see [ai-clients.md](ai-clients.md)).

Site forms (see `docs/forms.md`): a webhook channel returns its signing `secret` only on creation and rotation; a Telegram channel created without `target` returns a binding `link` (`https://t.me/<bot>?start=<code>`). Telegram channels with a chat ID and e-mail channels start `pending` and become `active` after `verify` with the code sent to them (30 minutes, 5 attempts; `channel_code_invalid`). Other codes: `channel_invalid`, `channel_unavailable` (Telegram or SMTP not configured, or the code could not be sent), `channel_limit_reached` (10 per site, 403), `telegram_token_invalid`, `setting_invalid`. In `PATCH /admin/forms/settings` a missing field keeps the value and an empty string clears it; the bot token and SMTP password are never returned. Visitor endpoints on site hosts (`POST /_form/<name>`, `GET /_form/challenge`, `GET /_dropsite/altcha.js`) are not part of API v1.
