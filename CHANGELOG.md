# Changelog

All notable changes to dropsite are documented in this file. The format is
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the
project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Release sections are generated from the Conventional Commits since the
previous release with `make changelog V=X.Y.Z` and reviewed before the tag is
pushed; see [docs/releasing.md](docs/releasing.md).

## [Unreleased]

## [0.5.0] - 2026-11-13

### Added

- Administrators see when a new release is out (a calm notice with a "what's new" link) and can update from Settings → Updates: the server backs up first and the sites keep working; assistants learn about it read-only through `check_updates` and `get_account`, and `UPDATE_CHECK=0` turns the check off.

### Changed

- The release workflow no longer repeats formatting, lint and tests: it
  requires the `ci` run on the tagged commit to have passed.

## [0.4.0] - 2026-11-13

### Added

- Opt-in access to the admin interface from the internet at `https://admin.<domain>` (installer question, default No, and Settings), and an Altcha check on the admin sign-in and invitation forms.

### Changed

- `dropsite-update` and `dropsite-rollback` print a short status per service
  instead of the list of changed files and the full container table.

## [0.3.0] - 2026-11-13

### Added

- Starter templates collect requests through the built-in forms (table
  booking, request, sign-up, contact) with a thanks page instead of mailto:
  and tel: buttons; the anti-robot check runs when the form is sent.

### Changed

- The server installer accepts Ubuntu 22.04 or newer and Debian 12 or newer
  (for example Ubuntu 26.04) instead of three fixed releases; a release Docker
  does not package yet is refused with a clear message.
- A form's result (saved or an error) now shows right above that form and the
  page returns to it; results from another page appear as a toast, and the
  create-site form no longer leaves a gap under the address hint.
- The server installer is quiet by default: four questions (the server name
  comes from the domain), a check mark per step, two DNS records and a short
  final block; the details go to `~/.dropsite/install-<name>.log` (mode 600),
  `--verbose` shows them on screen, and `--lang en|ru|zh` picks the language
  (each README starts it in its own language).

## [0.2.0] - 2026-11-13

### Changed

- Brand identity: the Dropsite logo (a laughing browser window with the
  wordmark), favicons and the "friendly ink" look across the web interface,
  the protected-site sign-in page and the form reply page: warm paper, ink
  outlines on everything you can press, thin warm edges on cards and lists,
  one sunny accent for primary actions and focus. Interface text uses the
  self-hosted Nunito font; the README shows the logo.
- Denser cards and lists, especially on phones, and one status badge for
  every state (live, draft, pending, error, quality score, DNS checks).

## [0.1.0] - 2026-11-13

First public release.

### Added

- Publishing of static sites on `<slug>.<base-domain>` with automatic HTTPS
  (on-demand or wildcard certificates), served by Caddy from read-only
  releases; the sites stay up while `control` is down.
- Remote MCP server with OAuth 2.1 for ChatGPT and Claude, a local stdio MCP
  server, an HTTP API with OpenAPI and the `dropsite` CLI.
- Drafts with a preview address, scheduled publishing, versions and rollback,
  clone, screenshots (optional renderer) and a quality report for each
  release with WebP, sitemap, robots and favicon optimization.
- Site forms with Telegram, e-mail and webhook delivery, Altcha anti-bot and
  CSV export; site data collections (public or moderated) with schema
  templates; Google Sheets sync; a secret proxy for third-party API keys.
- Password-protected sites, noindex, expiry, own domains with administrator
  approval, statistics and counters, shared workspaces and an audit log.
- Web interface in English, Russian and Simplified Chinese with a getting
  started checklist and step-by-step chat connection guides.
- One-command server installer for macOS and Linux, daily backups,
  `dropsite-update`, `dropsite-rollback` and `dropsite-admin-password`.
- Prebuilt multi-arch images on GHCR and CLI binaries with checksums and
  attestations, published from version tags.

### Security

- The admin interface refuses to start on a public address unless
  `ALLOW_PUBLIC_ADMIN=1` is set; outside Docker it listens on `127.0.0.1`.
- Password-protected sites answer with `Cache-Control: private, no-store`.
- Only the rightmost `X-Forwarded-For` hop is trusted behind the proxy.
- Workspace member additions do not reveal whether an account exists and are
  rate limited; forms refuse browser posts marked cross-site.
- The installer does not keep the first administrator password on the server.
- Threat model and operator guidance in [docs/security.md](docs/security.md).

[Unreleased]: https://github.com/b0balYson/dropsite/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/b0balYson/dropsite/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/b0balYson/dropsite/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/b0balYson/dropsite/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/b0balYson/dropsite/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/b0balYson/dropsite/releases/tag/v0.1.0
