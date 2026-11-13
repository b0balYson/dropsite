# Security model

This page describes what dropsite protects, what it deliberately leaves to the operator and which settings matter. It complements the hardening already built into the code: argon2id passwords, hashed sessions and tokens, CSRF protection, SSRF guards with a dial-time address check, safe archive extraction, parameterized SQL, a strict CSP and distroless non-root containers.

## Deployment model

One server runs Caddy (TLS and serving of published sites) and `control` (API, MCP endpoint, web interface, deploys) with Docker Compose. Only ports 80 and 443 face the internet. The web interface is a separate plain-HTTP listener on port 8082 that is meant for the operator's own machine or a private network; an administrator can additionally open it to the internet over HTTPS at `https://admin.<BASE_DOMAIN>` (off by default, see [below](#admin-interface-from-the-internet)). Everything below assumes this single-node model.

## Web interface (admin UI)

The interface listens on `ADMIN_BIND_IP:8082`, by default `127.0.0.1:8082`, and uses plain HTTP: the session cookie gets the `Secure` flag only when the request arrives over TLS. Reach it in one of two ways:

- **SSH tunnel (default, recommended):** `ssh -L 8082:127.0.0.1:8082 server`, then open `http://127.0.0.1:8082`. The server installer writes this tunnel into `~/.ssh/config` for you.
- **Private network:** join the server to an overlay network (Tailscale, WireGuard, NetBird) and set `ADMIN_BIND_IP` to its address in that network.
- **From the internet (opt-in, a potential threat):** `https://admin.<BASE_DOMAIN>` through Caddy, turned on by an administrator in Settings or by the installer. See [Admin interface from the internet](#admin-interface-from-the-internet).

`control` checks the address at start:

| Address | Result |
| --- | --- |
| Loopback (`127.0.0.1`, `::1`) | starts |
| Private network: RFC 1918 (`10/8`, `172.16/12`, `192.168/16`), CGNAT `100.64.0.0/10` (Tailscale), IPv6 ULA `fc00::/7`, link-local | starts and logs a warning that the interface is served over plain HTTP |
| Public address, `0.0.0.0`, `::` or an empty host | **refuses to start** unless `ALLOW_PUBLIC_ADMIN=1` |

In Docker Compose `control` always listens on `:8082` inside the container (`ADMIN_ADDR`), so the check reads the published `ADMIN_BIND_IP`. A binary started outside Docker checks the host of `ADMIN_ADDR`, which defaults to `127.0.0.1:8082`.

Set `ALLOW_PUBLIC_ADMIN=1` only when something else protects the port: TLS termination in front of it and a firewall that admits known addresses. TLS on the admin listener itself is not built in: the tunnel already encrypts the traffic.

### Sign-in check (Altcha)

The sign-in form and the invitation (set-password) form carry an Altcha proof of work, on the loopback and the public listener alike. The widget solves it in the browser when the form is submitted; the server verifies the solution (signed, bound to the request host and to the admin purpose, single use) **before** the password is checked, and an address that is already over the sign-in limit is refused before any work is spent. Solutions of site forms or of a protected site's login page are not accepted. The difficulty is the forms base level (Settings → forms) and rises one step (×4) for an address after a failed sign-in; it does not rise with every challenge, because everyone behind the SSH tunnel shares one address. Without JavaScript the form cannot be submitted, and the page says so. The password check itself is unchanged: argon2id with a dummy hash for unknown users, and the per-IP and per-account limits.

### Sessions

A session lasts 14 days and is extended while it is used. It is not bound to an IP address or browser, and there is no second factor. This is acceptable for an interface that only the operator's machine or a private network can reach; it is the reason the interface must not be exposed more widely. Blocking a user ends all of their sessions and API access at once; `control reset-password` ends the sessions of that user.

### The first administrator's password

`ADMIN_EMAIL` and `ADMIN_PASSWORD` create the first administrator when the database is empty and are ignored afterwards. A password left in `.env` stays visible through `docker inspect` and would become valid again if the database were ever recreated empty, so:

- the server installer shows the generated password once, removes `ADMIN_PASSWORD` from `.env` after the first healthy start and recreates `control` without it;
- `control` logs a warning when `ADMIN_PASSWORD` is still set although users exist;
- a forgotten password is reset on the server with `dropsite-admin-password` (installer) or `docker compose exec -T control /control reset-password [email]`.

`deploy/local.env` contains a public password and is for local development only; never use it on a server.

## Admin interface from the internet

Off by default. When an administrator turns it on, the same web interface is served at `https://admin.<BASE_DOMAIN>`; the SSH tunnel keeps working. **This is a potential threat:** anyone who knows or guesses the address can reach the sign-in page and try passwords, and every vulnerability of the interface becomes reachable from the internet. Keep it off and use the SSH tunnel unless you need it; if you turn it on, give every user a strong unique password and turn it off again when you no longer need it.

How it is built:

- **Switch.** Settings → "Access to the admin interface from the internet". Only an administrator signed in to the web interface can change it; turning it on needs the "I understand the risk" checkbox and the administrator's current password (checked under the sign-in limits). API tokens, OAuth grants and AI assistants (MCP) cannot read or change it. Every change is in the audit log. The installer asks once (default No) and passes the answer with `control public-admin on|off`; it is stored in the database, never in `.env`. While it is on, links to the interface that are made at that moment (new invitations, the `cabinet_url` the assistant and the API get, the forms and proxy screens) use the public address; otherwise they use `UI_URL`. An invitation token works on both listeners whatever its link says.
- **Caddy.** A static site block for `admin.<BASE_DOMAIN>` always exists and proxies to `control:8084`, a separate listener that is never published to the host. Caddy is never reloaded. The block adds HSTS, `X-Frame-Options: DENY` and `noindex`, and writes no access log (invitation links carry their secret in the path).
- **Certificates.** In ondemand mode the permission endpoint allows `admin.<BASE_DOMAIN>` only while `/data/tls-hosts/admin.<BASE_DOMAIN>` exists; `control` writes it when public access is turned on, removes it when it is turned off and repairs it at every start from the database. So no certificate is requested while it is off. In wildcard mode the `*.<BASE_DOMAIN>` certificate already covers the name; nothing is issued for it.
- **The listener.** While public access is off, every request to `:8084` (pages, static files, the Altcha challenge, anything) gets a bare `404` before any cookie is set. It also answers `404` to any `Host` other than `admin.<BASE_DOMAIN>` (with the HTTPS port in local development).
- **Client address.** On this listener `control` reads exactly one `X-Forwarded-For` hop, the rightmost, and only from a peer in `TRUSTED_PROXIES` (the Compose network): the address Caddy writes. Sign-in limits and audit entries are therefore per real client. The loopback listener still never reads the header.
- **Cookies and CSRF.** The admin host lies under the base domain, whose other subdomains run user JavaScript (published sites) and are same-site with it. Therefore every cookie on this listener is `Secure`, `HttpOnly` and carries the `__Host-` prefix, which a sibling subdomain cannot set or overwrite, and cookies without the prefix are ignored. Unsafe requests need the CSRF token, `Sec-Fetch-Site: same-origin` (when the browser sends it; `same-site` is refused) and, when present, an `Origin` exactly equal to `https://admin.<BASE_DOMAIN>`. The CSP, frame and referrer headers are those of the loopback listener.
- **Other containers.** The optional screenshot renderer shares a network with `control` and could open a connection to `:8084`, but the browser cannot set the `Host` header, so it only ever gets `404`.

`ALLOW_PUBLIC_ADMIN` is unrelated: it still governs publishing the plain-HTTP port 8082 on a public `ADMIN_BIND_IP`, and the public admin listener does not count as such.

## Client addresses and proxies

The client IP is the key of every per-IP rate limit (sign-in, API tokens, forms, site data, the secret proxy, OAuth) and is written into the audit log.

- Caddy replaces `X-Forwarded-For` on every request it forwards to `control` with exactly one address, `{client_ip}`: the address of its peer, whatever the client sent.
- `control` reads `X-Forwarded-For` only when the connection comes from `TRUSTED_PROXIES` (default: RFC 1918 and loopback, which covers the Compose network), and then believes only the **rightmost** address. A client in a trusted network (VPN, LAN, another container) therefore cannot prepend a forged address.
- The web interface on the loopback listener never reads `X-Forwarded-For`: there is no proxy in front of it. The public admin listener reads one hop as described [above](#admin-interface-from-the-internet).

### A CDN or load balancer in front of Caddy

Without further setup Caddy sees the CDN as the client: every visitor then shares the CDN's addresses, so the limits get stricter (they are never bypassed). To keep per-visitor limits, tell Caddy whom to trust for the client address with `CADDY_SERVERS_EXTRA` in `.env`, for example for Cloudflare:

```bash
CADDY_SERVERS_EXTRA="trusted_proxies static 173.245.48.0/20 103.21.244.0/22 103.22.200.0/22 103.31.4.0/22 141.101.64.0/18 108.162.192.0/18 190.93.240.0/20 188.114.96.0/20 197.234.240.0/22 198.41.128.0/17 162.158.0.0/15 104.16.0.0/13 104.24.0.0/14 172.64.0.0/13 131.0.72.0/22
client_ip_headers CF-Connecting-IP"
```

List the provider's current ranges, then `docker compose up -d caddy`. Caddy then forwards the visitor's address as the single hop and `control` needs no change. Do not widen `TRUSTED_PROXIES` for a CDN: it describes the hop between Caddy and `control` only.

## Rate limits

Limits are kept in the memory of `control`: a restart (every update) resets them, and several `control` replicas would not share them. This fits the single-node model; a shared store would be needed before running more than one instance.

## Password-protected sites and caches

Every answer of a site protected by a visitor password carries `Cache-Control: private, no-store`: HTML, assets, 404 pages, `/_dropsite/data` and `/_dropsite/proxy`. A CDN or shared cache in front of Caddy therefore never stores them and cannot serve them to visitors who did not sign in. Open sites keep `no-cache` for HTML and `public, max-age=3600` for assets.

## Account privacy

Adding a member to a workspace answers the same way for every e-mail without an active account (unknown, blocked or not yet activated), and a user may try at most 20 additions an hour. The exact reason is written to the audit log only (`workspace.member.refused`). Sign-in answers are uniform and timing-equalized as well.

## Site forms

Forms refuse posts whose `Origin` names another site and posts that a browser marks `Sec-Fetch-Site: cross-site`. These checks stop casual cross-site posting only: requests without browser headers (scripts, sandboxed frames with `Origin: null`) pass them. Spam is stopped by Altcha, the honeypot field, the minimum fill time and the per-IP and per-site limits ([forms.md](forms.md)).

## Updates

- **Outgoing request.** About twice a day `control` asks `https://api.github.com/repos/b0balYson/dropsite/releases/latest` for the newest release (`UPDATE_CHECK=0` turns it off). The client has timeouts, reads at most 1 MB, follows no redirects, talks only to that host and connects only to public addresses. Nothing about the installation is sent beyond a plain `User-Agent: dropsite-update-check`. The release link shown is built from the version, never taken from the answer.
- **Who can update.** Only an administrator signed in to the web interface (the loopback listener or the public admin address, with the same CSRF and origin checks) can press **Update**, after a confirmation; every request is in the audit log. API tokens, OAuth grants and AI assistants (MCP) cannot: `check_updates` and `GET /api/v1/updates` only read, and a test keeps every API and MCP path away from the request file, so content an assistant reads (a page, a form submission, a prompt injection) can never make the server update itself.
- **The request file.** `control` keeps no Docker socket. It writes `update/request` in its state volume: two lines, `version=X.Y.Z` and `admin=<user id>`, and refuses a second request while one waits or runs. The root job `dropsite-update-watch` treats the file as hostile, as if a compromised `control` had written it: it reads at most 256 bytes and removes the file, accepts only exactly those two lines with `X.Y.Z` digits and a numeric ID, requires a tag `vX.Y.Z` that exists in the repository and is newer than the running version, never evaluates or executes anything from the file, and runs only `dropsite-update --to X.Y.Z`. Every file operation inside the volume (reading the request, writing the status and the heartbeat) runs as `control`'s own user through `setpriv`, so a symbolic link planted there cannot make root read or overwrite a file. One update runs at a time (a lock on the host). The worst a compromised `control` can do is start an update to an existing newer release, which is what the button does anyway.

## Google Sheets

All users share one Google service account of the installation, so its access is the access of every user. Restrict it as described in the checklist of [sheets.md](sheets.md#checklist-what-the-service-account-may-reach). Separate Google access per user is not available.

## Published content and abuse

Every user can publish arbitrary HTML and JavaScript on `<site>.<base domain>`. This is the purpose of the product, and it also allows phishing pages or malware hosting by a malicious or compromised account. The defences are organizational:

- Use a base domain that hosts nothing else and shares no cookies with other services. For installations with untrusted users, add it to the [Public Suffix List](https://publicsuffix.org/) so browsers isolate the sites from each other.
- Give accounts only to people you trust; there is no self-registration.

When a report of abuse arrives:

1. Find the site: *Administration → All sites*, search by address, title or owner e-mail.
2. Take it offline: delete the site from its page (it disappears from the address at once and is purged after 7 days), or protect it with a password if you need to keep looking at it.
3. Stop the account: *Administration → Users → Block*. Sign-in, API tokens and AI connections stop working at once.
4. Keep the evidence: the audit log (*Administration → Audit log*) records who published what and from which address; make a backup with `dropsite-backup` before purging.
5. Answer the reporter and, if the base domain was flagged by a browser or mail provider, ask for a review after the content is gone.
