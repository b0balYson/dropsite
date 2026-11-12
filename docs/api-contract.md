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
SiteItem {id, title, url, urls[], slug|null, owner_id, owner_email, hostnames[], last_deploy_at|null, size_bytes, created_at,
          updated_at, noindex, protected, expires_at|null}
LimitOverrides {max_site_bytes|null, max_file_bytes|null, max_files|null, max_sites_per_user|null, keep_releases|null}
Token    {id, name, prefix, created_at, last_used_at|null, expires_at|null, revoked_at|null}
Audit    {id, actor_id|null, actor_email, action, target, meta{}, ip, created_at}
List     {items[], total}
Stats    {site_id, from, to, days[{date, views, visitors}], totals{views, visitors}, top_pages[{name, views}], top_referrers[{name, views}]}
Channel  {id, kind, target, status, enabled, consecutive_failures, paused_at|null, created_at, updated_at}
                                                kind: telegram | email | webhook; status: pending | active
Submission {id, form, host, created_at, fields[{name, value}], deliveries[{channel_id, kind, target, status, attempts, last_error, next_attempt_at|null, sent_at|null}]}
                                                delivery status: pending | sent | failed | cancelled
FormSiteSettings {site_id, site_url, require_altcha, form_names[], telegram_available, telegram_bot|null, email_available, cabinet_url}
Counters {site_id, own{yandex_metrica_id, google_analytics_id}, global{...}, new_deploy_id|null}   "" means not set
Connection {kind, id, name, detail|null, connected_at, last_used_at|null, expires_at|null, current}
                                                kind: app (OAuth) | token
SiteFile {path, size, sha256, type}
FilesResult {deploy_id, url, urls[], deploy: Deploy, site: Site, base_deploy_id, written, deleted, fetched[{url, path, type, bytes, original_bytes, resized}]}
Upload   {id, mode, site_id, base_deploy_id, files, bytes, expires_at, max_bytes, max_files, max_file_bytes, max_call_bytes, fetched[]}
Collection {name, title, mode, schema|null, documents, pending, public_url, created_at, updated_at}
                                                mode: public | moderated
Document {id, data{}, position, status, created_at, updated_at}
                                                status: approved | pending
FormSettings {telegram{bot, token_set}, smtp{host, port, username, password_set, from, security}, retention_days, altcha_max_number, secrets_unreadable}
```

IDs of users, tokens and audit entries are JSON strings (`"id": "2"`); `transfer` accepts `user_id` as a string or a number. Every response carries `X-Request-Id`, which also appears in server logs.

`url` is the primary address (slug if set, otherwise the random default). `urls` lists every active address, primary first; external domains appear there only after an administrator approves them, while `hostnames` lists every address in any status.

## Endpoints

| Method and path | Request | Response |
| --- | --- | --- |
| `POST /connect/exchange` | `{code, name?}`, no token | `201 {token: Token, value, user: User}`; `value` is shown once |
| `GET /me` | | `{user: User, limits: Limits, site_count, max_domains_per_site, max_inline_bytes, ui_url, root_site: Site\|null}`; `root_site` only for administrators |
| `GET /sites` | `?limit&offset` | `List<SiteItem>` (own sites) |
| `POST /sites` | `{title, slug?}` | `201 Site` |
| `GET /sites/{ref}` | | `Site` |
| `PATCH /sites/{ref}` | `{title?, slug?, noindex?, protected?, password?, expires_at?}`; `slug: ""` or `null` removes it | `Site` |
| `DELETE /sites/{ref}` | | `204` |
| `POST /sites/{ref}/deploys` | `multipart/form-data`, field `archive` (zip or tar.gz); `?draft=true` saves the site's draft instead of going live | `201 {deploy_id, url, urls[], deploy: Deploy, site: Site, quality: QualitySummary\|null}` |
| `GET /sites/{ref}/deploys` | | `List<Deploy>` (newest first) |
| `GET /sites/{ref}/deploys/{id}` | `id` or `current` | `Deploy` + `quality: QualityReport\|null` (docs/quality.md) |
| `POST /sites/{ref}/rollback` | `{deploy_id?}`; empty means the previous release | `{deploy_id, url, urls[], deploy: Deploy, site: Site, quality}` (the target's report) |
| `POST /sites/{ref}/draft/publish` | | like rollback, to the site's draft; `400 no_draft` without one |
| `DELETE /sites/{ref}/draft` | | `Site` (`draft: null`); `400 no_draft` without one |
| `GET /sites/{ref}/hostnames` | | `List<Hostname>` (every address, any status) |
| `POST /sites/{ref}/hostnames` | `{hostname}` | `201 Hostname` (external, `pending`) |
| `DELETE /sites/{ref}/hostnames/{hostname}` | | `204`; only external domains, any status |
| `GET /sites/{ref}/domain-setup` | | `{site_id, cname_target, server_ips[], max_domains}` |
| `GET /sites/{ref}/stats` | `?days` (1–400, default 30) | `Stats` |
| `GET /sites/{ref}/attachments` | | `{site_id, forms{names[], channels, working_channels, require_altcha}, counters{own, global}}`: forms that received stored submissions, delivery channels (working = confirmed, enabled, not paused), own and global counters |
| `GET /sites/{ref}/counters` | | `Counters` |
| `PATCH /sites/{ref}/counters` | `{yandex_metrica_id?, google_analytics_id?}`; `""` removes one | `Counters`; a published site gets a new release |
| `GET /sites/{ref}/files` | `?deploy_id&prefix` | `{site_id, url, deploy_id, current, files: SiteFile[], total_files, total_bytes, truncated}` |
| `POST /sites/{ref}/files/read` | `{deploy_id?, paths[]}` (1–50 paths or patterns) | `{site_id, deploy_id, current, files[SiteFile + {encoding, content, note}], missing[], truncated}` |
| `PATCH /sites/{ref}/files` | `{base_deploy_id, files?[{path, content, encoding?}], delete?[], fetch?[{url, path}]}` | `FilesResult`; `409 deploy_conflict` when the base is not current |
| `POST /uploads` | `{site?, title?, mode?, base_deploy_id?}` | `201 Upload` |
| `POST /uploads/{id}/files` | `{files?[], fetch?[]}` | `Upload` |
| `POST /uploads/{id}/commit` | `{delete?[]}` (patch mode) | `201 FilesResult` |
| `POST /uploads/{id}/schedule` | `{publish_at}` (RFC 3339 with zone) | `201 Schedule`; the session becomes a prepared release that goes live at `publish_at` |
| `POST /sites/{ref}/schedules` | `{deploy_id, publish_at}` | `201 Schedule` (`id, site_id, site_title, site_url, deploy_id, publish_at, status, error, created_at, finished_at, preview_url`) |
| `GET /schedules?site=` | | `List<Schedule>`: pending and finished in the last 7 days |
| `DELETE /schedules/{id}` | | `Schedule` (`cancelled`); `409 schedule_not_pending` |
| `POST /sites/{ref}/clone` | `{title?, slug?}` | `201 DeployResult` of the new site (files only) |
| `GET /templates?lang=` | | `List<{name, title, description}>` |
| `POST /templates/{name}/sites` | `{title?, slug?, lang?}` | `201 DeployResult`; `404 template_not_found` |
| `GET /sites/{ref}/screenshot?deploy_id=&device=` | | `200 image/png` with `X-Dropsite-Site-Id`, `X-Dropsite-Deploy-Id`, `X-Dropsite-Device`, `X-Dropsite-Viewport`; `503 renderer_unavailable` without a renderer ([screenshots.md](screenshots.md)) |
| `GET /sites/{ref}/visual-diff?from=&to=&device=` | | `{site_id, from_deploy_id, to_deploy_id, device, changed_percent, width, height, image_png}` (base64 PNG); `503 renderer_unavailable` |
| `GET /sites/{ref}/forms/channels` | | `List<Channel>` (no secrets) |
| `POST /sites/{ref}/forms/channels` | `{kind, target?}` | `201 {channel: Channel, secret?, link?}` |
| `PATCH /sites/{ref}/forms/channels/{id}` | `{enabled}` | `Channel`; `true` also resumes a paused channel |
| `DELETE /sites/{ref}/forms/channels/{id}` | | `204` |
| `POST /sites/{ref}/forms/channels/{id}/verify` | `{code}` | `Channel` |
| `POST /sites/{ref}/forms/channels/{id}/secret` | | `{channel: Channel, secret}` (webhooks only) |
| `GET /sites/{ref}/publish-settings` | | `PublishSettings` `{site_id, optimize, installation_enabled, effective}` |
| `PATCH /sites/{ref}/publish-settings` | `{optimize}` | `PublishSettings`; applies from the next publication (docs/publishing.md) |
| `GET /sites/{ref}/forms/settings` | | `FormSiteSettings` |
| `PATCH /sites/{ref}/forms/settings` | `{require_altcha}` | `FormSiteSettings` |
| `GET /sites/{ref}/forms/submissions` | `?form&limit&offset` | `List<Submission>` (newest first) |
| `GET /sites/{ref}/forms/submissions.csv` | `?form` | `text/csv`, up to 10 000 newest and 64 MB, streamed; 100 field columns + `_other` (JSON) |
| `GET /sites/{ref}/forms/submissions/search` | `?form&q&since&until&days&limit&cursor` | `{items: Submission[], total, next_cursor\|null, server_time, cabinet_url}` (newest first) |
| `GET /sites/{ref}/forms/submissions/export` | `?form&q&since&until&days&max_rows` | `{csv, rows, total, truncated, cabinet_url}` |
| `GET /sites/{ref}/collections` | | `List<Collection>` |
| `POST /sites/{ref}/collections` | `{name, title?, mode?, schema?}` | `201 Collection` |
| `PATCH /sites/{ref}/collections/{name}` | `{title?, mode?, schema?}` (`schema: null` removes it) | `Collection` |
| `DELETE /sites/{ref}/collections/{name}` | `{confirm}` or `?confirm` = the name | `204` |
| `GET /sites/{ref}/collections/{name}/documents` | `?status&order&limit&cursor` | `{items: Document[], next_cursor\|null}` |
| `POST /sites/{ref}/collections/{name}/documents` | `{documents[{id?, data, position?}], replace?}` (≤ 1000, body ≤ 20 MiB) | `{ids[], collection: Collection}` |
| `POST /sites/{ref}/collections/{name}/delete-documents` | `{ids[]}` | `{count, collection: Collection}` |
| `DELETE /sites/{ref}/collections/{name}/documents/{doc}` | | `204` |
| `POST /sites/{ref}/collections/{name}/moderate` | `{ids[], action}` (`approve` \| `reject`) | `{count, collection: Collection}` |
| `GET /tokens` | | `List<Token>` |
| `POST /tokens` | `{name, expires_in_days?}` | `201 {token: Token, value}`; `value` is shown once |
| `DELETE /tokens/{id}` | | `204` |
| `GET /connections` | | `List<Connection>` (apps and active tokens, no secrets) |
| `DELETE /connections/{kind}/{id}` | | `204` |
| `PUT /root-site` | `{site, confirm}` | `{root_url, site: Site}`; administrators only |
| `DELETE /root-site` | `?confirm` | `{root_url, site: Site}`; administrators only |
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

`{ref}` is a site ID or its slug. `limit` defaults to 50; values outside 1–200 are rejected with 400. JSON request bodies are limited to 64 KiB, except `PATCH /sites/{ref}/files` and `POST /uploads/{id}/files` (32 MiB; at most 16 MB of decoded file content per call).

## Endpoints for assistants

These endpoints give the local MCP server of the CLI (`dropsite mcp`) the tools of the remote `/mcp` endpoint; they call the same service methods, so limits, checks and errors are the same (see [ai-clients.md](ai-clients.md)).

- Site files and upload sessions follow the spec of `update_site_files` and `begin_upload`/`upload_files`/`commit_upload`: releases they create have the source `mcp`; downloads in `fetch` go only to public `https` addresses; a stale `base_deploy_id` gives `409 deploy_conflict`. A site without a release answers `400 site_not_published`. Upload sessions belong to the user (at most 3, closed after 30 idle minutes); an unknown, expired or other user's session answers `400 upload_expired`.
- `PATCH /sites/{ref}/counters` changes only the fields present; with neither it answers 400.
- `GET /connections` marks with `current` the token of the request; `DELETE /connections/{kind}/{id}` disconnects an app (all its tokens) or revokes a token. Credentials are never created here: tokens come from `POST /tokens` or the web interface.
- `PUT /root-site` and `DELETE /root-site` require `confirm` to repeat the ID or slug of the site (for `DELETE`, of the site now on the root domain). Users who are not administrators get `400 invalid_input`, the same answer an assistant gets.
- In submission search and export, `since` and `until` take `YYYY-MM-DD` (UTC; a date `until` includes the whole day) or RFC 3339 (`until` exclusive); `days` (1–3650) counts back from the server clock and overrides `since`. `limit` is 1–100 (default 20), `max_rows` 1–1000 (default 200); the CSV is at most 256 KB and `truncated` tells whether newer rows were left out.

`expires_in_days` accepts 1–3650; omitted means the token never expires. In `GET /admin/audit`, `from` and `to` accept RFC 3339 or `YYYY-MM-DD`; `from` is inclusive, an RFC 3339 `to` is exclusive and a date `to` includes the whole day. `PATCH /admin/users/{id}` with `blocked` returns only `{user}`; the invite fields appear only with `reissue_invite`.

The deploy source is `mcp` when the request carries `X-Dropsite-Assistant: mcp`, `cli` when it carries `X-Dropsite-Client: cli/<version>`, otherwise `api`. `X-Dropsite-Assistant: mcp` marks a request made for an AI assistant (the local MCP server of the CLI): the token then has the rights it would have on `/mcp` — no administrator powers except the root domain, audit entries with the source `mcp`. The header can only lower rights. The upload may be at most `max_site_bytes + max_files KiB + 1 MiB`; larger bodies get 413 without being read fully.

Every publication of new content (`POST /sites/{ref}/deploys`, `PATCH /sites/{ref}/files`, `POST /uploads/{id}/commit`, the web interface and MCP) scans the text files of the new release for high-confidence secrets: JWTs whose payload has `role: service_role`, Supabase `sb_secret_` keys, Stripe `sk_live_`/`rk_live_`, OpenAI and Anthropic API keys, Telegram bot tokens, AWS access key IDs, PEM private key blocks and GitHub tokens. A hit answers `400 secrets_found` with `details.found` (`path:line (kind)` joined by `; `, at most 20 entries, then `and N more`) and `details.count`; the values are never returned or logged and the current release stays. Public keys (Supabase anon JWTs and `sb_publishable_`, Stripe `pk_`, Firebase `AIza` web keys) and obvious placeholders pass; binary files are not scanned. Rollbacks and the re-release made for changed counters are not rescanned.

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

Site forms (see `docs/forms.md`): a webhook channel returns its signing `secret` only on creation and rotation; a Telegram channel created without `target` returns a binding `link` (`https://t.me/<bot>?start=<code>`). Telegram channels with a chat ID and e-mail channels start `pending` and become `active` after `verify` with the code sent to them (30 minutes, 5 attempts; `channel_code_invalid`). A webhook channel is paused (`paused_at` set) after 10 failed attempts in a row; turning a channel off cancels its queued deliveries. Confirmation codes are limited to 3 per recipient per 24 hours and 100 per installation per hour (`rate_limited`). Other codes: `channel_invalid`, `channel_unavailable` (Telegram or SMTP not configured, or the code could not be sent), `channel_limit_reached` (10 per site, 403), `telegram_token_invalid`, `setting_invalid`. In `PATCH /admin/forms/settings` a missing field keeps the value and an empty string clears it; the bot token and SMTP password are never returned. Visitor endpoints on site hosts (`POST /_form/<name>`, `GET /_form/challenge`, `GET /_dropsite/altcha.js`, and the sign-in page of protected sites `/_dropsite/login` with its challenge `GET /_dropsite/login/challenge`) are not part of API v1.

Site collections: small JSON data of a site that its pages read without a new release. A collection name and a document ID are 1-64 characters `a-z`, `0-9`, `-`, `_` starting with a letter or digit (lower-cased). `mode` is `public` (only the owner writes) or `moderated` (visitors propose, only approved documents are public; a `schema` is required). A `schema` is a JSON Schema (draft-07 or 2020-12) with `"type": "object"`, at most 16 KB; `$ref` may point only inside the schema (`#/...` or `#anchor`) and must not loop back without descending into a property or an item, and `$dynamicRef` and nested `$id` are refused; a changed schema applies only to documents written afterwards. Limits: 20 collections per site, 5000 documents per collection, 16 KB per document in compact form, 1000 documents per write, 1000 pending documents (`limit_exceeded`, 403). Owner writes are atomic and approved at once; an error in any document rejects the whole write with `document_invalid` and `details{index, field}`. Other codes: `collection_invalid` (`details.field`: `name`, `title`, `mode` or `schema`), `collection_exists` (409). Another user's site answers 404.

Visitor endpoints on site hosts, served after the access check of protected sites (also not part of API v1; every answer is JSON with `Cache-Control: no-cache`):

| Method and path | Request | Response |
| --- | --- | --- |
| `GET /_dropsite/data/<name>` | `?order=position\|newest\|oldest&limit=1..500&after=<next>` | `{items[{id, data, position, created_at, updated_at}], next\|null}`, approved documents only, `ETag` (304 on `If-None-Match`) |
| `GET /_dropsite/data/<name>/<id>` | | one approved document |
| `POST /_dropsite/data/<name>` | `application/json` object ≤ 8 KB | `202 {ok: true, status: "pending"}` |

A proposal drops keys starting with `_` and `altcha`, rejects keys missing from the schema's `properties` (`400 document_invalid`) and is protected like site forms: a non-empty `_hp` is answered like success and not stored, a foreign `Origin` gives `403 form_origin_mismatch`, rate limits per IP and per site give `429`, the site flag "require Altcha" needs a valid unused `altcha` solution from `GET /_form/challenge` (`400 form_captcha_invalid`). `POST` to a public collection gives `405 collection_read_only`; a full collection gives `429 collection_pending_full`; another content type gives `415`. Unknown collections and documents, pending documents and inactive addresses give 404.
Secret proxy (see `docs/proxy.md`): `GET /sites/{ref}/proxy/endpoints` lists `ProxyEndpoint {name, upstream_url, allowed_hosts[], methods[], paths[], auth, auth_name|null, enabled, secret_set, secret_updated_at|null, secret_url, call_url, usage{requests, errors, rejected, last_used_at|null, last_status|null}, created_at, updated_at}`; `PUT /sites/{ref}/proxy/endpoints/{name}` with `{upstream_url, auth, auth_name?, methods?, paths?, allowed_hosts?, enabled?}` creates (201) or replaces (200) an endpoint, omitted fields take their defaults; `DELETE /sites/{ref}/proxy/endpoints/{name}` answers 204. The key is never accepted or returned: a body with `secret` answers `400 proxy_secret_ui_only` with `details.secret_url`, the web interface screen where the user pastes it. Changing `upstream_url`, `allowed_hosts`, `auth` or `auth_name` erases a stored key. Other codes: `proxy_invalid` (400), `proxy_limit_reached` (10 per site, 403). The visitor path `/_dropsite/proxy/<name>/<path>` on site hosts is not part of API v1; its errors use the same JSON format with `proxy_origin` and `proxy_path` (403), `proxy_method` (405), `proxy_too_large` (413), `rate_limited` (429), `proxy_upstream` (502), `proxy_secret_missing` (503) and `proxy_timeout` (504).
