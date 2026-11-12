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
| conflict (`email_taken`, `slug_taken`) | 409 |
| limit (`site_limit_reached`) | 403 |
| too large (`archive_too_large`, `archive_file_too_large`, `archive_too_many_files`) | 413 |
| wrong method (`method_not_allowed`) | 405 with `Allow` |
| rate limited | 429 with `Retry-After` (seconds) |
| missing or invalid token | 401 |
| unexpected | 500, `{"code": "internal"}`, no details |

## Objects

```text
User     {id, email, role, status, created_at}
Limits   {max_site_bytes, max_file_bytes, max_files, max_sites_per_user, keep_releases}
Hostname {hostname, kind}                       kind: default | slug | apex
Deploy   {id, site_id, source, size_bytes, file_count, status, created_at, created_by, current}
Site     {id, title, url, urls[], slug|null, hostnames[], owner_id, created_at, updated_at, current_deploy: Deploy|null}
SiteItem {id, title, url, urls[], slug|null, owner_id, owner_email, last_deploy_at|null, size_bytes, created_at, updated_at}
Token    {id, name, prefix, created_at, last_used_at|null, expires_at|null, revoked_at|null}
Audit    {id, actor_id|null, actor_email, action, target, meta{}, ip, created_at}
List     {items[], total}
```

IDs of users, tokens and audit entries are JSON strings (`"id": "2"`); `transfer` accepts `user_id` as a string or a number. Every response carries `X-Request-Id`, which also appears in server logs.

`url` is the primary address (slug if set, otherwise the random default). `urls` lists every address, primary first.

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
| `GET /openapi.json` | no auth | OpenAPI 3.1 document |

`{ref}` is a site ID or its slug. `limit` defaults to 50; values outside 1–200 are rejected with 400. JSON request bodies are limited to 64 KiB.

`expires_in_days` accepts 1–3650; omitted means the token never expires. In `GET /admin/audit`, `from` and `to` accept RFC 3339 or `YYYY-MM-DD`; `from` is inclusive, an RFC 3339 `to` is exclusive and a date `to` includes the whole day. `PATCH /admin/users/{id}` with `blocked` returns only `{user}`; the invite fields appear only with `reissue_invite`.

The deploy source is `cli` when the request carries `X-Pushpage-Client: cli/<version>`, otherwise `api`. The upload may be at most `max_site_bytes + max_files KiB + 1 MiB`; larger bodies get 413 without being read fully.
