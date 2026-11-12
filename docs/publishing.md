# Publishing pipeline

Every publication (web interface, CLI, API, MCP `deploy_files`, `update_site_files`, `commit_upload`) extracts and checks the archive, then optionally optimizes the new release, inserts analytics counters and switches the site atomically.

## Optimization

Optimization never changes, removes or renames a file of the site, HTML included. Everything it creates lives in the release's `.dropsite/` directory:

| Generated file | Served |
|---|---|
| `.dropsite/webp/<path>.webp` for `.jpg`, `.jpeg`, `.png` (any case) | instead of `<path>` when the request's `Accept` contains `image/webp`; every response for such a path carries `Vary: Accept` |
| `.dropsite/static/favicon.ico` | at `/favicon.ico` when the site has no own file there |
| `.dropsite/static/robots.txt`, `sitemap.xml` | at `/robots.txt`, `/sitemap.xml` when the site has no own file there; not generated for a site hidden from search at publish time |

Caddy decides with static rules (`deploy/Caddyfile`); direct requests to `/.dropsite/...` are 404 like every hidden path. An archive's own `.dropsite/` directory at the root is skipped like `.git/`. The site file tools of the API and MCP never show generated files, and they do not count towards `file_count`, `size_bytes` or limits.

- **Images.** WebP has the same size in pixels as the original (JPEG lossy at quality 80, PNG lossless). A variant is kept only when it is at most 90 % of the original and an independent decoder reads it back with the same size. Images under 8 KB, over 16 megapixels, with content not matching the extension, or broken are left alone. Images are never resized: that would change how pages without explicit sizes look.
- **Sitemap.** All `.html`/`.htm` pages except `404.html` and `200.html` at the root, hidden paths and pages with `<meta name="robots" content="noindex">`; `index.html` becomes its directory. Addresses use the site's primary address at publish time.
- **robots.txt.** Allows everything and points to the site's own or the generated sitemap.
- **favicon.ico.** A round icon whose colour depends on the site ID.

Not done on purpose: HTML/CSS/JS minification (Caddy already compresses with zstd/gzip, build tools already minify, and rewriting user code risks changing its behaviour), resizing, AVIF, rewriting links.

### Limits and failure handling

- One optimization runs at a time per installation. A publication spends at most 20 seconds on it, including the wait for its turn; images left after that get no variant in this release. Fallback files are generated first.
- Any error or panic only skips the affected file or step and is logged; a publication never fails because of optimization.

### Switches

- Installation: `PUBLISH_OPTIMIZE=off` turns it off for everyone (default `on`).
- Site: `PATCH /api/v1/sites/{ref}/publish-settings` `{"optimize": false}`, the MCP tool `set_publish_settings` or the "Publishing" block of the site screen. Applies from the next publication; turning it back on and publishing again restores the generated files.
