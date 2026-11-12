# Release quality report

Every publication of new content (web interface, CLI, API, MCP `deploy_files`, `deploy_folder`, `update_site_files`, `commit_upload`) gets a static quality report, computed from the release's files after optimization and before analytics counters are inserted. It reads files only: no network, no rendering, no scripts. A counter change copies the report of the release it re-publishes; a rollback reuses the target's report. A failed or panicking analysis leaves the release without a report and never fails the publication. Releases published before reports existed have `quality: null`.

## Checks

| Category | What counts |
|---|---|
| `broken_links` | `<a href>`/`<area href>` to a site path that is not served: the file, `<path>.html` or `<path>/index.html`. Query and fragment are ignored, percent-encoding is decoded. Not checked when `200.html` (SPA mode) exists at the root. |
| `missing_assets` | Files loaded automatically but absent: `img`, `source`, `srcset`, `script`, `link` (stylesheet, icon, preload, modulepreload, manifest), `video`, `audio`, `poster`, `track`, `embed`, `url()` and `@import` in stylesheets, `<style>` and `style` attributes. |
| `external_resources`, `external_hosts` | The same kinds of files loaded from other servers (`http://`, `https://`, `//`). Ordinary links and `iframe` embeds are not counted. Absolute addresses of the site's own hosts are checked as internal. |
| `images_without_alt` | `<img>` without an `alt` attribute (empty `alt` is fine for decorative images). |
| `pages_missing` | Pages without a non-empty `<title>`, `<meta name="description">`, `<meta name="viewport">` or `lang` on `<html>`. |
| `heaviest_pages`, `heavy_pages` | Top five pages by HTML plus unique local files they load (including what their stylesheets load, one level); heavier than 3 MB counts as heavy. |
| `largest_files` | Top five files by size. |

`/_form*`, `/_dropsite*`, `mailto:`, `tel:`, `javascript:`, `data:` and anchors are never problems. The generated `robots.txt`, `sitemap.xml` and `favicon.ico` (docs/publishing.md) count as present. The generated `.dropsite/` directory is not analyzed. `optimization` summarizes what publish-time optimization added.

## Score

100 minus capped penalties: broken links and missing files 5 each (at most 25 per category), each external host 5 (at most 20), missing title or viewport 5 per page (at most 10 each), missing description or `lang` 2 per page (at most 6 each), image without alt 1 (at most 10), heavy page 5 (at most 10). Grade A from 90, B from 75, C from 50, else D. `summary` is one English sentence; the short form in publication results (`quality`) adds up to five `issues`, most important first.

## Limits

At most 1000 pages and 500 stylesheets are read, 2 MB per page and 1 MB per stylesheet; every list keeps at most 50 items while `counts` holds the full numbers. `truncated` is true when a limit was hit.

## Where to see it

- API: `GET /api/v1/sites/{ref}/deploys/{id}` (`id` may be `current`) returns the release with `quality`; publication, file edit, upload commit and rollback responses carry the short `quality`.
- MCP: `get_quality_report`; `quality` in the results of `deploy_files`, `deploy_folder`, `update_site_files`, `commit_upload` and `rollback`.
- Web interface: the score next to every release in the history links to the report screen.
- CLI: `dropsite deploy` prints the summary after publishing.

Reports are deleted together with pruned releases and purged sites.
