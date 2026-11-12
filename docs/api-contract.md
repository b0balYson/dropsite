# HTTP API v1: wire contract

Base URL: `https://<API_HOST>/api/v1`. Authentication: `Authorization: Bearer <token>` on every endpoint except `GET /openapi.json`. Bodies are JSON (`Content-Type: application/json`), except the deploy upload. Times are RFC 3339 in UTC. The machine-readable description is `GET /api/v1/openapi.json`.

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
Site     {id, title, url, urls[], slug|null, hostnames[], owner_id, created_at, updated_at, current_deploy: Deploy|null}
SiteItem {id, title, url, urls[], slug|null, owner_id, owner_email, last_deploy_at|null, size_bytes, created_at, updated_at}
Token    {id, name, prefix, created_at, last_used_at|null, expires_at|null, revoked_at|null}
Audit    {id, actor_id|null, actor_email, action, target, meta{}, ip, created_at}
List     {items[], total}
Stats    {site_id, from, to, days[{date, views, visitors}], totals{views, visitors}, top_pages[{name, views}], top_referrers[{name, views}]}
```

IDs of users, tokens and audit entries are JSON strings (`"id": "2"`); `transfer` accepts `user_id` as a string or a number. Every response carries `X-Request-Id`, which also appears in server logs.

`url` is the primary address (slug if set, otherwise the random default). `urls` lists every active address, primary first; external domains appear there only after an administrator approves them, while `hostnames` lists every address in any status.

## Endpoints

| Method and path | Request | Response |
| --- | --- | --- |
| `GET /me` | | `{user: User, limits: Limits, site_count}` |
| `GET /sites` | `?limit&offset` | `List<SiteItem>` (own sites) |
| `POST /sites` | `{title, slug?}` | `201 Site` |
| `GET /sites/{ref}` | | `Site` |
| `PATCH /sites/{ref}` | `{title?, slug?}`; `slug: ""` or `null` removes it | `Site` |
| `DELETE /sites/{ref}` | | `204` |
| `POST /sites/{ref}/deploys` | `multipart/form-data`, field `archive` (zip or tar.gz) | `201 {deploy_id, url, urls[], deploy: Deploy, site: Site}` |
| `GET /sites/{ref}/deploys` | | `List<Deploy>` (newest first) |
| `POST /sites/{ref}/rollback` | `{deploy_id?}`; empty means the previous release | `{deploy_id, url, urls[], deploy: Deploy, site: Site}` |
| `GET /sites/{ref}/hostnames` | | `List<Hostname>` (every address, any status) |
| `POST /sites/{ref}/hostnames` | `{hostname}` | `201 Hostname` (external, `pending`) |
| `DELETE /sites/{ref}/hostnames/{hostname}` | | `204`; only external domains, any status |
| `GET /sites/{ref}/stats` | `?days` (1–400, default 30) | `Stats` |
| `GET /tokens` | | `List<Token>` |
| `POST /tokens` | `{name, expires_in_days?}` | `201 {token: Token, value}`; `value` is shown once |
| `DELETE /tokens/{id}` | | `204` |
| `GET /admin/users` | `?q&status&limit&offset` | `List<User + site_count>` |
| `POST /admin/users` | `{email, role?}` | `201 {user: User, invite_url, invite_expires_at}` |
| `PATCH /admin/users/{id}` | `{blocked: bool}` or `{reissue_invite: true}` | `{user: User, invite_url?, invite_expires_at?}` |
| `GET /admin/sites` | `?q&limit&offset` | `List<SiteItem>` (all sites) |
| `POST /admin/sites/{ref}/apex` | | `Site` |
| `DELETE /admin/sites/{ref}/apex` | | `Site` |
| `POST /admin/sites/{ref}/transfer` | `{user_id}` | `Site` |
| `GET /admin/audit` | `?actor_id&action&from&to&limit&offset` | `List<Audit>` |
| `GET /admin/domains` | `?status&q&limit&offset` | `List<Domain>` (oldest first) |
| `POST /admin/domains/{hostname}/approve` | | `Domain` |
| `POST /admin/domains/{hostname}/reject` | `{reason}` | `Domain` |
| `POST /admin/domains/{hostname}/dns-check` | | `DNSCheck` |
| `GET /openapi.json` | no auth | OpenAPI 3.1 document |

`{ref}` is a site ID or its slug. `limit` defaults to 50; values outside 1–200 are rejected with 400. JSON request bodies are limited to 64 KiB.

`expires_in_days` accepts 1–3650; omitted means the token never expires. In `GET /admin/audit`, `from` and `to` accept RFC 3339 or `YYYY-MM-DD`; `from` is inclusive, an RFC 3339 `to` is exclusive and a date `to` includes the whole day. `PATCH /admin/users/{id}` with `blocked` returns only `{user}`; the invite fields appear only with `reissue_invite`.

The deploy source is `cli` when the request carries `X-Dropsite-Client: cli/<version>`, otherwise `api`. The upload may be at most `max_site_bytes + max_files KiB + 1 MiB`; larger bodies get 413 without being read fully.

External domains (see `docs/external-domains.md`): `hostname` is normalised (lower case, punycode, no trailing dot) and refused with `domain_invalid` (not a name of two or more labels, an IP, a scheme, path or port) or `domain_forbidden` (the base domain or below it, the API host or below it, the UI host or one of its parents). A site holds at most `MAX_DOMAINS_PER_SITE` external domains in any status (`domain_limit_reached`, `details.limit`). `reject` requires a reason of 1–200 characters (`domain_reason_invalid`); rejecting an approved domain withdraws the approval. In `GET /admin/domains`, `status` is `pending`, `active`, `rejected` or `all` (default). `dns-check` takes at most 5 seconds and never fails because of DNS: problems are reported in `verdict`.
`GET /sites/{ref}/stats` counts page views of HTML pages from the sites access log (bots and assets excluded), by UTC day; `days` lists every day of the period ending today, oldest first, including days without views. `visitors` is the approximate number of unique visitors of a day (hash of IP and User-Agent with a daily salt); the total is the sum of daily values. In `top_referrers` an empty `name` is a direct visit; in both tops `*` stands for everything beyond the rows stored per day (500 pages, 200 referrers). Statistics lag behind by up to one collection interval (`STATS_INTERVAL`, default one minute).

`preview_url` is the address that serves exactly this release, `https://p-<deploy id>-<site id>.<BASE_DOMAIN>/` (with `X-Robots-Tag: noindex`). It is set only when the installation runs with `TLS_MODE=wildcard` and the release is `ready`; otherwise it is `null` (see `docs/tls.md`).
