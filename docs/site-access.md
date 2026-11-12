# Site access controls

Three per-site settings and per-user limits. All of them are changed in the web interface, through `PATCH /api/v1/sites/{ref}` (see [api-contract.md](api-contract.md)) or, for the expiry, with `dropsite deploy --expires`.

## How it works

`control` keeps the state in the database and mirrors two flags into marker files on the sites volume:

```text
/data/flags/noindex/<hostname>     X-Robots-Tag: noindex, nofollow
/data/flags/protected/<hostname>   Caddy asks control before serving
```

Markers exist for every address of the site: the random address, the slug, the base domain root and, in wildcard mode, every release preview. They are created before a new address starts serving and removed after an address is released, so an address is never less restricted than its site. On start `control` brings the markers in line with the database.

Caddy checks the markers with a `file` matcher on every request, so nothing is reloaded:

1. `/_dropsite/*` is proxied to `control` (the sign-in page) before any check.
2. With the `noindex` marker the response gets `X-Robots-Tag: noindex, nofollow` (also on errors and redirects).
3. With the `protected` marker Caddy calls `forward_auth` on `control:8081/internal/auth-check`. `control` answers 200 (serve), 302 to `/_dropsite/login?next=<path>` (no valid cookie) or an error. Only then come the usual rules, so `404.html`, `200.html` and unknown paths of a protected site are never shown without access.
4. If `control` cannot be reached, the protected site answers 503 with a short "temporarily unavailable" page. Open sites never reach `control` and keep working.

## Password

- At least 10 characters (the same rule as account passwords), stored as an argon2id hash and never returned by the API.
- The sign-in page is neutral: it shows the site title and no platform name, in Russian or English by the browser language. The form is protected against CSRF (a token bound to a nonce cookie and the address, valid for an hour, plus an `Origin` check).
- A successful sign-in sets `dropsite_access`: `HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/`, without `Domain`, valid for 7 days. It is signed (HMAC-SHA256) with a key derived from a random installation secret stored in the database (and therefore in backups) and is bound to the site and to the current password version: changing or removing the password invalidates every cookie.
- Wrong passwords: at most 10 per 15 minutes from one IP for one site and 100 per 15 minutes for one site from all IPs; then the page answers 429 without checking the password.

## Lifetime

`expires_at` must be in the future and at most 10 years ahead. In the interface it is a day: the site works through that day (UTC). `control` checks every 30 seconds; an expired site is soft-deleted exactly like a deleted one (addresses stop working and are released at once, the audit log gets `site.expire`) and purged 7 days later. The interface warns 3 days before.

## Per-user limits

Administrators set personal values of `MAX_SITE_BYTES`, `MAX_FILE_BYTES`, `MAX_FILES`, `MAX_SITES_PER_USER` and `KEEP_RELEASES` on Users → Limits or with `PATCH /api/v1/admin/users/{id}` and `{"limits": {...}}`. The effective value is: user override → admin setting → environment → default. Uploads and release pruning follow the limits of the site owner, whoever publishes. `GET /api/v1/me` returns the effective limits, so the CLI checks archives against them.
