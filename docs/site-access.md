# Site access controls

Three per-site settings and per-user limits. All of them are changed in the web interface, through `PATCH /api/v1/sites/{ref}` (see [api-contract.md](api-contract.md)) or, for the expiry, with `dropsite deploy --expires`.

## How it works

`control` keeps the state in the database and mirrors two flags into marker files on the sites volume:

```text
/data/flags/noindex/<hostname>     X-Robots-Tag: noindex, nofollow
/data/flags/protected/<hostname>   Caddy asks control before serving
```

Markers exist for every address of the site: the random address, the slug, the base domain root, approved external domains and, in wildcard mode, every release preview. A pending or rejected external domain is not an address and gets none. They are created before a new address starts serving and removed after an address is released, so an address is never less restricted than its site. On start `control` brings the markers in line with the database: missing markers first, then the `by-host` links, and stray markers only after stray links are gone. Changes of the base domain root lock both the new and the previous site, and an expired site is re-checked under its lock before it is taken down, so a concurrent change by the owner wins.

Caddy checks the markers with a `file` matcher on every request, so nothing is reloaded:

1. `/_dropsite/*` is proxied to `control` (the sign-in page, its Altcha challenge and the widget script) before any check.
2. With the `noindex` marker the response gets `X-Robots-Tag: noindex, nofollow` (also on errors and redirects).
3. With the `protected` marker Caddy calls `forward_auth` on `control:8081/internal/auth-check`. `control` answers 200 (serve), 302 to `/_dropsite/login?next=<path>` (no valid cookie) or an error. Only then come the usual rules, so `404.html`, `200.html` and unknown paths of a protected site are never shown without access.
4. If `control` cannot be reached, the protected site answers 503 with a short "temporarily unavailable" page. Open sites never reach `control` and keep working.

## Password

- At least 10 characters (the same rule as account passwords), stored as an argon2id hash and never returned by the API.
- The sign-in page is neutral: it shows the site title and no platform name, in Russian or English by the browser language. The form is protected against CSRF (a token bound to the `__Host-dropsite_login_nonce` cookie and the address, valid for an hour, plus an `Origin`/`Sec-Fetch-Site` check).
- A successful sign-in sets `__Host-dropsite_access`: `HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/`, without `Domain`, valid for 7 days. It is signed (HMAC-SHA256) with a key derived from a random installation secret stored in the database (and therefore in backups) and is bound to the site and to the current password version: changing or removing the password invalidates every cookie.
- Both cookies carry the `__Host-` prefix: browsers accept them only host-only, `Secure` and with `Path=/`, so another site under the same base domain cannot plant a cookie of that name (cookie tossing). If several cookies of the name still arrive, `control` accepts the request when any of them is valid. Visitors signed in before this change (cookie `dropsite_access`) sign in once more.

### Anti-robot check (Altcha)

Every sign-in carries a self-hosted [Altcha](https://altcha.org) proof of work; it is always on and has no setting. The widget (the same embedded script as for forms, `/_dropsite/altcha.js`) starts solving as soon as the page opens, while the visitor types; there is no puzzle to click through. At the base difficulty set by the administrator for forms (default `altcha_max_number` 100 000) this takes about 1 second on a desktop and a few seconds on a phone. Without JavaScript the page shows a note that it is required.

- The challenge comes from `GET /_dropsite/login/challenge` on the site's address. It lies under `/_dropsite/*`, which Caddy proxies before `forward_auth`, so it works without the access cookie (`/_form/challenge` is behind `forward_auth` and does not). It answers 404 on addresses of open sites.
- A challenge is signed with the address and its purpose (`login` or `form`): a solution for a site form cannot be spent on the sign-in page and vice versa. Each solution is accepted once.
- `control` checks the solution **before** the password and before any failure counter. A missing, invalid, expired, foreign or reused solution gets the form back with a neutral error (400); the password is not checked (argon2id is not run) and nothing is counted.
- Difficulty grows per source (IP; IPv6 per /64) and site: each challenge and each failed sign-in within 10 minutes multiply it by 4, up to ×256. A visitor who mistypes once waits about 13 s on the second try (desktop), the third try takes minutes, so guessing is impractical after 2–3 attempts. The level decays as events leave the 10-minute window. Challenges are limited to 60 per 10 minutes per IP.

### Wrong passwords

- Per source and site: at most 10 failed attempts per 15 minutes (IPv6 addresses are counted per /64, since one subscriber usually holds a whole /64). Past the limit the page answers 429 at once, without checking the Altcha solution or the password, even a correct one.
- Per site, from all sources: 100 failures per 15 minutes put the site "under attack". This **never refuses** a sign-in: a fixed per-site lockout would let anyone lock the owner's visitors out with a few addresses. Instead every sign-in challenge of the site is at least ×16 the base difficulty (about 13 s on a desktop) until the failures leave the window.
- Only attempts with a valid Altcha solution count toward either limit, so requests without proof of work cannot lock anyone out.
- Counters and difficulty levels live in the memory of `control`; a restart resets them.

## Lifetime

`expires_at` must be in the future and at most 10 years ahead. In the interface it is a day: the site works through that day (UTC). `control` checks every 30 seconds; an expired site is soft-deleted exactly like a deleted one (addresses stop working and are released at once, the audit log gets `site.expire`) and purged 7 days later. The interface warns 3 days before.

## Per-user limits

Administrators set personal values of `MAX_SITE_BYTES`, `MAX_FILE_BYTES`, `MAX_FILES`, `MAX_SITES_PER_USER` and `KEEP_RELEASES` on Users → Limits or with `PATCH /api/v1/admin/users/{id}` and `{"limits": {...}}`. The effective value is: user override → admin setting → environment → default. Uploads and release pruning follow the limits of the site owner, whoever publishes. `GET /api/v1/me` returns the effective limits, so the CLI checks archives against them.
