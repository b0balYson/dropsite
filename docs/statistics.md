# Visit statistics and analytics counters

## How visits are collected

Caddy writes one JSON line per request to a published site into `/logs/sites-access.log` on the `logs` volume (both TLS modes; API requests are not included). The same lines also go to Caddy's stdout. Caddy rotates the file at 50 MB and keeps 5 old files uncompressed, so the volume holds at most about 300 MB.

`control` mounts the volume read-only and reads new lines every `STATS_INTERVAL` (default one minute). It remembers the file's inode and offset in the database together with the counts, in one transaction, so a restart, a rotation or a truncation never counts a line twice. After a rotation it finishes the old file first. Serving never depends on this: if `control` is stopped, Caddy keeps writing and `control` catches up later, as long as the unread part has not been rotated away (it then logs a warning).

A **page view** is a `GET` answered with 200 or 304 for an HTML page: `/`, a path ending in `/`, `.html`, `.htm` or without an extension, and `text/html` when the response says so. 304 counts because HTML is served with `Cache-Control: no-cache`, so every repeat visit is a revalidation. Images, styles, scripts, 404s, errors and robots (empty User-Agent or one containing `bot`, `crawl`, `spider`, `curl`, `wget`, `python`, `headless`, `lighthouse` and similar) are not counted. Release previews (`p-<release>-<site>.<BASE_DOMAIN>` in wildcard mode) are review traffic and are not counted either.

For each site and UTC day the database keeps views, unique visitors, views per page (path without the query) and per referrer host. A site gets at most 500 page rows and 200 referrer rows per day; the rest is shown as "Other". Navigation within the site is not a referrer; a missing `Referer` is a direct visit. Aggregates are kept for 400 days and deleted together with the site.

## Privacy

Raw lines, IP addresses and User-Agent strings are never stored in the database. A visitor is the first 16 bytes of HMAC-SHA256 over IP address and User-Agent, keyed with a random salt of the day. The salt and the visitor hashes are deleted by the first collection after the day ends, after which the hashes cannot be linked to anyone; lines of a closed day read later (after a long outage) count as views only. Visitors over a period are the sum of daily values, so one person visiting on three days counts three times.

The raw access log on the `logs` volume does contain IP addresses until rotation removes it. Treat the volume like any server log.

## Analytics counters

A site owner may enter a **Yandex Metrica** counter number (4 to 12 digits) and/or a **Google Analytics 4** Measurement ID (`G-` and 4 to 16 letters or digits) on the site screen. An administrator may enter global IDs in Settings; they are added to every site on top of its own IDs. Only IDs are accepted, so a setting can never inject arbitrary markup.

dropsite inserts the standard code (Metrica `tag.js` with click map and link tracking, without Session Replay; GA4 `gtag.js`) between `<!-- dropsite:counters -->` markers:

- before the first `</head>`; without one, before the first `<body`; otherwise at the end of the file (never before `<!doctype>`, which would switch the page to quirks mode);
- in every `.html` and `.htm` file of the release; other files and UTF-16 files are left byte for byte;
- rewritten files get the time of the release, so browsers do not keep the old page (ETag is derived from mtime and size); unchanged files keep theirs.

Releases are immutable, so:

- **a new publication** gets the current site and global IDs;
- **changing a site's IDs** creates a new release — a copy of the current one with the block replaced or removed — and switches to it like a publication. It counts against the limit of 30 publications per hour, older releases keep their old code and rollback brings it back;
- **changing global IDs** reaches each site with its next publication or change of its own IDs.

Size limits apply to the archive content; the inserted code (about 1 KB per page) is included in the release size shown in the history.

## API

`GET /api/v1/sites/{ref}/stats?days=30` (1 to 400 days ending today) returns every day with `views` and `visitors`, totals, and the top 10 pages and referrers. See [api-contract.md](api-contract.md).
