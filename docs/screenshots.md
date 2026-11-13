# Copies, templates, scheduled publishes and screenshots

## Copy of a site

`POST /api/v1/sites/{ref}/clone`, the MCP tool `clone_site`, `dropsite clone <site>` or the "Make a copy" button on the site page create a new site of the caller from the release being served. Only files are copied. Forms and delivery channels, site data (collections), own domains, the slug, password, noindex, expiry and analytics counters stay with the original; the counter block dropsite inserted into its pages is removed, and the copy gets its own counters like any new release. The copy counts against the caller's site and deploy limits.

## Templates

Five starter sites are built into the binary: `cafe`, `services`, `event`, `portfolio` and `links`. Each is a few kilobytes of HTML and CSS with SVG icons, works from 360 px wide, has Russian and English texts, `404.html` and a favicon, and loads nothing from other servers. `GET /api/v1/templates`, `POST /api/v1/templates/{name}/sites` (`title`, `slug`, `lang`), the MCP tools `list_templates` and `create_site_from_template` and the "Start from a template" panel create and publish such a site; the title is escaped into the pages. Change the sample texts afterwards like any site.

## Scheduled publishes

A release can go live at a given time:

- an existing ready release of the site that is not being served: `POST /api/v1/sites/{ref}/schedules` (`deploy_id`, `publish_at`), the site page in the web interface, or `schedule_publish` with `site` and `deploy_id`;
- an upload session: `POST /api/v1/uploads/{id}/schedule` (`publish_at`) or `schedule_publish` with `upload_id`, instead of committing it. The session closes into a **prepared release**: it is in the version list (and has a preview link in wildcard mode) but visitors keep seeing the current release.

`publish_at` is RFC 3339 with a time zone, in the future and at most 365 days ahead. A site has at most 20 pending publishes; a release waits for at most one. A worker in `control` checks every 15 seconds and switches the site exactly like a rollback; publishes missed while `control` was stopped run right after it starts. A deleted (or expired) site cancels its publishes; a release that is gone fails them. Pending ones can be cancelled (`DELETE /api/v1/schedules/{id}`, `cancel_scheduled_publish`); the prepared release then stays in the history. Old-release pruning (`KEEP_RELEASES`) and "rollback to the previous release" skip releases that wait for a publish. Counters are inserted when the release is prepared. The audit log records `schedule.create`, `schedule.cancel`, `schedule.publish` and `schedule.fail`.

## Screenshots and visual diff

`control` never runs a browser. Screenshots are taken by an optional **renderer**: [Gotenberg](https://gotenberg.dev) 8 with Chromium, in its own container. When it is not configured, the screenshot endpoints answer `503 renderer_unavailable`, the MCP tools return the same error and the web interface hides the screenshot block; nothing else changes.

### Enable

In `deploy/.env`:

```
COMPOSE_PROFILES=renderer
RENDERER_URL=http://renderer:3000
```

then `docker compose up -d --wait`. The `renderer` service is attached only to the internal network `render` (`internal: true`): it cannot reach the internet or Caddy, only `control`. Expect about 1 GB of RAM for Chromium while it renders.

| Variable | Default | Meaning |
|---|---|---|
| `RENDERER_URL` | empty (off) | Base URL of the renderer |
| `RENDER_ADDR` | `:8083` | Listener of `control` the renderer loads pages from; started only with `RENDERER_URL` |
| `RENDER_BASE_URL` | `http://control:8083` | That listener as the renderer reaches it |

### How a screenshot is taken

1. `control` checks the caller's right to the site and release (foreign ones are 404), picks the viewport (`desktop` 1280×800 or `mobile` 390×844) and creates a random token valid for 2 minutes and bound to that one release.
2. It asks Gotenberg (`POST /forms/chromium/screenshot/url`) to open `RENDER_BASE_URL/` with the header `X-Dropsite-Render-Token` on every request.
3. The render listener serves the files of exactly that release, read-only, for a valid token and nothing else. This works for releases that are not being served and for password-protected sites; `/_form` and `/_dropsite` do not work on screenshots, and resources on other servers are not loaded.
4. The PNG of the first screen is cached under `STATE_DIR/screenshots` (releases never change) and removed after 30 days. Each user may trigger 30 renders per minute.

A visual diff screenshots two releases (by default the second is the current one), lays them on a common canvas and counts pixels whose colour differs by more than 24 in any channel. The answer is the share of changed pixels (0–100, two decimals) and a PNG "before | after" with the changes highlighted in red on the right half.

Endpoints: `GET /api/v1/sites/{ref}/screenshot?deploy_id=&device=` (PNG) and `GET /api/v1/sites/{ref}/visual-diff?from=&to=&device=` (JSON with the PNG in base64). MCP: `screenshot_site` and `compare_versions` return the picture as an image block, so the assistant can look at its own work.
