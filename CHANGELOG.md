# Changelog

All notable changes to dropsite are documented in this file. The format is
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the
project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Release sections are generated from the Conventional Commits since the
previous release with `make changelog V=X.Y.Z` and reviewed before the tag is
pushed; see [docs/releasing.md](docs/releasing.md).

## [Unreleased]

### Breaking changes

- `control` refuses to start when the admin UI would be reachable on a public
  address: a public `ADMIN_BIND_IP` (or `0.0.0.0`) in compose, or a public or
  empty host in `ADMIN_ADDR` outside Docker. Keep `ADMIN_BIND_IP=127.0.0.1`
  and use an SSH tunnel, or a private network address (RFC 1918, Tailscale
  `100.64.0.0/10`, IPv6 ULA); to accept the risk set `ALLOW_PUBLIC_ADMIN=1`.
  Check `.env` before updating ([docs/security.md](docs/security.md)).
- `ADMIN_ADDR` outside Docker now defaults to `127.0.0.1:8082` instead of
  `:8082`; the compose file sets `:8082` inside the container explicitly.

### Security

- Every answer of a password-protected site, files, 404 pages,
  `/_dropsite/data` and `/_dropsite/proxy` included, carries
  `Cache-Control: private, no-store`, so a CDN or shared cache never stores it.
- Behind a trusted proxy only the rightmost `X-Forwarded-For` address is used
  as the client IP: a client in a trusted network can no longer forge its rate
  limit key or audit address. The admin listener never reads the header.
- Adding a workspace member answers alike for every e-mail without an active
  user and is limited to 20 attempts an hour per user (`429 rate_limited`);
  the reason is recorded in the audit log only.
- Site forms refuse posts a browser marks `Sec-Fetch-Site: cross-site`.
- The server installer removes `ADMIN_PASSWORD` from `.env` once the first
  administrator exists and recreates `control` without it; the password is
  shown once and reset with `dropsite-admin-password`. `ADMIN_PASSWORD` is no
  longer required by the compose file, and `control` warns when it is still
  set although users exist.

### Documentation

- Threat model and operator guidance in [docs/security.md](docs/security.md);
  a checklist for the Google Sheets service account in
  [docs/sheets.md](docs/sheets.md).
